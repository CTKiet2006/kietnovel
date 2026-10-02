package host

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/revision"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
)

// upgradeProject nâng dữ liệu dự án cũ lên định dạng hiện tại, và đưa cùng một lỗi gốc cho cả giao diện lẫn log.
func upgradeProject(st *storepkg.Store) error {
	if err := runProjectUpgrades(st); err != nil {
		slog.Error("Nâng cấp dữ liệu dự án thất bại", "module", "migration", "err", err)
		return err
	}
	return nil
}

func runProjectUpgrades(st *storepkg.Store) error {
	version, err := st.LoadProjectFormatVersion()
	if err != nil {
		return fmt.Errorf("Không đọc được phiên bản định dạng dự án: %w", err)
	}
	if version > storepkg.CurrentProjectFormatVersion {
		return fmt.Errorf("Định dạng dự án v%d mới hơn mức chương trình này hỗ trợ (v%d), hãy nâng cấp %s", version, storepkg.CurrentProjectFormatVersion, buildversion.AppName)
	}
	for version < storepkg.CurrentProjectFormatVersion {
		next := version + 1
		switch version {
		case storepkg.LegacyProjectFormatVersion:
			if err := migrateLegacyBook(st); err != nil {
				return fmt.Errorf("Nâng cấp dữ liệu dự án v%d→v%d: %w", version, next, err)
			}
		case storepkg.ChapterRecordProjectFormatVersion:
			// v3 bổ sung các bản ghi nhận có thể v2 đã bỏ sót; bản ghi đã có được giữ nguyên bởi hàm migration.
			if err := revision.MigrateLegacyBaseline(st); err != nil {
				return fmt.Errorf("Nâng cấp dữ liệu dự án v%d→v%d: %w", version, next, err)
			}
		default:
			return fmt.Errorf("Không hỗ trợ nâng cấp từ định dạng dự án v%d", version)
		}
		if err := st.SaveProjectFormatVersion(next); err != nil {
			return fmt.Errorf("Không lưu được phiên bản định dạng dự án v%d: %w", next, err)
		}
		slog.Info("Đã nâng cấp xong dữ liệu dự án", "module", "migration", "from", version, "to", next)
		version = next
	}
	return nil
}

// bookIncomplete báo meta/book.json có đọc được nhưng thiếu field bắt buộc.
// ok=false nghĩa là không đọc được hoặc parse hỏng — không phải trường hợp
// "còn thiếu", mà là dữ liệu hỏng, phải để lỗi nổi lên.
func bookIncomplete(st *storepkg.Store) (incomplete, ok bool) {
	data, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "book.json"))
	if err != nil {
		return false, false
	}
	var raw domain.BookMetadata
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, false
	}
	raw = raw.Normalized()
	return raw.Validate() != nil, true
}

func migrateLegacyBook(st *storepkg.Store) error {
	book, err := st.Book.Load()
	if err != nil {
		// book.json có sẵn nhưng thiếu field — đúng trạng thái của truyện vừa
		// tạo bằng /new: tên hiển thị đã ghi, còn synopsis thì Architect mới
		// viết sau. Không bỏ qua thì /new tạo được thư mục rồi không bao giờ
		// mở được, và người dùng tưởng lệnh hỏng.
		//
		// Chỉ bỏ qua khi file ĐỌC ĐƯỢC (JSON hợp lệ) và thiếu field. JSON hỏng
		// thì phải báo lỗi: nuốt im lặng sẽ biến dữ liệu hỏng thành truyện trắng.
		if incomplete, ok := bookIncomplete(st); ok && incomplete {
			return nil
		}
		return err
	}
	if book == nil {
		book, err = loadLegacyBook(st)
		if err != nil || book == nil {
			return err
		}
	}
	// Truyện vừa tạo bằng /new: book.json có tên hiển thị nhưng chưa có synopsis
	// (Architect viết sau bằng save_book). Không có gì để nâng cấp, và Save thì
	// nghiêm nên ghi lại sẽ lỗi — bỏ qua thay vì làm hỏng lúc khởi động.
	if book.Synopsis == "" {
		return nil
	}
	if err := st.Book.Save(*book); err != nil {
		return fmt.Errorf("保存旧作品信息: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "book", "meta/book.json"); err != nil {
		return fmt.Errorf("记录旧作品信息: %w", err)
	}
	return nil
}

func loadLegacyBook(st *storepkg.Store) (*domain.BookMetadata, error) {
	data, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "progress.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取旧作品进度: %w", err)
	}
	var legacy struct {
		NovelName string `json:"novel_name"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("解析旧作品进度: %w", err)
	}
	legacy.NovelName = strings.TrimSpace(legacy.NovelName)
	if legacy.NovelName == "" {
		return nil, nil
	}
	premise, err := st.Outline.LoadPremise()
	if err != nil {
		return nil, fmt.Errorf("读取旧故事前提: %w", err)
	}
	title := legacyPremiseTitle(premise)
	if title == "" {
		return nil, fmt.Errorf("旧故事前提缺少书名标题")
	}
	if title != legacy.NovelName {
		return nil, fmt.Errorf("旧作品书名冲突: progress=%q, premise=%q", legacy.NovelName, title)
	}
	synopsis := legacyPremiseSection(premise, "核心冲突")
	if synopsis == "" {
		return nil, fmt.Errorf("旧故事前提缺少“核心冲突”，无法生成作品简介")
	}
	return &domain.BookMetadata{Title: title, Synopsis: synopsis}, nil
}

func legacyPremiseTitle(premise string) string {
	for _, line := range strings.Split(premise, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "# ")), "《》\"")
		}
	}
	return ""
}

func legacyPremiseSection(premise, heading string) string {
	var body []string
	matched := false
	for _, line := range strings.Split(premise, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if matched {
				break
			}
			matched = strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")) == heading
			continue
		}
		if matched {
			body = append(body, line)
		}
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}

// resumeLabel builds the UI label for Resume out of facts.
// An empty label means there is no resumable state (a fresh start should be used instead). Resuming itself
// needs no prompt at all - the Engine only restores facts: it recomputes the route from the store and continues (docs/engine-rfc.md §6).
func resumeLabel(store *storepkg.Store) (Msg, error) {
	progress, err := store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return Msg{}, err
	}
	if progress == nil || progress.Phase == domain.PhaseComplete {
		return Msg{}, nil
	}
	return describeResume(store, progress)
}

// describeResume builds a human-readable resume label; it does not affect Engine routing.
// Every execution route is derived by the Flow Router from facts; this is only the UI-facing "Resume: xxx".
func describeResume(store *storepkg.Store, progress *domain.Progress) (Msg, error) {
	switch progress.Phase {
	case domain.PhasePremise, domain.PhaseOutline:
		return Msg{Key: "Khôi phục: giai đoạn kế hoạch (%s)", Args: []any{progress.Phase}}, nil
	case domain.PhaseWriting:
		// Priority aligns with the Router's decision priority so the label matches the command about to be dispatched.
		pending, err := store.Signals.LoadPendingCommit()
		if err != nil {
			return Msg{}, fmt.Errorf("đọc commit chờ khôi phục: %w", err)
		}
		if pending != nil {
			return Msg{Key: "Khôi phục: chương %d bị gián đoạn lúc ghi", Args: []any{pending.Chapter}}, nil
		}
		if len(progress.PendingRewrites) > 0 {
			verb := "Viết lại"
			if progress.Flow == domain.FlowPolishing {
				verb = "Trau chuốt"
			}
			return Msg{Key: "%s khôi phục: %d chương chờ xử lý", Args: []any{verb, len(progress.PendingRewrites)}}, nil
		}
		if progress.Flow == domain.FlowReviewing {
			return Msg{Key: "Khôi phục: gián đoạn lúc duyệt"}, nil
		}
		if progress.InProgressChapter > 0 {
			return Msg{Key: "Khôi phục: chương %d đang viết dở", Args: []any{progress.InProgressChapter}}, nil
		}
		label, err := describeArcEndLabel(store, progress)
		if err != nil {
			return Msg{}, err
		}
		if !label.Empty() {
			return label, nil
		}
		return Msg{Key: "Khôi phục: tiếp tục từ chương %d", Args: []any{progress.NextChapter()}}, nil
	}
	return Msg{Key: "Khôi phục"}, nil
}

// describeArcEndLabel builds UI-friendly labels for the various intermediate states at the end of an arc / volume.
// It keeps the same ordering as the arc-end branch of flow.Route so the label matches the Router's first command.
func describeArcEndLabel(store *storepkg.Store, progress *domain.Progress) (Msg, error) {
	if !progress.Layered || len(progress.CompletedChapters) == 0 {
		return Msg{}, nil
	}
	lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
	boundary, err := store.Outline.CheckArcBoundary(lastCh)
	if err != nil {
		return Msg{}, fmt.Errorf("kiểm tra biên cung: %w", err)
	}
	if boundary == nil || !boundary.IsArcEnd {
		return Msg{}, nil
	}
	vol, arc := boundary.Volume, boundary.Arc
	hasArcReview, err := store.World.HasArcReview(lastCh)
	if err != nil {
		return Msg{}, fmt.Errorf("đọc duyệt cung: %w", err)
	}
	hasArcSummary, err := store.Summaries.HasArcSummary(vol, arc)
	if err != nil {
		return Msg{}, fmt.Errorf("đọc tóm tắt cung: %w", err)
	}
	hasVolumeSummary := false
	if boundary.IsVolumeEnd {
		hasVolumeSummary, err = store.Summaries.HasVolumeSummary(vol)
		if err != nil {
			return Msg{}, fmt.Errorf("đọc tóm tắt tập: %w", err)
		}
	}
	switch {
	case !hasArcReview:
		return Msg{Key: "Khôi phục: chờ duyệt cuối cung (V%d A%d)", Args: []any{vol, arc}}, nil
	case !hasArcSummary:
		return Msg{Key: "Khôi phục: chờ tạo tóm tắt cung (V%d A%d)", Args: []any{vol, arc}}, nil
	case boundary.IsVolumeEnd && !hasVolumeSummary:
		return Msg{Key: "Khôi phục: chờ tạo tóm tắt tập (V%d)", Args: []any{vol}}, nil
	case boundary.NeedsExpansion && boundary.NextArc > 0:
		return Msg{Key: "Khôi phục: chờ bung cung kế (V%d A%d)", Args: []any{boundary.NextVolume, boundary.NextArc}}, nil
	case boundary.NeedsNewVolume:
		return Msg{Key: "Khôi phục: chờ quyết định tập kế (V%d cuối)", Args: []any{vol}}, nil
	}
	return Msg{}, nil
}
