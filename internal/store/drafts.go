package store

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// DraftStore manages chapter plans, drafts and final drafts.
type DraftStore struct{ io *IO }

func NewDraftStore(io *IO) *DraftStore { return &DraftStore{io: io} }

// SaveChapterPlan stores the chapter plan in drafts/{ch}.plan.json.
func (s *DraftStore) SaveChapterPlan(plan domain.ChapterPlan) error {
	return s.io.WriteJSON(fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter), plan)
}

// LoadChapterPlan reads the chapter plan.
func (s *DraftStore) LoadChapterPlan(chapter int) (*domain.ChapterPlan, error) {
	var plan domain.ChapterPlan
	if err := s.io.ReadJSON(fmt.Sprintf("drafts/%02d.plan.json", chapter), &plan); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &plan, nil
}

// SaveDraft stores the whole-chapter draft in drafts/{ch}.draft.md.
func (s *DraftStore) SaveDraft(chapter int, content string) error {
	return s.io.WriteMarkdown(fmt.Sprintf("drafts/%02d.draft.md", chapter), content)
}

// AppendDraft appends content to an existing draft (continue-writing mode).
func (s *DraftStore) AppendDraft(chapter int, content string) error {
	rel := fmt.Sprintf("drafts/%02d.draft.md", chapter)
	return s.io.WithWriteLock(func() error {
		existing, err := s.io.ReadFileUnlocked(rel)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var merged string
		if len(existing) > 0 {
			merged = string(existing) + "\n\n" + content
		} else {
			merged = content
		}
		return s.io.WriteFileUnlocked(rel, []byte(merged))
	})
}

// LoadDraft reads the whole-chapter draft.
func (s *DraftStore) LoadDraft(chapter int) (string, error) {
	data, err := s.io.ReadFile(fmt.Sprintf("drafts/%02d.draft.md", chapter))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LoadChapterContent loads the chapter draft body and its word count.
// ListDrafts liệt kê số chương đang có bản nháp trong drafts/, tăng dần.
// Dùng cho màn đọc: người viết cần xem được bản nháp để quyết định có chốt hay
// không, chứ chỉ đọc bản chốt thì thành vòng luẩn quẩn.
func (s *DraftStore) ListDrafts() ([]int, error) {
	entries, err := os.ReadDir(s.io.path("drafts"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(name, ".md"))
		if err != nil || n <= 0 {
			continue
		}
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}

func (s *DraftStore) LoadChapterContent(chapter int) (string, int, error) {
	draft, err := s.LoadDraft(chapter)
	if err != nil {
		return "", 0, err
	}
	if draft != "" {
		return draft, domain.WordCount(draft), nil
	}
	return "", 0, nil
}

// SaveFinalChapter stores the final chapter text in chapters/{ch}.md.
func (s *DraftStore) SaveFinalChapter(chapter int, content string) error {
	return s.io.WriteMarkdown(fmt.Sprintf("chapters/%02d.md", chapter), content)
}

// LoadChapterText reads the text of a committed final chapter.
func (s *DraftStore) LoadChapterText(chapter int) (string, error) {
	data, err := s.io.ReadFile(fmt.Sprintf("chapters/%02d.md", chapter))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LoadChapterRange reads a fragment of the final chapter text within a given range.
func (s *DraftStore) LoadChapterRange(from, to, maxRunes int) (map[int]string, error) {
	result := make(map[int]string)
	for ch := from; ch <= to; ch++ {
		text, err := s.LoadChapterText(ch)
		if err != nil {
			return nil, err
		}
		if text == "" {
			continue
		}
		if maxRunes > 0 {
			runes := []rune(text)
			if len(runes) > maxRunes {
				text = string(runes[:maxRunes]) + "..."
			}
		}
		result[ch] = text
	}
	return result, nil
}

var dialogueRe = regexp.MustCompile(`"[^"]*"`)

// ExtractDialogue extracts the dialogue fragments of a given character from the committed chapters.
// maxCompletedChapter is passed in by the caller to avoid a cross-domain dependency.
func (s *DraftStore) ExtractDialogue(characterName string, aliases []string, maxSamples, maxCompletedChapter int) ([]string, error) {
	if maxSamples <= 0 {
		maxSamples = 5
	}
	names := append([]string{characterName}, aliases...)

	var samples []string
	var readErrs []error
	for ch := maxCompletedChapter; ch >= 1 && len(samples) < maxSamples; ch-- {
		text, err := s.LoadChapterText(ch)
		if err != nil {
			readErrs = append(readErrs, fmt.Errorf("chapter %d: %w", ch, err))
			continue
		}
		if text == "" {
			continue
		}
		paragraphs := strings.Split(text, "\n")
		for _, para := range paragraphs {
			if len(samples) >= maxSamples {
				break
			}
			found := false
			for _, name := range names {
				if strings.Contains(para, name) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			matches := dialogueRe.FindAllString(para, -1)
			for _, m := range matches {
				if len(samples) >= maxSamples {
					break
				}
				if utf8.RuneCountInString(m) > 5 {
					samples = append(samples, characterName+": "+m)
				}
			}
		}
	}
	return samples, errors.Join(readErrs...)
}

// ExtractStyleAnchors extracts representative passages from the committed chapters as style anchors.
// maxCompletedChapter is passed in by the caller to avoid a cross-domain dependency.
func (s *DraftStore) ExtractStyleAnchors(maxAnchors, maxCompletedChapter int) ([]string, error) {
	if maxAnchors <= 0 {
		maxAnchors = 5
	}

	var anchors []string
	var readErrs []error
	for ch := 1; ch <= maxCompletedChapter && len(anchors) < maxAnchors; ch++ {
		text, err := s.LoadChapterText(ch)
		if err != nil {
			readErrs = append(readErrs, fmt.Errorf("chapter %d: %w", ch, err))
			continue
		}
		if text == "" {
			continue
		}
		paragraphs := strings.Split(text, "\n\n")
		for _, para := range paragraphs {
			if len(anchors) >= maxAnchors {
				break
			}
			para = strings.TrimSpace(para)
			runeCount := utf8.RuneCountInString(para)
			if runeCount < 50 || runeCount > 300 {
				continue
			}
			if strings.Count(para, "\u201c") > 2 {
				continue
			}
			anchors = append(anchors, para)
		}
	}
	return anchors, errors.Join(readErrs...)
}
