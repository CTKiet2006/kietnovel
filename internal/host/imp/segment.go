package imp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// BoundaryDecision is the model's boundary judgement for a single owned range (RFC §8.2).
type BoundaryDecision struct {
	UnitID    string `json:"unit_id"`
	Anchor    string `json:"anchor,omitempty"`
	Kind      string `json:"kind"` // chapter / group / front_matter / back_matter
	Title     string `json:"title,omitempty"`
	Uncertain bool   `json:"uncertain,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

const (
	kindChapter     = "chapter"
	kindGroup       = "group"
	kindFrontMatter = "front_matter"
	kindBackMatter  = "back_matter"
)

// boundaryBatch is the structured return of one segmentation call.
type boundaryBatch struct {
	Boundaries []BoundaryDecision `json:"boundaries"`
}

// ChapterSpan is one committable chapter after segmentation is confirmed: title + the normalized text byte range (including the title line).
type ChapterSpan struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Start  int    `json:"start_byte"`
	End    int    `json:"end_byte"`
}

// MatterSpan is a volume/part title or an explicitly ancillary region.
type MatterSpan struct {
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
	Start int    `json:"start_byte"`
	End   int    `json:"end_byte"`
}

// Segmentation is a segmentation result that passed whole-text coverage validation (the upstream of confirmation and per-chapter analysis).
type Segmentation struct {
	Chapters  []ChapterSpan `json:"chapters"`
	Matter    []MatterSpan  `json:"matter,omitempty"`    // group / front / back
	Uncertain []int         `json:"uncertain,omitempty"` // chapter numbers marked uncertain, so the preview can flag them
	Notes     []string      `json:"notes,omitempty"`     // notes that need manual checking at segmentation time (e.g. a placeholder title with an empty body folded into the previous segment)
}

// Content returns the normalized body of chapter i (including the title line).
func (s *Segmentation) Content(normalized []byte, i int) string {
	c := s.Chapters[i]
	return string(normalized[c.Start:c.End])
}

// resolveSegmentation maps the ordered boundary decisions to a Segmentation validated for whole-text coverage (RFC §8.3).
// A pure function: model output and code validation are kept separate, so Go does not re-judge "is this line a chapter title" -- but the coverage invariant must hold.
func resolveSegmentation(normalized []byte, units []SourceUnit, decisions []BoundaryDecision) (*Segmentation, error) {
	if len(decisions) == 0 {
		return nil, fmt.Errorf("未识别到任何边界")
	}
	// Precondition contract: units must be sorted in numeric (Line,Part) order (lexicographic ID order is forbidden).
	for i := 1; i < len(units); i++ {
		if !unitLess(units[i-1], units[i]) {
			return nil, fmt.Errorf("SourceUnit 未按 (Line,Part) 数值序排列：%s 后接 %s", units[i-1].ID, units[i].ID)
		}
	}
	unitByID := make(map[string]SourceUnit, len(units))
	for _, u := range units {
		unitByID[u.ID] = u
	}

	type point struct {
		byte int
		d    BoundaryDecision
	}
	points := make([]point, 0, len(decisions))
	for i, d := range decisions {
		switch d.Kind {
		case kindChapter, kindGroup, kindFrontMatter, kindBackMatter:
		default:
			return nil, fmt.Errorf("边界[%d] kind 非法：%q", i, d.Kind)
		}
		b, err := resolveBoundaryByte(unitByID, d.UnitID, d.Anchor)
		if err != nil {
			return nil, err
		}
		points = append(points, point{byte: b, d: d})
	}
	// The occasional out-of-order or duplicate output from the model is a coordinate-discipline problem, so Go repairs it deterministically instead of
	// issuing a final veto: discarding the whole segmentation stage because two boundaries came back swapped is an unacceptable cost (measured: 319 boundaries
	// once lost to a single in-chunk inversion, and the chunk cache would make the failure reproduce deterministically). Inter-chunk order is guaranteed by owned ranges not overlapping,
	// so out-of-order can only happen within a chunk: a stable sort by byte offset restores the true order with zero information loss; an exact-byte duplicate keeps the one that
	// appeared first and is recorded in Notes for a human to check in the confirmation preview.
	sort.SliceStable(points, func(i, j int) bool { return points[i].byte < points[j].byte })
	var notes []string
	uniq := points[:0]
	for _, p := range points {
		if n := len(uniq); n > 0 && uniq[n-1].byte == p.byte {
			// An exactly identical duplicate is mechanical redundancy and is silently deduped; a semantic conflict at the same position (different kind/title) was already
			// re-asked at call time, so anything reaching here can only come from a pre-repair stale cache -- keep the one that appeared first and record Notes for human review.
			if prev := uniq[n-1].d; prev.Kind != p.d.Kind || boundaryLabel(prev) != boundaryLabel(p.d) {
				notes = append(notes, fmt.Sprintf("边界 %q 与 %q 重合（byte %d），已保留前者",
					boundaryLabel(prev), boundaryLabel(p.d), p.byte))
			}
			continue
		}
		uniq = append(uniq, p)
	}
	points = uniq
	// Non-empty text before the first boundary (a preface, ads, etc. where the model missed the starting boundary) does not trigger a final veto: Go deterministically
	// adds a front_matter to cover [0, first) and records Notes for a human to check in the confirmation preview -- the miss is already in the chunk cache, and a
	// final veto would make a rerun reproduce the same failure with zero calls (the same philosophy as absorbing empty-body chapters, RFC §8.3.5).
	// The semantic judgement itself was already handed back to the model at call time (chunkValidator.coverStart re-asks); this fallback only heals old cache.
	if head := points[0].byte; head != 0 && strings.TrimSpace(string(normalized[:head])) != "" {
		notes = append(notes, fmt.Sprintf("起始 %d 字节文本未被模型归属（%s…），已收为 front_matter，请核对是否漏切章节",
			head, snippet(string(normalized[:min(head, 48)]), 24)))
		points = append([]point{{byte: 0, d: BoundaryDecision{UnitID: units[0].ID, Kind: kindFrontMatter}}}, points...)
	}

	seg := &Segmentation{Notes: notes}
	chapterNo := 0
	// absorb merges a span into the most recently produced span (a chapter or an ancillary region, either is fine), returning false when there is nothing to merge into.
	absorb := func(end int) bool {
		ci, mi := len(seg.Chapters)-1, len(seg.Matter)-1
		switch {
		case ci >= 0 && (mi < 0 || seg.Chapters[ci].Start > seg.Matter[mi].Start):
			seg.Chapters[ci].End = end
		case mi >= 0:
			seg.Matter[mi].End = end
		default:
			return false
		}
		return true
	}
	for i, p := range points {
		start := p.byte
		if i == 0 {
			start = 0 // the first segment absorbs the leading whitespace
		}
		end := len(normalized)
		if i+1 < len(points) {
			end = points[i+1].byte
		}
		title := strings.TrimSpace(p.d.Title)
		if title == "" {
			title = firstLine(normalized, p.byte, end)
		}
		switch p.d.Kind {
		case kindChapter:
			if strings.TrimSpace(bodyAfterTitle(normalized, p.byte, end)) == "" {
				// Real web-novel sources often contain "locked/paid chapter" placeholders: the title is there, the body is missing. Do not fail outright --
				// a final one-vote veto would waste every model call of the segmentation stage; the title line is merged into the previous segment (no text is lost),
				// recorded in Notes and surfaced by the confirmation preview, and a human who disagrees can rule via --guide (the RFC §8.4 stop point exists for exactly this).
				seg.Notes = append(seg.Notes,
					fmt.Sprintf("章节标题 %q 无正文（byte %d..%d），已并入前段（常见于锁定/付费占位章节）", title, start, end))
				if !absorb(end) {
					seg.Matter = append(seg.Matter, MatterSpan{Kind: kindFrontMatter, Title: title, Start: start, End: end})
				}
				continue
			}
			chapterNo++
			seg.Chapters = append(seg.Chapters, ChapterSpan{Number: chapterNo, Title: title, Start: start, End: end})
			if p.d.Uncertain {
				seg.Uncertain = append(seg.Uncertain, chapterNo)
			}
		default:
			seg.Matter = append(seg.Matter, MatterSpan{Kind: p.d.Kind, Title: title, Start: start, End: end})
		}
	}
	if chapterNo == 0 {
		return nil, fmt.Errorf("切分未产出任何章节（group 不计入章节）")
	}
	// Same-named chapters are a deterministic signal of "one chapter was mis-split" (in a source with a title convention, chapter names must not repeat), so only
	// Notes is recorded for a human to check in the confirmation preview (a non-empty Notes blocks --yes) -- whether to merge is not for Go to decide.
	titleAt := make(map[string]int, len(seg.Chapters))
	for _, c := range seg.Chapters {
		key := squashSpace(c.Title)
		if first, ok := titleAt[key]; ok && key != "" {
			seg.Notes = append(seg.Notes, fmt.Sprintf("第 %d 章与第 %d 章标题相同（%q），疑似同章被误切，请核对",
				c.Number, first, snippet(c.Title, 24)))
		} else {
			titleAt[key] = c.Number
		}
	}
	return seg, nil
}

// squashSpace removes all whitespace, for echoing titles and comparing same names -- whitespace/decorative differences are not semantic differences.
func squashSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// firstLine returns the text of the first line in [start,end) with whitespace stripped.
func firstLine(normalized []byte, start, end int) string {
	s := string(normalized[start:end])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// bodyAfterTitle returns the body of [start,end) with the first line (the title) removed.
// A multi-line chapter title occupies the first line alone and the body follows it; a single-line segment with no line break (the anchor segmentation case) is all body,
// so return the whole segment instead of an empty string -- otherwise a legal single-line / single-line-multi-chapter novel is misjudged as "empty body" and rejected (RFC §8.3).
func bodyAfterTitle(normalized []byte, start, end int) string {
	s := string(normalized[start:end])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// planChunks cuts the units into non-overlapping, fully covering owned index ranges [start,end) according to a byte budget.
// The chunk size is computed from the context budget, not from a fixed line or chapter count (RFC §8.1).
func planChunks(units []SourceUnit, budgetBytes int) [][2]int {
	if len(units) == 0 {
		return nil
	}
	if budgetBytes <= 0 {
		return [][2]int{{0, len(units)}}
	}
	var chunks [][2]int
	start := 0
	acc := 0
	for i, u := range units {
		size := u.EndByte - u.StartByte
		if acc > 0 && acc+size > budgetBytes {
			chunks = append(chunks, [2]int{start, i})
			start = i
			acc = 0
		}
		acc += size
	}
	chunks = append(chunks, [2]int{start, len(units)})
	return chunks
}

// buildProjection assembles the structured projection payload of one owned range (with a little context); the model returns boundaries only for the owned part.
// It also returns the full set of unit_ids in the projection (owned + the context area), so output validation can tell hallucination from out-of-bounds.
func buildProjection(units []SourceUnit, owned [2]int, contextMargin, ctxBudget int, guidance string) (string, map[string]bool) {
	// The context area shrinks under dual caps of unit count and bytes (with ctxBudget<=0 only the unit count applies): margin units are usually
	// ordinary lines, but the virtual splits of an over-long line can reach MaxUnitBytes, and a handful of those can swallow the entire input budget -- the context
	// is only reference material and is not worth that price.
	lo, budget := owned[0], ctxBudget
	for lo > 0 && owned[0]-lo < contextMargin {
		if n := len(units[lo-1].Text); ctxBudget > 0 {
			if n > budget {
				break
			}
			budget -= n
		}
		lo--
	}
	hi, budget := owned[1], ctxBudget
	for hi < len(units) && hi-owned[1] < contextMargin {
		if n := len(units[hi].Text); ctxBudget > 0 {
			if n > budget {
				break
			}
			budget -= n
		}
		hi++
	}
	type projUnit struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	proj := struct {
		OwnedStart   string     `json:"owned_start"`
		OwnedEnd     string     `json:"owned_end"`
		Units        []projUnit `json:"units"`
		UserGuidance string     `json:"user_guidance,omitempty"`
	}{
		OwnedStart:   units[owned[0]].ID,
		OwnedEnd:     units[owned[1]-1].ID,
		UserGuidance: guidance,
	}
	ids := make(map[string]bool, hi-lo)
	for i := lo; i < hi; i++ {
		proj.Units = append(proj.Units, projUnit{ID: units[i].ID, Text: units[i].Text})
		ids[units[i].ID] = true
	}
	data, _ := json.MarshalIndent(proj, "", "  ")
	return string(data), ids
}

// segmentInputDigest covers the semantic inputs the segmentation action actually consumes: the normalized source, user guidance and prompt version (RFC §6.3).
func segmentInputDigest(normalizedDigest, guidance, promptVersion string) string {
	return Digest([]byte(strings.Join([]string{"segment", promptVersion, normalizedDigest, guidance}, "\x00")))
}

// segmentChunkPath / segmentChunkDigest: the artifact path and identity of the chunk-level boundary cache.
// The identity binds the segmentation identity (source + guidance + prompt version) to the chunk's owned unit range -- any upstream change naturally invalidates the cache.
func segmentChunkPath(owned [2]int) string {
	return fmt.Sprintf("%s/chunk-%06d-%06d.json", dirSegmentChunks, owned[0], owned[1])
}

func segmentChunkDigest(identity, loID, hiID string) string {
	return Digest([]byte(strings.Join([]string{"segment-chunk", identity, loID, hiID}, "\x00")))
}

// Segment performs semantic segmentation over the whole normalized text: it calls the model per owned range to identify boundaries, then validates whole-text coverage.
// contextMargin is the number of context units, chunkBytes the byte budget of an owned range, maxTokens the per-call output budget.
// When w is non-nil the boundary cache is persisted chunk by chunk (identity = segmentInputDigest): one chunk can take minutes, and a failure of any single chunk
// must not re-pay the calls of the already completed chunks -- the same philosophy as analyze per chapter and synthesize per range, since before this
// segmentation was the only expensive stage with no intra-stage persistence, so one failure threw away everything.
func Segment(ctx context.Context, m callModel, systemPrompt string, normalized []byte, units []SourceUnit, guidance string, chunkBytes, contextMargin, maxTokens int, prof callProfile, w *Workspace, identity string) (*Segmentation, error) {
	chunks := planChunks(units, planningBudget(chunkBytes, systemPrompt, guidance))
	unitByID := make(map[string]SourceUnit, len(units))
	for _, u := range units {
		unitByID[u.ID] = u
	}
	var decisions []BoundaryDecision
	// chunk processes one owned range: a cache hit costs zero calls; when the output is truncated on length and the range can be split again, it halves the chunk
	// and retries recursively (the boundary JSON of many short chapters can exceed the visible output, the same philosophy as analyze shrinking a batch) -- the half-chunk has
	// its own cache path, so the retry's work is not paid for twice; only a truncation at unit level is genuine insufficient capacity.
	var chunk func(owned [2]int, cur, total int) ([]BoundaryDecision, error)
	chunk = func(owned [2]int, cur, total int) ([]BoundaryDecision, error) {
		lo, hi := units[owned[0]], units[owned[1]-1]
		rel, want := segmentChunkPath(owned), segmentChunkDigest(identity, lo.ID, hi.ID)
		if w != nil {
			if art, err := readArtifact[boundaryBatch](w, rel); err == nil && art.InputDigest == want {
				return art.Payload.Boundaries, nil
			}
		}
		// A single chunk's model call can take minutes, so echoing progress per chunk + the cumulative boundary count is what stops the panel from going
		prof.step(cur, total, "切分第 %d/%d 块（%s..%s），已识别 %d 个边界...",
			cur, total, lo.ID, hi.ID, len(decisions))
		// The context area's byte cap is chunkBytes/8 but no less than 4096: what it must stop is the virtual splits of over-long lines (a single split can reach
		// MaxUnitBytes) swallowing the input budget; the margin overhead of ordinary lines is harmless anyway.
		payload, projIDs := buildProjection(units, owned, contextMargin, max(chunkBytes/8, 4096), guidance)
		ownedIDs := make(map[string]bool, owned[1]-owned[0])
		for i := owned[0]; i < owned[1]; i++ {
			ownedIDs[units[i].ID] = true
		}
		v := chunkValidator{projIDs: projIDs, ownedIDs: ownedIDs, unitByID: unitByID,
			normalized: normalized, coverStart: owned[0] == 0}
		batch, err := callStructured[boundaryBatch](ctx, m, segmentContract, systemPrompt, payload, maxTokens, prof, func(b *boundaryBatch) error {
			return v.validate(b.Boundaries)
		})
		if err != nil {
			var tr *errTruncated
			if errors.As(err, &tr) && owned[1]-owned[0] > 1 {
				mid := (owned[0] + owned[1]) / 2
				prof.step(0, 0, "块 %s..%s 边界输出被截断（章节过密），对半缩块重试", lo.ID, hi.ID)
				prof.logger().Warn("imp 切分输出截断，对半缩块", "chunk", lo.ID+".."+hi.ID)
				left, lerr := chunk([2]int{owned[0], mid}, cur, total)
				if lerr != nil {
					return nil, lerr
				}
				right, rerr := chunk([2]int{mid, owned[1]}, cur, total)
				if rerr != nil {
					return nil, rerr
				}
				return append(left, right...), nil
			}
			return nil, fmt.Errorf("切分区间 %s..%s：%w", lo.ID, hi.ID, err)
		}
		// Boundaries in the context area are governed by the adjacent chunk (which will report it again in its own owned range), so Go simply clips them:
		// coordinate discipline is enforced by code, and semantic retries are reserved for genuine semantic failures -- the old behaviour re-asked on out-of-bounds feedback,
		// and weak models routinely burn all 3 attempts and drag the whole chunk down (RFC §8.1 "the model handles semantics, Go handles coordinates").
		kept := make([]BoundaryDecision, 0, len(batch.Boundaries))
		for _, bd := range batch.Boundaries {
			if ownedIDs[bd.UnitID] {
				kept = append(kept, bd)
			}
		}
		if n := len(batch.Boundaries) - len(kept); n > 0 {
			// This is routine coordinate discipline, not an anomaly, so it is echoed as ordinary progress -- a warning colour would make the user think something went wrong.
			prof.step(0, 0, "已裁掉 %d 个上下文区多报的边界（归相邻块自行报告，非错误）", n)
		}
		// Echo the model's semantic judgement (the titles it identified), so the user sees what the model understood rather than only a mechanical count.
		if len(kept) > 0 {
			prof.step(0, 0, "模型识别出：%s", previewBoundaries(kept))
		}
		if w != nil {
			if err := writeArtifact(w, rel, want, boundaryBatch{Boundaries: kept}); err != nil {
				return nil, fmt.Errorf("落盘切分块 %s..%s：%w", lo.ID, hi.ID, err)
			}
		}
		return kept, nil
	}
	for ci, owned := range chunks {
		kept, err := chunk(owned, ci+1, len(chunks))
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, kept...)
	}
	seg, err := resolveSegmentation(normalized, units, decisions)
	if err != nil {
		// When the final integration fails the chunk cache is already worthless: the digest always matches, so a rerun would reread the same batch of boundaries with zero
		// calls and deterministically reproduce the same failure. Clearing the cache buys a fresh chance for the model on the next segmentation run; the decision snapshot
		// goes to failures/ through errSemantic for after-the-fact troubleshooting. A failed clear must be reported truthfully -- claiming it was cleared makes the user
		// reread the bad cache again on the next run (Debug-First).
		hint := "块缓存已清除，重跑将重新切分"
		if w != nil {
			if cerr := w.clearDir(dirSegmentChunks); cerr != nil {
				hint = fmt.Sprintf("块缓存清除失败：%v，重跑前请手动删除 meta/import/segment-chunks/", cerr)
			}
		}
		raw, _ := json.MarshalIndent(decisions, "", "  ")
		return nil, &errSemantic{Raw: string(raw), Err: fmt.Errorf("整合全书切分失败（%s）：%w", hint, err)}
	}
	return seg, nil
}

// planningBudget deducts the request's structural overhead from the input budget: the system prompt and guidance are deducted by their actual length, and the remainder is
// scaled by 3/4 to account for the inflation of the projected JSON wrapper (ids/quotes/escaping is about 1/3 of the body) -- the owned body is only part of the request,
// so planning at full value would overshoot the real input budget with a long prompt or a large context area. The floor chunkBytes/4 stops an over-long prompt from
// squeezing the budget negative; chunkBytes<=0 means no budget (a single chunk) and is passed through unchanged.
func planningBudget(chunkBytes int, systemPrompt, guidance string) int {
	if chunkBytes <= 0 {
		return chunkBytes
	}
	b := (chunkBytes - len(systemPrompt) - len(guidance)) * 3 / 4
	return max(b, chunkBytes/4)
}

// boundaryLabel gives a boundary decision a readable label: the title is preferred, and without one it falls back to kind@unit_id.
func boundaryLabel(d BoundaryDecision) string {
	if t := strings.TrimSpace(d.Title); t != "" {
		return t
	}
	return d.Kind + "@" + d.UnitID
}

// previewBoundaries squeezes a batch of boundary decisions into a one-line title preview (at most 3 plus a count), for echoing in the panel.
func previewBoundaries(bs []BoundaryDecision) string {
	titles := make([]string, 0, 3)
	for _, b := range bs {
		titles = append(titles, snippet(boundaryLabel(b), 24))
		if len(titles) == 3 {
			break
		}
	}
	s := strings.Join(titles, " / ")
	if len(bs) > len(titles) {
		s += fmt.Sprintf("（共 %d 处）", len(bs))
	}
	return s
}

// chunkValidator carries the call-time validation context of one segmentation call: a unit_id outside the projection is a hallucination; a boundary in the owned
// area must additionally have a legal kind, a resolvable anchor and no semantic conflict at the same position; the first chunk needs a boundary covering the start of the text.
// Failing to block these bad values at call time lets them land in the cache chunk by chunk -- the digest always matches, so a rerun rereads the same bad data
// with zero calls and reproduces the failure deterministically (RFC §8.3). Semantic judgements (which one to keep, what the opening is) are handed back to the
// model through re-asking; Go does not answer on its behalf. Context-area boundaries are bound to be clipped by coordinate discipline, so nothing is re-asked for them.
type chunkValidator struct {
	projIDs, ownedIDs map[string]bool
	unitByID          map[string]SourceUnit
	normalized        []byte
	coverStart        bool // first chunk: the non-empty text before the start of the text must be covered by a boundary
}

func (v chunkValidator) validate(bs []BoundaryDecision) error {
	seen := make(map[int]BoundaryDecision)
	first := -1
	for _, b := range bs {
		if b.UnitID == "" {
			return fmt.Errorf("边界缺 unit_id")
		}
		if !v.projIDs[b.UnitID] {
			return fmt.Errorf("边界 unit_id %q 不存在于本次投影中", b.UnitID)
		}
		if !v.ownedIDs[b.UnitID] {
			continue
		}
		switch b.Kind {
		case kindChapter, kindGroup, kindFrontMatter, kindBackMatter:
		default:
			return fmt.Errorf("边界 %s kind 非法：%q（只能是 chapter/group/front_matter/back_matter）", b.UnitID, b.Kind)
		}
		at, err := resolveBoundaryByte(v.unitByID, b.UnitID, b.Anchor)
		if err != nil {
			return err
		}
		// Title echo: the title of a chapter/group must genuinely exist in the boundary unit's original text (ignoring whitespace differences) --
		// an invented title is stopped here by fact (measured: in one source, 67 of 157 chapters had a boundary the model invented mid-chapter plus a
		// fabricated title). Semantic discretion still belongs to the model: a source genuinely without a title convention may set uncertain and keep the summarising title;
		// the descriptive titles of front/back matter are low risk and are not verified.
		if (b.Kind == kindChapter || b.Kind == kindGroup) && !b.Uncertain {
			if t := squashSpace(b.Title); t != "" && !strings.Contains(squashSpace(v.unitByID[b.UnitID].Text), t) {
				return fmt.Errorf("边界 %s 的标题 %q 在该单元原文中找不到：若这里是上一章的延续正文，请不要为它设边界（由前文边界归属，boundaries 可为空）；若源文此处确实没有标题行、标题是你归纳的，请置 uncertain=true",
					b.UnitID, snippet(b.Title, 24))
			}
		}
		// A conflict at the same position (different kind/title) is a semantic issue and which one to keep is not for Go to decide; an exactly identical
		// duplicate is mechanical redundancy and is silently deduped by resolve after being let through.
		if prev, ok := seen[at]; ok {
			if prev.Kind != b.Kind || boundaryLabel(prev) != boundaryLabel(b) {
				return fmt.Errorf("边界 %q 与 %q 落在同一位置（%s），语义冲突，请只保留正确的一个",
					boundaryLabel(prev), boundaryLabel(b), b.UnitID)
			}
		} else {
			seen[at] = b
		}
		if first < 0 || at < first {
			first = at
		}
	}
	if v.coverStart {
		head := first
		if head < 0 {
			head = len(v.normalized) // the first chunk reported no owned boundary at all: none of the leading text is covered
		}
		if head > 0 && strings.TrimSpace(string(v.normalized[:head])) != "" {
			return fmt.Errorf("起始 %d 字节文本（%s…）未归属任何边界，请为文本开头补充边界（front_matter/chapter/group）",
				head, snippet(string(v.normalized[:min(head, 48)]), 24))
		}
	}
	return nil
}
