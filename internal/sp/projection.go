package sp

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Hard character budget theo mode. Đếm bằng RUNE (utf8.RuneCountInString),
// không phải byte — snapshot tiếng Việt/Trung đếm byte sẽ phình gấp 2-3 lần.
// Không giả vờ đây là số token chính xác: tokenizer phụ thuộc model/ngôn ngữ.
const (
	BudgetAskChars     = 32 * 1024
	BudgetInspectChars = 28 * 1024
	BudgetSuggestChars = 20 * 1024
)

// BudgetForMode trả budget ký tự của mode. Mode lạ về budget nhỏ nhất để an toàn.
func BudgetForMode(mode Mode) int {
	switch mode {
	case ModeAsk:
		return BudgetAskChars
	case ModeInspect:
		return BudgetInspectChars
	case ModeSuggest:
		return BudgetSuggestChars
	default:
		return BudgetSuggestChars
	}
}

// Projection là kết quả project snapshot theo mode + budget.
// Blocks là COPIES (struct copy, strings immutable) theo thứ tự đã sắp —
// snapshot gốc không bao giờ bị đụng. Omitted là IDs bị loại hẳn.
// Truncated = có loại hoặc có cắt block.
type Projection struct {
	Blocks    []ContextBlock
	Omitted   []string
	Truncated bool
	// Chars là rune count của Blocks đã render (không tính marker).
	Chars int
}

// truncatedMarker báo model biết context đã bị cắt — để thiếu context không
// biến thành [FACT] bịa. Tiếng Anh cố định như protocol markers (không dịch).
// Marker nằm NGOÀI budget và không tính vào Chars.
const truncatedMarker = "[context:truncated]\nSome lower-priority context was omitted because of the context budget.\n\n"

// blockRank trả thứ tự ưu tiên (nhỏ = giữ trước) của block trong mode.
// Rank là số thứ tự trong ordered list của từng mode — KHÔNG dùng map để sắp
// vì Go map iteration ngẫu nhiên sẽ phá determinism.
// chapter là chương hiện tại của snapshot (để phân current/next/older outline).
func blockRank(mode Mode, blk ContextBlock, chapter int) int {
	const tail = 99
	isOutline := blk.Kind == "outline"
	n := outlineChapter(blk.ID)
	isCur, isNext := n > 0 && n == chapter, n > 0 && n == chapter+1
	switch mode {
	case ModeInspect:
		switch {
		case blk.Kind == "progress":
			return 0
		case blk.ID == "book":
			return 1
		case isOutline && isCur:
			return 2
		case isOutline && isNext:
			return 3
		case blk.Kind == "summary":
			return 4
		case blk.Kind == "character":
			return 5
		case blk.Kind == "world":
			return 6
		case blk.Kind == "foreshadow":
			return 7
		case blk.Kind == "timeline":
			return 8
		case blk.Kind == "review":
			return 9
		case blk.ID == "premise":
			return 10
		case isOutline:
			return 11
		}
	case ModeSuggest:
		switch {
		case blk.Kind == "progress":
			return 0
		case blk.ID == "premise":
			return 1
		case blk.ID == "book":
			return 2
		case isOutline && isCur:
			return 3
		case isOutline && isNext:
			return 4
		case blk.Kind == "summary":
			return 5
		case blk.Kind == "foreshadow":
			return 6
		case blk.Kind == "world":
			return 7
		case blk.Kind == "character":
			return 8
		case blk.Kind == "timeline":
			return 9
		case blk.Kind == "review":
			return 10
		case isOutline:
			return 11
		}
	default: // ModeAsk
		switch {
		case isOutline && isCur:
			return 0
		case blk.Kind == "summary":
			return 1
		case blk.Kind == "character":
			return 2
		case blk.Kind == "world":
			return 3
		case blk.Kind == "foreshadow":
			return 4
		case blk.Kind == "review":
			return 5
		case blk.Kind == "progress":
			return 6
		case blk.ID == "book":
			return 7
		case isOutline && isNext:
			return 8
		case blk.ID == "premise":
			return 9
		case blk.Kind == "timeline":
			return 10
		case isOutline:
			return 11
		}
	}
	return tail
}

// outlineChapter trích số chương từ ID "outline:chapter:N". Không parse được → 0.
func outlineChapter(id string) int {
	rest, ok := strings.CutPrefix(id, "outline:chapter:")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// renderedSize là rune count của block khi render dạng "[ID]\ncontent\n\n".
func renderedSize(blk ContextBlock) int {
	return utf8.RuneCountInString("[" + blk.ID + "]\n" + strings.TrimSpace(blk.Content) + "\n\n")
}

// ProjectSnapshot lọc + cắt snapshot theo mode + budget, deterministic:
// cùng input luôn ra cùng output (stable sort, không map iteration trong đường sắp).
// Không mutate snapshot gốc. maxChars <= 0 → lỗi (không panic, không đoán).
func ProjectSnapshot(snap StorySnapshot, mode Mode, maxChars int) (Projection, error) {
	if maxChars <= 0 {
		return Projection{}, errors.New("sp: budget phải > 0")
	}
	// Copy + stable sort theo (rank, index gốc). Ties giữ nguyên thứ tự snapshot.
	type ranked struct {
		blk  ContextBlock
		rank int
		idx  int
	}
	rs := make([]ranked, 0, len(snap.Blocks))
	for i, b := range snap.Blocks {
		rs = append(rs, ranked{blk: b, rank: blockRank(mode, b, snap.Chapter), idx: i})
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].rank != rs[j].rank {
			return rs[i].rank < rs[j].rank
		}
		return rs[i].idx < rs[j].idx
	})

	var out Projection
	used := 0
	for _, r := range rs {
		size := renderedSize(r.blk)
		if used+size <= maxChars {
			out.Blocks = append(out.Blocks, r.blk)
			used += size
			continue
		}
		// Không vừa: nếu chưa giữ gì cả thì cắt block này cho vừa (giữ header
		// "[ID]\n" nguyên vẹn — không cắt giữa metadata/source identifier),
		// ngược lại loại hẳn và ghi ID để audit.
		if len(out.Blocks) == 0 {
			header := "[" + r.blk.ID + "]\n"
			headRunes := utf8.RuneCountInString(header)
			room := maxChars - headRunes - 2 // "\n\n" cuối
			if room > 0 {
				cut := truncateRunes(strings.TrimSpace(r.blk.Content), room)
				out.Blocks = append(out.Blocks, ContextBlock{
					ID: r.blk.ID, Kind: r.blk.Kind, Content: cut,
				})
				used += headRunes + utf8.RuneCountInString(cut) + 2
				out.Truncated = true
				continue
			}
		}
		out.Omitted = append(out.Omitted, r.blk.ID)
		out.Truncated = true
	}
	out.Chars = used
	return out, nil
}

// truncateRunes cắt chuỗi còn tối đa n runes, không chẻ đôi rune UTF-8.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// RenderContext render projection thành text gửi advisor (STORY STATE section).
// Marker truncated (nếu có) đặt ở ĐẦU để model thấy trước khi đọc — và nằm ngoài
// budget, không tính vào Chars.
func RenderContext(snap StorySnapshot, mode Mode, maxChars int) (string, Projection, error) {
	proj, err := ProjectSnapshot(snap, mode, maxChars)
	if err != nil {
		return "", Projection{}, err
	}
	var b strings.Builder
	if proj.Truncated {
		b.WriteString(truncatedMarker)
	}
	for _, blk := range proj.Blocks {
		fmt.Fprintf(&b, "[%s]\n%s\n\n", blk.ID, strings.TrimSpace(blk.Content))
	}
	return b.String(), proj, nil
}

// EstimateTokens ước thô số token từ số ký tự (~4 chars/token latin).
// Chỉ là estimate để audit/debug — KHÔNG dùng để ra quyết định budget,
// và tên field audit phải ghi rõ estimate.
func EstimateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}
