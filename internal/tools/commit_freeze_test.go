package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newChapterOneReadyStore dựng store đã qua audit, chapter 1 có thể commit bình thường.
func newChapterOneReadyStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(1, "第一章正文。"); err != nil {
		t.Fatal(err)
	}
	return s
}

func chapterOneArgs(t *testing.T) []byte {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"chapter": 1, "title": "第一章", "summary": "摘要",
		"characters": []string{"主角"}, "key_events": []string{"事件"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return args
}

// Issue #100/#128 (phần chưa ai vá): pending commit ở stage=Started nhưng KHÔNG phải
// rewrite bị đóng băng payload. Khi validate fail, code cũ chỉ giải phóng cho
// rewrite, nên bản thường giữ nguyên payload hỏng — mọi lần thử lại đều phát lại
// cùng một lỗi và model không thể truyền tham số mới để sửa. Đây là deadlock.
func TestCommitChapterReleasesFrozenPayloadForNewChapter(t *testing.T) {
	s := newChapterOneReadyStore(t)
	tool := newTestCommitChapterTool(s)

	// Lần 1: commit hỏng vì args thiếu title (ErrToolArgs) → lưu pending?
	// Không: validate chạy trước SavePendingCommit, nên phải tạo pending bằng tay
	// để mô phỏng đúng trạng thái "đã đóng băng rồi mới hỏng".
	bad, err := json.Marshal(map[string]any{
		"chapter": 1, "title": "第一章", "summary": "摘要",
		"characters": []string{"主角"}, "key_events": []string{"事件"},
		// state_changes thiếu field → chapterfacts.Validate trả ErrToolArgs
		"state_changes": []map[string]any{{"entity": "主角"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Chốt băng payload hỏng ở stage Started, giống trạng thái sau khi một lần
	// commit đã đi qua validate rồi hỏng vì hoàn cảnh đổi.
	if err := s.Signals.SavePendingCommit(domain.PendingCommit{
		Chapter: 1,
		Stage:   domain.CommitStageStarted,
		Payload: bad,
	}); err != nil {
		t.Fatal(err)
	}

	// Lần 2: model gửi tham số ĐÚNG, nhưng payload đóng băng sẽ ghi đè lên nó.
	_, execErr := tool.Execute(context.Background(), chapterOneArgs(t))
	if execErr == nil {
		// Hợp lệ: môi trường không đổi thì payload cũ vẫn chạy được.
		return
	}
	if !strings.Contains(execErr.Error(), "已解除冻结") {
		t.Fatalf("lỗi validate phải kèm thông báo đã giải phóng đóng băng, got %v", execErr)
	}
	pending, lerr := s.Signals.LoadPendingCommit()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if pending != nil {
		t.Fatalf("payload hỏng phải được xoá để model tự sửa, còn lại: %+v", pending)
	}
}

// Chốt hồi quy: sau khi giải phóng, lần gọi với tham số đúng phải commit thành công.
func TestCommitChapterSucceedsAfterFreezeRelease(t *testing.T) {
	s := newChapterOneReadyStore(t)
	tool := newTestCommitChapterTool(s)

	bad, err := json.Marshal(map[string]any{
		"chapter": 1, "title": "第一章", "summary": "摘要",
		"characters": []string{"主角"}, "key_events": []string{"事件"},
		"state_changes": []map[string]any{{"entity": "主角"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Signals.SavePendingCommit(domain.PendingCommit{
		Chapter: 1, Stage: domain.CommitStageStarted, Payload: bad,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), chapterOneArgs(t)); err == nil {
		t.Fatal("payload hỏng phải bị chặn")
	}

	// Lần này không còn payload đóng băng → tham số mới được dùng.
	if _, err := tool.Execute(context.Background(), chapterOneArgs(t)); err != nil {
		t.Fatalf("sau khi giải phóng đóng băng, commit phải thành công: %v", err)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if progress == nil || !containsInt(progress.CompletedChapters, 1) {
		t.Fatalf("chương 1 phải được đánh dấu hoàn thành, got %+v", progress)
	}
}

func containsInt(list []int, want int) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
