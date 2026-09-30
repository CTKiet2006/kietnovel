package host

import (
	"fmt"
	"sort"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// ChapterText là nội dung một chương đã chốt, kèm tiêu đề lấy từ dàn ý.
type ChapterText struct {
	Chapter   int
	Title     string
	Body      string
	WordCount int
	// Exists phân biệt "chương chưa viết" với "chương viết rỗng". Cả hai đều cho
	// Body rỗng, nhưng người đọc cần biết để không tưởng sách bị hỏng.
	Exists bool
	// Committed phân biệt đã chốt với còn là bản nháp. Người đọc cần biết để
	// không tưởng bản nháp là bản cuối.
	Committed bool
}

// ReadChapter đọc một chương: ưu tiên bản đã chốt (chapters/), không có thì lấy
// bản nháp (drafts/).
//
// Vì sao phải đọc được cả nháp: bản nháp là thứ người viết CẦN đọc để quyết định
// có chốt hay không. Chỉ đọc được bản chốt thì tạo ra vòng luẩn quẩn — không đọc
// được nháp thì không biết có nên chốt, mà chưa chốt thì đọc đâu.
//
// Chương chưa viết trả Exists=false, Body rỗng, KHÔNG phải lỗi. Chỉ lỗi đĩa và
// dàn ý hỏng mới thành error.
//
// Tiêu đề lấy từ dàn ý, không lấy từ tiêu đề markdown trong file chương: tên file
// chỉ là %02d.md, còn dàn ý là nơi duy nhất giữ tên chương sau khi engine commit.
func (h *Host) ReadChapter(chapter int) (ChapterText, error) {
	if chapter <= 0 {
		return ChapterText{}, fmt.Errorf("số chương phải lớn hơn 0")
	}
	body, err := h.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return ChapterText{}, fmt.Errorf("đọc chương %d: %w", chapter, err)
	}
	committed := body != ""
	if !committed {
		// Chưa chốt: đọc bản nháp nếu có, để người viết xem được nội dung đang
		// làm dở trước khi quyết định duyệt.
		if draft, err := h.store.Drafts.LoadDraft(chapter); err == nil {
			body = draft
		}
	}
	out := ChapterText{
		Chapter:   chapter,
		Body:      body,
		WordCount: domain.WordCount(body),
		Exists:    body != "",
		Committed: committed,
	}
	// Dàn ý hỏng không nên chặn việc đọc: vẫn đọc được nội dung, chỉ mất tiêu đề.
	if entry, err := h.store.Outline.GetChapterOutline(chapter); err == nil && entry != nil {
		out.Title = entry.Title
	}
	return out, nil
}

// ReadableChapters liệt kê chương nào đọc được: đã chốt thì chắc chắn có; đang
// làm dở thì lấy theo danh sách bản nháp. Tăng dần, không trùng.
func (h *Host) ReadableChapters() ([]int, error) {
	seen := map[int]bool{}
	var out []int
	add := func(list []int) {
		for _, n := range list {
			if n > 0 && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	committed, err := h.CompletedChapters()
	if err != nil {
		return nil, err
	}
	add(committed)
	if drafts, err := h.store.Drafts.ListDrafts(); err == nil {
		add(drafts)
	}
	sort.Ints(out)
	return out, nil
}

// CompletedChapters liệt kê số chương đã chốt, tăng dần. Trả rỗng khi chưa có sách.
func (h *Host) CompletedChapters() ([]int, error) {
	progress, err := h.store.Progress.Load()
	if err != nil || progress == nil {
		return nil, err
	}
	out := make([]int, len(progress.CompletedChapters))
	copy(out, progress.CompletedChapters)
	return out, nil
}
