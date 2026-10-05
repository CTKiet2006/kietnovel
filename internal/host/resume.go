package host

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/revision"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// upgradeProject 把老项目数据升到当前格式，并把同一个根错误同时交给界面和日志。
func upgradeProject(st *storepkg.Store) error {
	if err := runProjectUpgrades(st); err != nil {
		slog.Error("项目数据升级失败", "module", "migration", "err", err)
		return err
	}
	return nil
}

func runProjectUpgrades(st *storepkg.Store) error {
	version, err := st.LoadProjectFormatVersion()
	if err != nil {
		return fmt.Errorf("读取项目格式版本: %w", err)
	}
	if version > storepkg.CurrentProjectFormatVersion {
		return fmt.Errorf("项目格式版本 v%d 高于当前程序支持的 v%d，请升级 ainovel-cli", version, storepkg.CurrentProjectFormatVersion)
	}
	for version < storepkg.CurrentProjectFormatVersion {
		next := version + 1
		switch version {
		case storepkg.LegacyProjectFormatVersion:
			if err := migrateLegacyBook(st); err != nil {
				return fmt.Errorf("升级项目数据 v%d→v%d: %w", version, next, err)
			}
		case storepkg.ChapterRecordProjectFormatVersion:
			// v3 补齐 v2 可能遗漏的接纳记录；已有记录由迁移函数原样保留。
			if err := revision.MigrateLegacyBaseline(st); err != nil {
				return fmt.Errorf("升级项目数据 v%d→v%d: %w", version, next, err)
			}
		default:
			return fmt.Errorf("不支持从项目格式 v%d 升级", version)
		}
		if err := st.SaveProjectFormatVersion(next); err != nil {
			return fmt.Errorf("保存项目格式版本 v%d: %w", next, err)
		}
		slog.Info("项目数据升级完成", "module", "migration", "from", version, "to", next)
		version = next
	}
	return nil
}

func migrateLegacyBook(st *storepkg.Store) error {
	book, err := st.Book.Load()
	if err != nil {
		return err
	}
	if book == nil {
		book, err = loadLegacyBook(st)
		if err != nil || book == nil {
			return err
		}
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

// resumeLabel 基于事实生成 Resume 的 UI 标签。
// label 为空表示无可恢复状态（应走新建）。恢复本身不需要任何 prompt——
// Engine 只恢复事实：从 store 重算路由续跑（docs/engine-rfc.md §6）。
func resumeLabel(store *storepkg.Store) (string, error) {
	progress, err := store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if progress == nil || progress.Phase == domain.PhaseComplete {
		return "", nil
	}
	return describeResume(store, progress)
}

// describeResume 生成人类可读的恢复标签；不影响 Engine 路由。
// 所有执行路由由 Flow Router 按事实推导；这里仅面向 UI 的 "恢复：xxx"。
func describeResume(store *storepkg.Store, progress *domain.Progress) (string, error) {
	switch progress.Phase {
	case domain.PhasePremise, domain.PhaseOutline:
		return fmt.Sprintf("恢复：规划阶段（%s）", progress.Phase), nil
	case domain.PhaseWriting:
		// 优先级与 Router 的决策优先级对齐，让 label 与即将派发的指令一致。
		pending, err := store.Signals.LoadPendingCommit()
		if err != nil {
			return "", fmt.Errorf("读取待恢复提交: %w", err)
		}
		if pending != nil {
			return fmt.Sprintf("恢复：第 %d 章提交中断", pending.Chapter), nil
		}
		if len(progress.PendingRewrites) > 0 {
			verb := "重写"
			if progress.Flow == domain.FlowPolishing {
				verb = "打磨"
			}
			return fmt.Sprintf("%s恢复：%d 章待处理", verb, len(progress.PendingRewrites)), nil
		}
		if progress.Flow == domain.FlowReviewing {
			return "恢复：审阅中断", nil
		}
		if progress.InProgressChapter > 0 {
			return fmt.Sprintf("恢复：第 %d 章进行中", progress.InProgressChapter), nil
		}
		label, err := describeArcEndLabel(store, progress)
		if err != nil {
			return "", err
		}
		if label != "" {
			return label, nil
		}
		return fmt.Sprintf("恢复：从第 %d 章继续", progress.NextChapter()), nil
	}
	return "恢复", nil
}

// describeArcEndLabel 为弧末/卷末的多种中间状态生成贴合 UI 的标签。
// 与 flow.Route 的弧末分支保持同序，保证 label 与 Router 首条指令对齐。
func describeArcEndLabel(store *storepkg.Store, progress *domain.Progress) (string, error) {
	if !progress.Layered || len(progress.CompletedChapters) == 0 {
		return "", nil
	}
	lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
	boundary, err := store.Outline.CheckArcBoundary(lastCh)
	if err != nil {
		return "", fmt.Errorf("检查弧边界: %w", err)
	}
	if boundary == nil || !boundary.IsArcEnd {
		return "", nil
	}
	vol, arc := boundary.Volume, boundary.Arc
	hasArcReview, err := store.World.HasArcReview(lastCh)
	if err != nil {
		return "", fmt.Errorf("读取弧评审: %w", err)
	}
	hasArcSummary, err := store.Summaries.HasArcSummary(vol, arc)
	if err != nil {
		return "", fmt.Errorf("读取弧摘要: %w", err)
	}
	hasVolumeSummary := false
	if boundary.IsVolumeEnd {
		hasVolumeSummary, err = store.Summaries.HasVolumeSummary(vol)
		if err != nil {
			return "", fmt.Errorf("读取卷摘要: %w", err)
		}
	}
	switch {
	case !hasArcReview:
		return fmt.Sprintf("恢复：弧末评审待处理（V%d A%d）", vol, arc), nil
	case !hasArcSummary:
		return fmt.Sprintf("恢复：弧摘要待生成（V%d A%d）", vol, arc), nil
	case boundary.IsVolumeEnd && !hasVolumeSummary:
		return fmt.Sprintf("恢复：卷摘要待生成（V%d）", vol), nil
	case boundary.NeedsExpansion && boundary.NextArc > 0:
		return fmt.Sprintf("恢复：待展开下一弧（V%d A%d）", boundary.NextVolume, boundary.NextArc), nil
	case boundary.NeedsNewVolume:
		return fmt.Sprintf("恢复：待决策下一卷（V%d 末）", vol), nil
	}
	return "", nil
}
