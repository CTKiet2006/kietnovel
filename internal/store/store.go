package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
)

// Store is the composition root of the state management and holds every sub-store.
type Store struct {
	dir string

	Progress *ProgressStore
	Book     *BookStore
	// BookLanguage khoá ngôn ngữ sáng tác của riêng truyện này. Tách khỏi
	// Book vì book.json là struct domain nghiêm ngặt (Validate bắt buộc title +
	// synopsis), thêm trường vào đó sẽ làm truyện cũ hỏng khi nạp lại.
	BookLanguage   *BookLanguageStore
	Outline        *OutlineStore
	Drafts         *DraftStore
	Summaries      *SummaryStore
	RunMeta        *RunMetaStore
	UserRules      *UserRulesStore
	Signals        *SignalStore
	Runtime        *RuntimeStore
	Characters     *CharacterStore
	World          *WorldStore
	Checkpoints    *CheckpointStore
	Sessions       *SessionStore
	Usage          *UsageStore
	Simulation     *SimulationStore
	Decisions      *DecisionStore
	ChapterRecords *ChapterRecordStore
	Revisions      *RevisionStore

	crossMu sync.Mutex // serializes cross-domain coordination; it does not make multiple files transactionally atomic
}

const (
	LegacyProjectFormatVersion        = 1
	ChapterRecordProjectFormatVersion = 2
	CurrentProjectFormatVersion       = 3
	projectFormatPath                 = "meta/format.json"
)

type projectFormat struct {
	Version int `json:"version"`
}

// NewStore creates the state manager, where dir is the novel output root directory.
func NewStore(dir string) *Store {
	io := newIO(dir)
	outline := NewOutlineStore(io)
	return &Store{
		dir:            dir,
		Progress:       NewProgressStore(newIO(dir)),
		Book:           NewBookStore(newIO(dir)),
		BookLanguage:   NewBookLanguageStore(newIO(dir)),
		Outline:        outline,
		Drafts:         NewDraftStore(newIO(dir)),
		Summaries:      NewSummaryStore(newIO(dir), outline),
		RunMeta:        NewRunMetaStore(newIO(dir)),
		UserRules:      NewUserRulesStore(newIO(dir)),
		Signals:        NewSignalStore(newIO(dir)),
		Runtime:        NewRuntimeStore(newIO(dir)),
		Characters:     NewCharacterStore(newIO(dir), outline),
		World:          NewWorldStore(newIO(dir)),
		Checkpoints:    NewCheckpointStore(io),
		Sessions:       NewSessionStore(newIO(dir)),
		Usage:          NewUsageStore(newIO(dir)),
		Simulation:     NewSimulationStore(newIO(dir)),
		Decisions:      NewDecisionStore(newIO(dir)),
		ChapterRecords: NewChapterRecordStore(newIO(dir)),
		Revisions:      NewRevisionStore(newIO(dir)),
	}
}

// Dir returns the output root directory.
func (s *Store) Dir() string { return s.dir }

// LoadProjectFormatVersion returns the data-format version of the work directory. An older work has no version file,
// so it counts as v1 and is upgraded uniformly by the startup migration; business code needs no legacy-format branch.
func (s *Store) LoadProjectFormatVersion() (int, error) {
	var format projectFormat
	if err := s.Progress.io.ReadJSON(projectFormatPath, &format); err != nil {
		if os.IsNotExist(err) {
			return LegacyProjectFormatVersion, nil
		}
		return 0, err
	}
	if format.Version <= 0 {
		return 0, fmt.Errorf("项目格式版本无效: %d", format.Version)
	}
	return format.Version, nil
}

// SaveProjectFormatVersion atomically updates the project format version once the whole migration has finished.
func (s *Store) SaveProjectFormatVersion(version int) error {
	if version <= 0 {
		return fmt.Errorf("项目格式版本必须大于 0: %d", version)
	}
	return s.Progress.io.WriteJSON(projectFormatPath, projectFormat{Version: version})
}

// CheckConsistency does one shallow validation pass over the fact layer, used at startup/recovery to produce warnings.
// Purely read-only: it fixes nothing and only returns a readable description of the problems. The caller decides how to surface it (log / UI).
// To avoid the IO cost of scanning the whole directory, only the key points of Progress are validated:
//   - the last completed chapter must have a final text under chapters/
//   - in Layered mode the current Volume/Arc must be findable in layered_outline
func (s *Store) CheckConsistency() []string {
	var warnings []string
	progress, err := s.Progress.Load()
	if err != nil {
		return append(warnings, fmt.Sprintf("progress 读取失败: %v", err))
	}
	if progress == nil {
		return warnings
	}
	if n := len(progress.CompletedChapters); n > 0 {
		lastCh := progress.CompletedChapters[n-1]
		if text, err := s.Drafts.LoadChapterText(lastCh); err != nil {
			warnings = append(warnings, fmt.Sprintf("第 %d 章终稿读取失败: %v", lastCh, err))
		} else if text == "" {
			warnings = append(warnings, fmt.Sprintf("progress 标记第 %d 章已完成，但 chapters/%02d.md 不存在或为空", lastCh, lastCh))
		}
	}
	if progress.Layered && progress.CurrentVolume > 0 && progress.CurrentArc > 0 {
		volumes, err := s.Outline.LoadLayeredOutline()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("分层大纲读取失败: %v", err))
		} else if len(volumes) > 0 {
			found := false
			for _, v := range volumes {
				if v.Index != progress.CurrentVolume {
					continue
				}
				for _, a := range v.Arcs {
					if a.Index == progress.CurrentArc {
						found = true
						break
					}
				}
				break
			}
			if !found {
				warnings = append(warnings, fmt.Sprintf("progress 当前 V%d A%d 在分层大纲中找不到对应条目", progress.CurrentVolume, progress.CurrentArc))
			}
		}
	}
	return warnings
}

// FoundationMissing returns the work information and foundation still missing from the initial planning, in a stable order.
// Long-form mode (layered_outline already exists) additionally requires the compass. A read failure must be returned as is; a
// corrupted or unreadable artifact must never be mistaken for "not created yet", or the caller could overwrite real data.
func (s *Store) FoundationMissing() ([]string, error) {
	var missing []string
	book, err := s.Book.Load()
	if err != nil {
		return nil, fmt.Errorf("load book metadata: %w", err)
	}
	if book == nil {
		missing = append(missing, "book")
	}
	premise, err := s.Outline.LoadPremise()
	if err != nil {
		return nil, fmt.Errorf("load premise: %w", err)
	}
	if premise == "" {
		missing = append(missing, "premise")
	}
	outline, err := s.Outline.LoadOutline()
	if err != nil {
		return nil, fmt.Errorf("load outline: %w", err)
	}
	if len(outline) == 0 {
		missing = append(missing, "outline")
	}
	characters, err := s.Characters.Load()
	if err != nil {
		return nil, fmt.Errorf("load characters: %w", err)
	}
	if len(characters) == 0 {
		missing = append(missing, "characters")
	}
	rules, err := s.World.LoadWorldRules()
	if err != nil {
		return nil, fmt.Errorf("load world rules: %w", err)
	}
	if len(rules) == 0 {
		missing = append(missing, "world_rules")
	}
	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, fmt.Errorf("load layered outline: %w", err)
	}
	if len(layered) > 0 {
		compass, err := s.Outline.LoadCompass()
		if err != nil {
			return nil, fmt.Errorf("load compass: %w", err)
		}
		if compass == nil {
			missing = append(missing, "compass")
		}
	}
	// A new book may only move from planning into writing after the model has explicitly semantically reviewed the already persisted artifacts.
	// PhaseWriting/Complete stands for an old book or an already reviewed new book, keeping historical projects compatible; the review itself
	// is an action rather than a missing file, so it is only appended when the other artifacts are all present.
	if len(missing) == 0 {
		progress, err := s.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w", err)
		}
		if progress == nil || (progress.Phase != domain.PhaseWriting && progress.Phase != domain.PhaseComplete) {
			missing = append(missing, "foundation_audit")
		}
	}
	return missing, nil
}

// FoundationFingerprint returns the content fingerprint of the current foundation artifacts. The Architect must hand
// back exactly this value, as read from novel_context, to the review tool, so that the verdict targets the version actually on disk
// and not something unsaved or already stale in the session.
func (s *Store) FoundationFingerprint() (string, error) {
	files := []string{"meta/book.json", "premise.md", "outline.json", "characters.json", "world_rules.json"}
	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return "", fmt.Errorf("load layered outline: %w", err)
	}
	if len(layered) > 0 {
		files = append(files, "layered_outline.json", "meta/compass.json")
	}

	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", rel, err)
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Init creates the required sub-directory structure.
func (s *Store) Init() error {
	if err := s.Checkpoints.InitError(); err != nil {
		return fmt.Errorf("load checkpoints: %w", err)
	}
	return s.Progress.io.EnsureDirs([]string{
		"chapters", "summaries", "drafts", "reviews", "meta", "meta/chapter_records", "meta/runtime", "meta/runtime/tasks", "meta/sessions", "meta/sessions/agents",
	})
}

// ── Cross-domain coordination methods ──

// ArcPosition is the volume/arc position determined by the story order of the layered outline.
type ArcPosition struct {
	Volume int
	Arc    int
}

// ExpandNextArc expands the next arc after the currently completed one (Outline + Progress move together).
// The target is determined jointly by the persisted progress and the outline; the model is only responsible for the creative content.
func (s *Store) ExpandNextArc(expansion domain.ArcExpansion) (ArcPosition, error) {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()

	var volumes []domain.VolumeOutline
	if err := s.Outline.io.ReadJSONUnlocked("layered_outline.json", &volumes); err != nil {
		return ArcPosition{}, fmt.Errorf("load layered_outline: %w", err)
	}
	repairMissingLayeredIndexes(volumes)

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return ArcPosition{}, err
	}
	if p == nil {
		return ArcPosition{}, fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
	}
	boundary := checkArcBoundary(volumes, p.LatestCompleted())
	if boundary == nil || !boundary.IsArcEnd || boundary.nextVolumePos < 0 {
		return ArcPosition{}, fmt.Errorf("当前进度不在弧末，无下一弧可展开: %w", errs.ErrToolPrecondition)
	}
	position := ArcPosition{Volume: boundary.NextVolume, Arc: boundary.NextArc}
	volumes, err = s.Outline.expandArcAtUnlocked(volumes, boundary.nextVolumePos, boundary.nextArcPos, expansion)
	if err != nil {
		return ArcPosition{}, err
	}
	p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
	if err := s.Progress.saveUnlocked(p); err != nil {
		return ArcPosition{}, err
	}
	return position, nil
}

// AppendVolume appends a new volume at the end of the layered outline (Outline + Progress move together).
func (s *Store) AppendVolume(vol domain.VolumeOutline) (domain.VolumeOutline, error) {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()

	volumes, saved, err := s.Outline.appendVolumeUnlocked(vol)
	if err != nil {
		return domain.VolumeOutline{}, err
	}

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return domain.VolumeOutline{}, err
	}
	if p == nil {
		p = &domain.Progress{}
	}
	p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
	if err := s.Progress.saveUnlocked(p); err != nil {
		return domain.VolumeOutline{}, err
	}
	return saved, nil
}

// ReviseOutline replaces the not-yet-happened planned tail starting at fromChapter.
// The flat outline replaces the tail of the whole book; the layered outline replaces only the tail of the arc that holds the target chapter. This definition lets a replay
// of the same payload produce the same result, while avoiding operation enumerations such as JSON Patch and insert/delete.
func (s *Store) ReviseOutline(fromChapter int, replacement []domain.OutlineEntry) (int, error) {
	if fromChapter <= 0 {
		return 0, fmt.Errorf("from_chapter must be > 0: %w", errs.ErrToolArgs)
	}

	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return 0, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if p == nil {
		return 0, fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
	}
	if p.Phase == domain.PhaseComplete {
		return 0, fmt.Errorf("全书已完结，不允许修改大纲: %w", errs.ErrToolPrecondition)
	}
	protected := p.InProgressChapter
	if latest := p.LatestCompleted(); latest > protected {
		protected = latest
	}
	if fromChapter <= protected {
		return 0, fmt.Errorf("第 %d 章已完成或正在写作；大纲修订必须从第 %d 章之后开始: %w",
			fromChapter, protected, errs.ErrToolPrecondition)
	}

	if p.Layered {
		volumes, err := s.Outline.reviseLayeredTailUnlocked(fromChapter, replacement)
		if err != nil {
			return 0, err
		}
		p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
		if err := s.Progress.saveUnlocked(p); err != nil {
			return 0, fmt.Errorf("save progress: %w: %w", errs.ErrStoreWrite, err)
		}
		return p.TotalChapters, nil
	}

	outline, err := s.Outline.reviseFlatTailUnlocked(fromChapter, replacement)
	if err != nil {
		return 0, err
	}
	p.TotalChapters = len(outline)
	if err := s.Progress.saveUnlocked(p); err != nil {
		return 0, fmt.Errorf("save progress: %w: %w", errs.ErrStoreWrite, err)
	}
	return p.TotalChapters, nil
}

// ClearHandledSteer clears PendingSteer and resets the legacy FlowSteering state.
// Two files cannot form a filesystem transaction, so the repeatable Progress is written first and the recovery intent is deleted last;
// if any step fails, PendingSteer survives at the very least and the next Resume can safely replay.
func (s *Store) ClearHandledSteer() error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.RunMeta.io.mu.Lock()
	defer s.RunMeta.io.mu.Unlock()
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	meta, err := s.RunMeta.loadUnlocked()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	p, err := s.Progress.loadUnlocked()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if p != nil && p.Flow == domain.FlowSteering {
		if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
			return err
		}
		p.Flow = domain.FlowWriting
		if err := s.Progress.saveUnlocked(p); err != nil {
			return err
		}
	}
	if meta != nil && meta.PendingSteer != "" {
		meta.PendingSteer = ""
		if err := s.RunMeta.saveUnlocked(*meta); err != nil {
			return err
		}
	}
	return nil
}
