package revision

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

type Change struct {
	Chapter       int
	BaseSHA256    string
	CurrentSHA256 string
	Before        string
	After         string
}

func Scan(st *store.Store) ([]Change, error) {
	progress, err := st.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("读取进度: %w", err)
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil, nil
	}
	chapters := slices.Clone(progress.CompletedChapters)
	slices.Sort(chapters)
	changes := make([]Change, 0)
	for _, chapter := range chapters {
		record, err := st.ChapterRecords.Load(chapter)
		if err != nil {
			return nil, err
		}
		if record == nil {
			return nil, fmt.Errorf("第 %d 章缺少接纳记录，当前项目不能安全识别外部修订", chapter)
		}
		path := filepath.Join(st.Dir(), filepath.FromSlash(fmt.Sprintf("chapters/%02d.md", chapter)))
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取第 %d 章工作区正文: %w", chapter, err)
		}
		content := domain.NormalizeChapterContent(string(data))
		digest := domain.ChapterContentSHA256(content)
		if digest == record.ContentSHA256 {
			continue
		}
		// 已接纳的正文为空时文件缺失不算改动；把非空正文删空则仍需用户处理。
		if strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("第 %d 章工作区正文为空，拒绝接纳", chapter)
		}
		changes = append(changes, Change{
			Chapter: chapter, BaseSHA256: record.ContentSHA256, CurrentSHA256: digest,
			Before: record.Content, After: content,
		})
	}
	return changes, nil
}

func ChangedChapters(changes []Change) []int {
	out := make([]int, len(changes))
	for i, change := range changes {
		out[i] = change.Chapter
	}
	return out
}
