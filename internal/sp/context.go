package sp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
)

// maxSnapshotRetries là số lần dựng lại snapshot khi anchor đổi giữa chừng.
// Quá số này thì trả lỗi rõ ràng thay vì lặp vô hạn trong lúc Writer commit liên tục.
const maxSnapshotRetries = 2

// progressAnchor trích các trường progress thực sự quyết định story position/state.
// Không so timestamp hay các trường trang trí: so thừa thì snapshot bị vứt oan.
func progressAnchor(p *domain.Progress) string {
	if p == nil {
		return "nil"
	}
	completed := append([]int(nil), p.CompletedChapters...)
	sort.Ints(completed)
	pending := append([]int(nil), p.PendingRewrites...)
	sort.Ints(pending)
	return fmt.Sprintf("%s|cur=%d|total=%d|done=%v|words=%d|inprog=%d|flow=%s|pending=%v|vol=%d|arc=%d|layered=%v",
		p.Phase, p.CurrentChapter, p.TotalChapters, completed, p.TotalWordCount,
		p.InProgressChapter, p.Flow, pending, p.CurrentVolume, p.CurrentArc, p.Layered)
}

// BuildSnapshot dựng StorySnapshot nhất quán từ Store.
//
// Thuật toán anchor: đọc progress trước (A), đọc toàn bộ sections, đọc progress
// sau (B). A != B nghĩa là Writer commit xen giữa — snapshot nửa cũ nửa mới nên
// vứt đi dựng lại, tối đa maxSnapshotRetries lần. Quá số lần thì trả lỗi rõ ràng
// "story state đang thay đổi, thử lại" thay vì trả một state nửa vời như thể nó
// hoàn chỉnh.
//
// Chỉ đọc những gì Store đã ghi nhận. Không đọc drafts/, không đọc stream của
// Writer: /sp trả lời dựa trên câu chuyện đã tồn tại, không dựa trên chương đang
// nửa sinh nửa chết.
func BuildSnapshot(st *storepkg.Store) (StorySnapshot, error) {
	if st == nil {
		return StorySnapshot{}, fmt.Errorf("sp: thiếu Store")
	}
	var lastErr error
	for attempt := 0; attempt <= maxSnapshotRetries; attempt++ {
		snap, anchorA, anchorB, retryable, err := buildOnce(st)
		if err != nil {
			// Lỗi đọc/ghi (store hỏng, progress chưa init) trả ngay: retry cũng
			// ra đúng lỗi đó, lặp chỉ tốn thời gian.
			if !retryable {
				return StorySnapshot{}, err
			}
			lastErr = err
			continue
		}
		if anchorA == anchorB {
			return snap, nil
		}
		lastErr = fmt.Errorf("story state đang thay đổi giữa lúc đọc (lần %d)", attempt+1)
	}
	return StorySnapshot{}, fmt.Errorf("story state đang thay đổi, thử lại: %w", lastErr)
}

func buildOnce(st *storepkg.Store) (snap StorySnapshot, anchorA, anchorB string, retryable bool, err error) {
	progA, err := st.Progress.Load()
	if err != nil {
		return StorySnapshot{}, "", "", false, fmt.Errorf("đọc progress: %w", err)
	}
	if progA == nil {
		// Store chưa khởi tạo progress: không có gì để snapshot. Lỗi rõ ràng,
		// không phải "đang thay đổi".
		return StorySnapshot{}, "", "", false, fmt.Errorf("progress chưa khởi tạo, không dựng được snapshot")
	}
	anchorA = progressAnchor(progA)

	var blocks []ContextBlock
	add := func(b ContextBlock) {
		if strings.TrimSpace(b.Content) != "" {
			blocks = append(blocks, b)
		}
	}

	// Vị trí hiện tại: phase, chương, tập/cung.
	add(ContextBlock{ID: "progress", Kind: "progress", Content: renderProgress(progA)})

	// Sách: tiêu đề + tóm tắt.
	if book, err := st.Book.Load(); err == nil && book != nil {
		add(ContextBlock{ID: "book", Kind: "book",
			Content: strings.TrimSpace(book.Title) + "\n" + strings.TrimSpace(book.Synopsis)})
	}

	// Tiền đề.
	if premise, err := st.Outline.LoadPremise(); err == nil {
		add(ContextBlock{ID: "premise", Kind: "premise", Content: premise})
	}

	// Dàn ý chương hiện tại + vài chương quanh nó. progA không nil ở đây
	// (nil đã return ở trên), không cần kiểm tra lại.
	cur := progA.CurrentChapter
	if cur <= 0 {
		cur = progA.LatestCompleted() + 1
	}
	for _, ch := range []int{cur - 1, cur, cur + 1} {
		if ch <= 0 {
			continue
		}
		if entry, err := st.Outline.GetChapterOutline(ch); err == nil && entry != nil {
			add(ContextBlock{
				ID:      fmt.Sprintf("outline:chapter:%d", ch),
				Kind:    "outline",
				Content: fmt.Sprintf("Chương %d: %s\nMốc chính: %s\nCảnh: %s", ch, entry.Title, entry.CoreEvent, strings.Join(entry.Scenes, "; ")),
			})
		}
	}

	// Tóm tắt các chương gần nhất đã chốt.
	latest := progA.LatestCompleted()
	if sums, err := st.Summaries.LoadRecentSummaries(latest, 3); err == nil {
		for _, s := range sums {
			add(ContextBlock{
				ID:      fmt.Sprintf("summary:chapter:%d", s.Chapter),
				Kind:    "summary",
				Content: fmt.Sprintf("Chương %d (%s): %s\nSự kiện: %s", s.Chapter, s.Title, s.Summary, strings.Join(s.KeyEvents, "; ")),
			})
		}
	}

	// Trạng thái nhân vật.
	if chars, err := st.Characters.Load(); err == nil {
		add(ContextBlock{ID: "characters", Kind: "character", Content: renderCharacters(chars)})
	}

	// Quy tắc thế giới: chỉ lấy phần đang hiệu lực, không lấy lịch sử sửa.
	if rules, err := st.World.LoadWorldRules(); err == nil {
		var lines []string
		for _, r := range rules {
			lines = append(lines, fmt.Sprintf("- [%s] %s (giới hạn: %s)", r.Category, r.Rule, r.Boundary))
		}
		add(ContextBlock{ID: "world:rules", Kind: "world", Content: strings.Join(lines, "\n")})
	}

	// Foreshadow đang mở (planted/advanced, chưa resolved).
	if ledger, err := st.World.LoadForeshadowLedger(); err == nil {
		add(ContextBlock{ID: "foreshadow:active", Kind: "foreshadow", Content: renderForeshadow(ledger)})
	}

	// Timeline gần nhất.
	if tl, err := st.World.LoadRecentTimeline(progA.LatestCompleted(), 10); err == nil {
		var lines []string
		for _, e := range tl {
			lines = append(lines, fmt.Sprintf("C%d [%s]: %s", e.Chapter, e.Time, e.Event))
		}
		add(ContextBlock{ID: "timeline:recent", Kind: "timeline", Content: strings.Join(lines, "\n")})
	}

	// Review gần nhất còn treo: chỉ lấy issue chưa xong, không lấy điểm số.
	if rev, err := st.Signals.LoadLastReviewSignal(); err == nil && rev != nil && len(rev.Issues) > 0 {
		var lines []string
		for _, is := range rev.Issues {
			lines = append(lines, fmt.Sprintf("- [%s/%s] %s", is.Severity, is.Type, is.Description))
		}
		add(ContextBlock{ID: fmt.Sprintf("review:chapter:%d", rev.Chapter), Kind: "review",
			Content: fmt.Sprintf("Chương %d, phán quyết: %s\n%s", rev.Chapter, rev.Verdict, strings.Join(lines, "\n"))})
	}

	progB, err := st.Progress.Load()
	if err != nil {
		// Lỗi lần hai: có thể do Writer đang ghi xen giữa (temp+rename xong
		// nhưng checkpoint chưa xong). Retry được, khác với lỗi lần đầu.
		return StorySnapshot{}, "", "", true, fmt.Errorf("đọc progress lần hai: %w", err)
	}
	anchorB = progressAnchor(progB)

	snap = StorySnapshot{CapturedAt: time.Now(), Blocks: blocks}
	snap.ProgressDigest = digestSnapshot(blocks, anchorA)
	snap.Chapter = progA.CurrentChapter
	if snap.Chapter <= 0 {
		snap.Chapter = progA.LatestCompleted() + 1
	}
	return snap, anchorA, anchorB, false, nil
}

func digestSnapshot(blocks []ContextBlock, anchor string) string {
	h := sha256.New()
	h.Write([]byte(anchor))
	h.Write([]byte{0})
	for _, b := range blocks {
		h.Write([]byte(b.ID))
		h.Write([]byte{0})
		h.Write([]byte(b.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func renderProgress(p *domain.Progress) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("Giai đoạn: %s. Đang viết chương %d. Đã chốt: %v. Tổng chữ: %d. Tập %d, cung %d.",
		p.Phase, p.CurrentChapter, p.CompletedChapters, p.TotalWordCount, p.CurrentVolume, p.CurrentArc)
}

func renderCharacters(chars []domain.Character) string {
	var lines []string
	for _, c := range chars {
		line := "- " + c.Name
		if c.Role != "" {
			line += " (" + c.Role + ")"
		}
		if c.Description != "" {
			line += ": " + c.Description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderForeshadow(ledger []domain.ForeshadowEntry) string {
	var lines []string
	for _, e := range ledger {
		if e.Status == "resolved" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- [%s] C%d gieo: %s", e.Status, e.PlantedAt, e.Description))
	}
	return strings.Join(lines, "\n")
}
