package imp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
)

func TestUnitLessNumericNotLexical(t *testing.T) {
	// Lexicographic order would judge L900 > L1000 and L1257.2 > L1800; numeric order must be the opposite.
	if !unitLess(SourceUnit{Line: 900}, SourceUnit{Line: 1000}) {
		t.Fatal("L900 应 < L1000（数值序）")
	}
	if !unitLess(SourceUnit{Line: 1257, Part: 2}, SourceUnit{Line: 1800}) {
		t.Fatal("L1257.2 应 < L1800")
	}
	if !unitLess(SourceUnit{Line: 1257, Part: 1}, SourceUnit{Line: 1257, Part: 2}) {
		t.Fatal("同行 part 应按数值序")
	}
	if unitLess(SourceUnit{Line: 5}, SourceUnit{Line: 5}) {
		t.Fatal("相等不应 less")
	}
}

func TestBuildSourceUnitsRoundtrip(t *testing.T) {
	norm := []byte("第一章\n正文一\n\n第二章\n正文二")
	units := buildSourceUnits(norm, 0)
	// Stitched back together: each unit's text + '\n' between lines should reproduce the normalized text.
	var b strings.Builder
	for i, u := range units {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(u.Text)
		if u.Text != string(norm[u.StartByte:u.EndByte]) {
			t.Fatalf("unit %s 字节范围与文本不符", u.ID)
		}
	}
	if b.String() != string(norm) {
		t.Fatalf("拼回不符：%q", b.String())
	}
	if units[0].ID != "L1" || units[3].ID != "L4" {
		t.Fatalf("ID 不符：%s %s", units[0].ID, units[3].ID)
	}
}

func TestBuildSourceUnitsVirtualShard(t *testing.T) {
	// A whole line far exceeds the budget -> split into several virtual units, with the boundaries on UTF-8 character boundaries.
	long := strings.Repeat("字", 100) // 每字 3 字节 = 300 字节
	units := buildSourceUnits([]byte(long), 30)
	if len(units) < 2 {
		t.Fatalf("超预算行应分片，得到 %d", len(units))
	}
	var b strings.Builder
	for _, u := range units {
		if u.Line != 1 || u.Part == 0 {
			t.Fatalf("虚拟分片应同 Line、Part>=1：%+v", u)
		}
		b.WriteString(u.Text) // 分片同一行，无换行分隔
	}
	if b.String() != long {
		t.Fatal("虚拟分片拼回丢字")
	}
}

func TestResolveBoundaryByteAnchor(t *testing.T) {
	units := []SourceUnit{{ID: "L1", Line: 1, StartByte: 0, EndByte: 10, Text: "楔子风起楔"}}
	m := map[string]SourceUnit{"L1": units[0]}
	if _, err := resolveBoundaryByte(m, "L1", "风起"); err != nil {
		t.Fatalf("唯一锚点应成功：%v", err)
	}
	if _, err := resolveBoundaryByte(m, "L1", "楔"); err == nil {
		t.Fatal("重复锚点应失败")
	}
	if _, err := resolveBoundaryByte(m, "L1", "缺失"); err == nil {
		t.Fatal("不存在锚点应失败")
	}
	if _, err := resolveBoundaryByte(m, "L9", ""); err == nil {
		t.Fatal("不存在 unit 应失败")
	}
}

func TestPlanChunksCoversWithoutGap(t *testing.T) {
	units := buildSourceUnits([]byte(strings.Repeat("行内容\n", 50)), 0)
	chunks := planChunks(units, 40)
	if len(chunks) < 2 {
		t.Fatalf("应分多块，得 %d", len(chunks))
	}
	// Seamless, non-overlapping and fully covering.
	if chunks[0][0] != 0 || chunks[len(chunks)-1][1] != len(units) {
		t.Fatal("未完整覆盖")
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i][0] != chunks[i-1][1] {
			t.Fatalf("块 %d 与前块不相接：%v", i, chunks)
		}
	}
}

func segFixture() ([]byte, []SourceUnit) {
	norm := []byte("前言\n感谢阅读\n第一章 风起\n正文一\n卷二\n第二章 云涌\n正文二")
	return norm, buildSourceUnits(norm, 0)
}

func TestResolveSegmentationHappy(t *testing.T) {
	norm, units := segFixture()
	// L1 preface(front) / L3 chapter one / L5 volume two(group) / L6 chapter two
	decisions := []BoundaryDecision{
		{UnitID: "L1", Kind: kindFrontMatter, Title: "前言"},
		{UnitID: "L3", Kind: kindChapter, Title: "第一章 风起"},
		{UnitID: "L5", Kind: kindGroup, Title: "卷二"},
		{UnitID: "L6", Kind: kindChapter, Title: "第二章 云涌"},
	}
	seg, err := resolveSegmentation(norm, units, decisions)
	if err != nil {
		t.Fatalf("覆盖校验应通过：%v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("章节数应为 2（group 不计），得 %d", len(seg.Chapters))
	}
	if seg.Chapters[0].Number != 1 || seg.Chapters[1].Number != 2 {
		t.Fatal("章节号应连续")
	}
	if !strings.Contains(seg.Content(norm, 0), "正文一") {
		t.Fatalf("章一正文不符：%q", seg.Content(norm, 0))
	}
	// Coverage: the first segment (front_matter) starts at 0 and the last chapter covers through the end of the text.
	if len(seg.Matter) == 0 || seg.Matter[0].Kind != kindFrontMatter || seg.Matter[0].Start != 0 {
		t.Fatalf("首段应为从 0 起的 front_matter：%+v", seg.Matter)
	}
	if seg.Chapters[len(seg.Chapters)-1].End != len(norm) {
		t.Fatal("末章应覆盖到文本尾")
	}
}

func TestResolveSegmentationRejections(t *testing.T) {
	norm, units := segFixture()
	cases := []struct {
		name string
		ds   []BoundaryDecision
	}{
		{"无章节", []BoundaryDecision{
			{UnitID: "L1", Kind: kindFrontMatter},
		}},
		{"非法kind", []BoundaryDecision{
			{UnitID: "L1", Kind: "verse"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := resolveSegmentation(norm, units, c.ds); err == nil {
				t.Fatalf("应被拒绝：%s", c.name)
			}
		})
	}
}

// TestResolveSegmentationReordersAndDedups guards the coordinate discipline of the final fallback: the model's occasional in-chunk
// out-of-order output is restored deterministically by a byte sort (measured: 319 boundaries once lost to a single inversion, and the chunk cache makes the failure reproduce deterministically);
// an exact-byte duplicate keeps the one that appeared first and is recorded in Notes for the confirmation preview.
func TestResolveSegmentationReordersAndDedups(t *testing.T) {
	norm, units := segFixture()
	seg, err := resolveSegmentation(norm, units, []BoundaryDecision{
		{UnitID: "L3", Kind: kindChapter, Title: "第一章 风起"},
		{UnitID: "L1", Kind: kindChapter, Title: "开篇"}, // 乱序：位置在 L3 之前
		{UnitID: "L6", Kind: kindChapter, Title: "第二章 云涌"},
		{UnitID: "L6", Kind: kindChapter, Title: "第二章 重复"}, // 同字节重复
	})
	if err != nil {
		t.Fatalf("乱序/重复应被确定性修复而非拒绝：%v", err)
	}
	if len(seg.Chapters) != 3 {
		t.Fatalf("应得 3 章，得 %d：%+v", len(seg.Chapters), seg.Chapters)
	}
	if seg.Chapters[0].Title != "开篇" || seg.Chapters[0].Start != 0 {
		t.Fatalf("排序后首章应为位置最前的边界：%+v", seg.Chapters[0])
	}
	if seg.Chapters[2].Title != "第二章 云涌" {
		t.Fatalf("同字节重复应保留先出现者：%+v", seg.Chapters[2])
	}
	if len(seg.Notes) != 1 || !strings.Contains(seg.Notes[0], "重合") {
		t.Fatalf("重复边界应记入 Notes：%v", seg.Notes)
	}
}

// TestResolveSegmentationAbsorbsLeadingText guards the deterministic repair of a missed start: non-empty leading text such as a preface or ads
// whose boundary the model missed must not trigger a final veto -- the miss is already in the chunk cache, and a veto would make a rerun reproduce the failure
// deterministically with zero calls. Go adds a front_matter to cover [0, first) and records Notes for the confirmation preview.
func TestResolveSegmentationAbsorbsLeadingText(t *testing.T) {
	norm, units := segFixture()
	// Only chapters from L3 onward were reported: the non-empty L1/L2 text has no owner.
	seg, err := resolveSegmentation(norm, units, []BoundaryDecision{
		{UnitID: "L3", Kind: kindChapter, Title: "第一章 风起"},
		{UnitID: "L6", Kind: kindChapter, Title: "第二章 云涌"},
	})
	if err != nil {
		t.Fatalf("起始未归属文本应被收为 front_matter 而非拒绝：%v", err)
	}
	if len(seg.Matter) != 1 || seg.Matter[0].Kind != kindFrontMatter || seg.Matter[0].Start != 0 {
		t.Fatalf("应补出从 0 起的 front_matter：%+v", seg.Matter)
	}
	if len(seg.Chapters) != 2 || seg.Chapters[0].Start == 0 {
		t.Fatalf("章节不应吞掉头部文本：%+v", seg.Chapters)
	}
	if len(seg.Notes) != 1 || !strings.Contains(seg.Notes[0], "未被模型归属") {
		t.Fatalf("应记录人工核对说明：%v", seg.Notes)
	}
}

// TestResolveSegmentationNotesDuplicateTitles guards the visibility of same-named chapters: in a source with a title convention chapter names
// must not repeat, and a repeat is a deterministic signal of "one chapter was mis-split" -- it is only recorded in Notes (blocking --yes, shown in
// the preview) for a human to check, and whether to merge is not for Go to decide.
func TestResolveSegmentationNotesDuplicateTitles(t *testing.T) {
	norm, units := segFixture()
	seg, err := resolveSegmentation(norm, units, []BoundaryDecision{
		{UnitID: "L1", Kind: kindFrontMatter, Title: "前言"},
		{UnitID: "L3", Kind: kindChapter, Title: "第一章 风起"},
		{UnitID: "L6", Kind: kindChapter, Title: "第一章风起"}, // 同名（空白差异忽略）
	})
	if err != nil {
		t.Fatalf("同名章应放行并记 Notes：%v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("应得 2 章，得 %d", len(seg.Chapters))
	}
	if len(seg.Notes) != 1 || !strings.Contains(seg.Notes[0], "标题相同") {
		t.Fatalf("应记一条同名核对说明：%v", seg.Notes)
	}
}

// TestChunkValidatorOwnedDiscipline guards the coverage of the call-time validation: an illegal kind inside the owned area, a
// bad anchor, a semantic conflict at the same position and an uncovered start in the first chunk must all be re-asked with feedback at call time -- letting them through
// puts them in the cache chunk by chunk, and when the final resolve notices, a rerun rereads the same bad data with zero calls; context-area boundaries are bound to be clipped,
// so nothing is re-asked for them; an exactly identical duplicate at the same position is mechanical redundancy and is silently deduped by resolve after being let through.
func TestChunkValidatorOwnedDiscipline(t *testing.T) {
	norm, units := segFixture()
	unitByID := map[string]SourceUnit{}
	proj, owned := map[string]bool{}, map[string]bool{}
	for _, u := range units {
		unitByID[u.ID] = u
		proj[u.ID] = true
	}
	owned["L1"], owned["L2"], owned["L3"] = true, true, true
	v := chunkValidator{projIDs: proj, ownedIDs: owned, unitByID: unitByID, normalized: norm}

	cases := []struct {
		name    string
		bs      []BoundaryDecision
		wantErr bool
	}{
		{"owned 非法 kind", []BoundaryDecision{{UnitID: "L1", Kind: "volume"}}, true},
		{"owned 坏 anchor", []BoundaryDecision{{UnitID: "L3", Kind: kindChapter, Anchor: "不存在的锚"}}, true},
		{"owned 合法 anchor", []BoundaryDecision{{UnitID: "L3", Kind: kindChapter, Anchor: "第一章"}}, false},
		{"上下文区非法 kind 不重问", []BoundaryDecision{{UnitID: "L6", Kind: "volume"}}, false},
		{"投影外幻觉 ID", []BoundaryDecision{{UnitID: "L99", Kind: kindChapter}}, true},
		{"同位语义冲突重问", []BoundaryDecision{
			{UnitID: "L1", Kind: kindChapter, Title: "前言"},
			{UnitID: "L1", Kind: kindFrontMatter, Title: "前言"},
		}, true},
		{"同位完全重复放行", []BoundaryDecision{
			{UnitID: "L1", Kind: kindChapter, Title: "前言"},
			{UnitID: "L1", Kind: kindChapter, Title: "前言"},
		}, false},
		// Title echo: the chapter/volume name must genuinely exist in the boundary unit's original text -- a fabricated title on a phantom boundary is stopped here.
		{"编造章节标题重问", []BoundaryDecision{{UnitID: "L2", Kind: kindChapter, Title: "第某章 我编的"}}, true},
		{"归纳标题须 uncertain 放行", []BoundaryDecision{{UnitID: "L2", Kind: kindChapter, Title: "第某章 我编的", Uncertain: true}}, false},
		{"回显容忍空白差异", []BoundaryDecision{{UnitID: "L3", Kind: kindChapter, Title: "第一章风起"}}, false},
		{"编造卷名重问", []BoundaryDecision{{UnitID: "L2", Kind: kindGroup, Title: "卷九"}}, true},
		{"附属描述性标题不核对", []BoundaryDecision{{UnitID: "L2", Kind: kindFrontMatter, Title: "引言"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := v.validate(c.bs); (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v，得 %v", c.wantErr, err)
			}
		})
	}

	// First-chunk start coverage: L1/L2 non-empty with no owning boundary -> re-ask; after adding a start boundary it passes.
	vs := v
	vs.coverStart = true
	if err := vs.validate([]BoundaryDecision{{UnitID: "L3", Kind: kindChapter}}); err == nil {
		t.Fatal("首块起始未归属应重问")
	}
	if err := vs.validate([]BoundaryDecision{
		{UnitID: "L1", Kind: kindFrontMatter}, {UnitID: "L3", Kind: kindChapter},
	}); err != nil {
		t.Fatalf("起点已覆盖应通过：%v", err)
	}
	if err := vs.validate(nil); err == nil {
		t.Fatal("首块零边界应重问（全部起始文本未归属）")
	}
}

// TestSegmentClearsChunksOnResolveFailure guards the master switch against "the cache reproduces deterministically": when the final
// integration fails the chunk cache is already worthless (the digest always matches, so a rerun rereads the same batch of boundaries and dies again), so it must be cleared
// to buy a fresh chance for the model on the next segmentation run; the decision snapshot goes to failures/ through errSemantic.
func TestSegmentClearsChunksOnResolveFailure(t *testing.T) {
	norm, units := segFixture()
	// The model marks the whole book as front_matter: no chapters, Go cannot repair it deterministically, the final step fails.
	m := &mockModel{responses: []string{boundariesJSON(boundaryFixture("L1", "", kindFrontMatter, "前言"))}}
	w := &Workspace{dir: t.TempDir()}
	_, err := Segment(context.Background(), m, "sys", norm, units, "", 0, 0, 4096, callProfile{}, w, "id-1")
	if err == nil {
		t.Fatal("无章节应终局失败")
	}
	var se *errSemantic
	if !errors.As(err, &se) {
		t.Fatalf("终局失败应为 errSemantic（统一落 failures/），得 %T", err)
	}
	if _, statErr := os.Stat(filepath.Join(w.dir, dirSegmentChunks)); !os.IsNotExist(statErr) {
		t.Fatalf("终局失败后块缓存应被清除：%v", statErr)
	}
}

// mockModel returns preset responses in order, for typed-call contract tests.
// stops can specify the stop reason per call; it defaults to stop or StopReasonStop.
type mockModel struct {
	responses []string
	stops     []agentcore.StopReason
	i         int
	stop      agentcore.StopReason
}

func (m *mockModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	idx := m.i
	r := m.responses[idx%len(m.responses)]
	sr := m.stop
	if idx < len(m.stops) {
		sr = m.stops[idx]
	}
	if sr == "" {
		sr = agentcore.StopReasonStop
	}
	m.i++
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock(r)},
		StopReason: sr,
	}}, nil
}

// TestResolveSegmentationSingleLineChapters guards #9: a single-line segment with no line break (the anchor segmentation case) is all body, so a
// single-line / single-line-multi-chapter novel must not be misjudged as "empty body" and rejected.
func TestResolveSegmentationSingleLineChapters(t *testing.T) {
	normalized := []byte("第一章甲的故事第二章乙的故事") // 整篇一行，无换行
	units := buildSourceUnits(normalized, 0)
	decisions := []BoundaryDecision{
		{UnitID: "L1", Kind: kindChapter, Title: "第一章"},                // 无锚点 → byte 0
		{UnitID: "L1", Anchor: "第二章", Kind: kindChapter, Title: "第二章"}, // 行内锚点切出第二章
	}
	seg, err := resolveSegmentation(normalized, units, decisions)
	if err != nil {
		t.Fatalf("单行多章应被接受：%v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("应切出 2 章，得 %d", len(seg.Chapters))
	}
	if got := seg.Content(normalized, 0); got != "第一章甲的故事" {
		t.Fatalf("首章正文范围不对：%q", got)
	}
}

func TestSegmentWithMockModel(t *testing.T) {
	norm, units := segFixture()
	resp := boundariesJSON(
		boundaryFixture("L1", "", kindFrontMatter, "前言"),
		boundaryFixture("L3", "", kindChapter, "第一章 风起"),
		boundaryFixture("L5", "", kindGroup, "卷二"),
		boundaryFixture("L6", "", kindChapter, "第二章 云涌"),
	)
	m := &mockModel{responses: []string{resp}}
	seg, err := Segment(context.Background(), m, "sys", norm, units, "", 0, 0, 4096, callProfile{}, nil, "")
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("应得 2 章，得 %d", len(seg.Chapters))
	}
}

// TestResolveSegmentationAbsorbsEmptyChapter guards dirty-source tolerance: real web-novel sources often contain "locked/paid chapter"
// placeholder titles (the title is there, the body is missing). Such boundaries must not fail outright -- a final one-vote veto would waste every model call of
// the segmentation stage; the placeholder segment is merged into the previous one (no text is lost) and recorded in Notes for the confirmation preview to present for human review.
func TestResolveSegmentationAbsorbsEmptyChapter(t *testing.T) {
	norm, units := segFixture()
	// The model marks the L5 "volume two" line as a chapter title: its span [L5,L6) has no body -> merge it into chapter one.
	decisions := []BoundaryDecision{
		{UnitID: "L1", Kind: kindFrontMatter, Title: "前言"},
		{UnitID: "L3", Kind: kindChapter, Title: "第一章 风起"},
		{UnitID: "L5", Kind: kindChapter, Title: "第五章 [本章节已锁定]"},
		{UnitID: "L6", Kind: kindChapter, Title: "第二章 云涌"},
	}
	seg, err := resolveSegmentation(norm, units, decisions)
	if err != nil {
		t.Fatalf("空正文占位章应被吸收而非整体失败：%v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("应得 2 章（占位并入前段），得 %d", len(seg.Chapters))
	}
	if got := seg.Content(norm, 0); !strings.Contains(got, "卷二") {
		t.Fatalf("占位段应并入第一章（文本不丢）：%q", got)
	}
	if len(seg.Notes) != 1 || !strings.Contains(seg.Notes[0], "已锁定") {
		t.Fatalf("应记录一条人工核对说明：%v", seg.Notes)
	}
	// The first point is itself an empty-body chapter: there is no previous segment to merge into -> it lands as front_matter, and again does not fail.
	seg, err = resolveSegmentation(norm, units, []BoundaryDecision{
		{UnitID: "L1", Kind: kindChapter, Title: "占位"}, // [L1,L2) 单行标题无正文
		{UnitID: "L2", Kind: kindChapter, Title: "第一章"},
	})
	if err != nil {
		t.Fatalf("首点空正文应落为 front_matter：%v", err)
	}
	if len(seg.Matter) != 1 || seg.Matter[0].Kind != kindFrontMatter {
		t.Fatalf("首点空正文应为 front_matter：%+v", seg.Matter)
	}
}

// TestSegmentClipsContextBoundaries guards the Go-side enforcement of coordinate discipline: a boundary the model returns in
// the context area does not trigger a semantic re-ask (weak models routinely burn all 3 attempts and drag the whole chunk down); the code clips it directly --
// that boundary is governed by the adjacent chunk, which will report it in its own owned range, and keeping it would cause cross-chunk duplication / out-of-order.
func TestSegmentClipsContextBoundaries(t *testing.T) {
	norm, units := segFixture()
	chunks := planChunks(units, planningBudget(40, "sys", "")) // 与 Segment 内部规划一致
	if len(chunks) < 2 {
		t.Fatalf("fixture 应分出至少 2 块，得 %d", len(chunks))
	}
	// Per-chunk response: one chapter boundary at the owned first unit (with no title it falls back to firstLine, sidestepping the title echo check --
	// what is being tested here is coordinate discipline); the first chunk additionally smuggles in a boundary for the next chunk's first unit (the context area).
	responses := make([]string, len(chunks))
	for ci, owned := range chunks {
		boundaries := []map[string]any{boundaryFixture(units[owned[0]].ID, "", kindChapter, "")}
		if ci == 0 {
			boundaries = append(boundaries, boundaryFixture(units[chunks[1][0]].ID, "", kindChapter, ""))
		}
		responses[ci] = boundariesJSON(boundaries...)
	}
	// The clipping notice is echoed as ordinary progress (routine coordinate discipline, not a warning -- a warn colour would make the user think something went wrong).
	var clipNotes int
	prof := callProfile{progress: func(_, _ int, s string) {
		if strings.Contains(s, "裁掉") {
			clipNotes++
		}
	}}
	seg, err := Segment(context.Background(), &mockModel{responses: responses}, "sys", norm, units, "", 40, 2, 4096, prof, nil, "")
	if err != nil {
		t.Fatalf("上下文区边界应被裁掉而非失败：%v", err)
	}
	if len(seg.Chapters) != len(chunks) {
		t.Fatalf("应得 %d 章（越界边界不重复计入），得 %d", len(chunks), len(seg.Chapters))
	}
	if clipNotes != 1 {
		t.Fatalf("应回显 1 条裁剪说明，得 %d", clipNotes)
	}
}

// TestSegmentReusesChunkArtifacts guards the chunk-level checkpoint: segmentation persists a boundary cache chunk by chunk, and on a rerun the chunks
// whose digest matches are reused with zero model calls -- segmentation is the most expensive stage, so a failure of any single chunk must not re-pay the completed ones (the same philosophy as analyze/synthesize).
func TestSegmentReusesChunkArtifacts(t *testing.T) {
	norm, units := segFixture()
	chunks := planChunks(units, planningBudget(40, "sys", "")) // 与 Segment 内部规划一致
	responses := make([]string, len(chunks))
	for ci, owned := range chunks {
		responses[ci] = boundariesJSON(boundaryFixture(units[owned[0]].ID, "", kindChapter, ""))
	}
	w := &Workspace{dir: t.TempDir()}
	m1 := &mockModel{responses: responses}
	seg1, err := Segment(context.Background(), m1, "sys", norm, units, "", 40, 2, 4096, callProfile{}, w, "id-1")
	if err != nil {
		t.Fatalf("首跑：%v", err)
	}
	if m1.i != len(chunks) {
		t.Fatalf("首跑应调用 %d 次，得 %d", len(chunks), m1.i)
	}
	m2 := &mockModel{responses: responses}
	seg2, err := Segment(context.Background(), m2, "sys", norm, units, "", 40, 2, 4096, callProfile{}, w, "id-1")
	if err != nil {
		t.Fatalf("重跑：%v", err)
	}
	if m2.i != 0 {
		t.Fatalf("digest 匹配的块应零调用复用，实际调用 %d 次", m2.i)
	}
	if len(seg2.Chapters) != len(seg1.Chapters) {
		t.Fatalf("复用结果应一致：%d != %d", len(seg2.Chapters), len(seg1.Chapters))
	}
	// Identity change (different prompt version / guidance / source) -> the cache naturally mismatches and everything is redone.
	m3 := &mockModel{responses: responses}
	if _, err := Segment(context.Background(), m3, "sys", norm, units, "", 40, 2, 4096, callProfile{}, w, "id-2"); err != nil {
		t.Fatalf("身份变化重跑：%v", err)
	}
	if m3.i != len(chunks) {
		t.Fatalf("身份变化应全部重做（%d 次调用），得 %d", len(chunks), m3.i)
	}
}

// TestSegmentShrinksChunkOnTruncation guards the output budget feedback loop: many short chapters make a chunk's boundary JSON
// exceed the visible output (stop=length), so it must halve the chunk and retry rather than failing outright -- the same philosophy as analyze shrinking a batch.
func TestSegmentShrinksChunkOnTruncation(t *testing.T) {
	norm, units := segFixture() // 7 个 unit，单块 [0,7)，mid=3
	left := boundariesJSON(boundaryFixture("L1", "", kindChapter, ""))
	right := boundariesJSON(boundaryFixture("L6", "", kindChapter, "第二章 云涌"))
	m := &mockModel{
		responses: []string{`{"boundaries":[]}`, left, right},
		stops:     []agentcore.StopReason{agentcore.StopReasonLength}, // 首调截断，两个半块正常
	}
	seg, err := Segment(context.Background(), m, "sys", norm, units, "", 0, 0, 4096, callProfile{}, nil, "")
	if err != nil {
		t.Fatalf("截断应缩块重试而非失败：%v", err)
	}
	if m.i != 3 {
		t.Fatalf("应为 1 次截断 + 2 次半块调用，得 %d", m.i)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("缩块结果应完整覆盖（2 章），得 %d", len(seg.Chapters))
	}
}

// TestPlanningBudget guards the structural overhead deduction from the segmentation planning budget: the owned body is only part of the request.
func TestPlanningBudget(t *testing.T) {
	if got := planningBudget(0, "sys", "g"); got != 0 {
		t.Fatalf("无预算应透传，得 %d", got)
	}
	if got := planningBudget(1000, strings.Repeat("s", 100), strings.Repeat("g", 100)); got != 600 {
		t.Fatalf("(1000-200)*3/4 应为 600，得 %d", got)
	}
	if got := planningBudget(1000, strings.Repeat("s", 2000), ""); got != 250 {
		t.Fatalf("超长提示应触发下限 chunkBytes/4=250，得 %d", got)
	}
}

// TestBuildProjectionContextByteCap guards the context area's byte cap: the virtual splits of over-long lines (a single split can reach
// MaxUnitBytes) swallow the input budget; the context is only reference material, so it shrinks under a byte cap instead of being taken wholesale.
func TestBuildProjectionContextByteCap(t *testing.T) {
	_, units := segFixture()
	if _, ids := buildProjection(units, [2]int{2, 3}, 2, 1, ""); len(ids) != 1 || !ids["L3"] {
		t.Fatalf("字节上限应裁掉上下文单元，只剩 owned：%v", ids)
	}
	if _, ids := buildProjection(units, [2]int{2, 3}, 2, 0, ""); len(ids) != 5 {
		t.Fatalf("无字节上限时应含前后各 2 个上下文单元（共 5），得 %v", ids)
	}
}

func TestCallStructuredTruncation(t *testing.T) {
	m := &mockModel{responses: []string{`{"boundaries":[]}`}, stop: agentcore.StopReasonLength}
	_, err := callStructured[boundaryBatch](context.Background(), m, segmentContract, "s", "p", 16, callProfile{}, nil)
	var trunc *errTruncated
	if err == nil || !asTruncated(err, &trunc) {
		t.Fatalf("长度截断应返回 *errTruncated，得 %v", err)
	}
}

func asTruncated(err error, target **errTruncated) bool {
	t, ok := err.(*errTruncated)
	if ok {
		*target = t
	}
	return ok
}
