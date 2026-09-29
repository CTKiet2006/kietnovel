package exp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// Run performs one export. It returns synchronously, with a small amount of IO (local file reads and writes).
//
// Failure semantics:
//   - invalid deps/opts -> a config error returned immediately
//   - no completed chapter at all -> an error is returned (so the caller is left in no doubt)
//   - a chapters/{ch}.md missing inside the range -> an error is returned (progress disagreeing with the filesystem is a fact-layer bug and the user should see it)
//   - the output path already exists and Overwrite was not given -> an error is returned
//
// Skipped covers chapters that are in range but not finished yet (the user passed to=100 but only wrote up to 80).
func Run(ctx context.Context, deps Deps, opts Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deps.Store == nil {
		return nil, fmt.Errorf("exp: deps.Store is nil")
	}

	if opts.Format == "" {
		f, err := inferFormat(opts.OutPath)
		if err != nil {
			return nil, err
		}
		opts.Format = f
	}
	if opts.Format != FormatTXT && opts.Format != FormatEPUB {
		return nil, fmt.Errorf("exp: 暂不支持的格式 %q", opts.Format)
	}

	progress, err := deps.Store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("加载 progress 失败：%w", err)
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil, fmt.Errorf("尚无已完成章节，无内容可导出")
	}
	book, err := deps.Store.Book.Load()
	if err != nil {
		return nil, fmt.Errorf("加载作品信息失败：%w", err)
	}
	if book == nil {
		return nil, fmt.Errorf("作品信息不存在，无法导出")
	}

	completed := make(map[int]struct{}, len(progress.CompletedChapters))
	maxCh := 0
	for _, c := range progress.CompletedChapters {
		completed[c] = struct{}{}
		if c > maxCh {
			maxCh = c
		}
	}

	from := opts.From
	if from <= 0 {
		from = 1
	}
	to := opts.To
	if to <= 0 {
		to = maxCh
	}
	if from > to {
		return nil, fmt.Errorf("章节范围无效：from=%d > to=%d", from, to)
	}

	var chapters, skipped []int
	for ch := from; ch <= to; ch++ {
		if _, ok := completed[ch]; ok {
			chapters = append(chapters, ch)
		} else {
			skipped = append(skipped, ch)
		}
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf("范围 %d..%d 内无已完成章节", from, to)
	}

	bodies := make(map[int]string, len(chapters))
	for _, ch := range chapters {
		text, err := deps.Store.Drafts.LoadChapterText(ch)
		if err != nil {
			return nil, fmt.Errorf("读取第 %d 章失败：%w", ch, err)
		}
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("progress 标记第 %d 章已完成，但 chapters/%02d.md 缺失或为空", ch, ch)
		}
		bodies[ch] = text
	}

	outline, _ := deps.Store.Outline.LoadOutline()
	var volumes []domain.VolumeOutline
	if progress.Layered {
		volumes, _ = deps.Store.Outline.LoadLayeredOutline()
	}

	outPath := opts.OutPath
	if outPath == "" {
		outPath = filepath.Join(deps.Store.Dir(), sanitizeFileName(book.Title)+"."+string(opts.Format))
	}

	if !opts.Overwrite {
		if _, err := os.Stat(outPath); err == nil {
			return nil, fmt.Errorf("文件已存在：%s（添加 --overwrite 覆盖）", outPath)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("检查输出路径失败：%w", err)
		}
	}

	titleIdx := buildTitleIndex(outline)
	for _, ch := range chapters {
		summary, err := deps.Store.Summaries.LoadSummary(ch)
		if err != nil {
			return nil, fmt.Errorf("读取第 %d 章摘要失败：%w", ch, err)
		}
		if summary != nil && strings.TrimSpace(summary.Title) != "" {
			titleIdx[ch] = summary.Title
		}
	}
	var locations map[int]chapterLocation
	if len(volumes) > 0 {
		locations = buildLocations(volumes)
	}

	var data []byte
	switch opts.Format {
	case FormatTXT:
		data = []byte(renderTXT(book.Title, chapters, titleIdx, locations, bodies))
	case FormatEPUB:
		buf, err := renderEPUB(*book, chapters, titleIdx, locations, bodies)
		if err != nil {
			return nil, fmt.Errorf("渲染 EPUB 失败：%w", err)
		}
		data = buf
	}

	if err := atomicWrite(outPath, data); err != nil {
		return nil, fmt.Errorf("写入失败：%w", err)
	}

	return &Result{
		Path:     outPath,
		Chapters: len(chapters),
		Bytes:    len(data),
		Skipped:  skipped,
	}, nil
}

// inferFormat guesses the format from the output path suffix. An empty path falls back to TXT; an unknown suffix is an error (to avoid silent mistakes).
func inferFormat(path string) (Format, error) {
	if path == "" {
		return FormatTXT, nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case "", ".txt":
		return FormatTXT, nil
	case ".epub":
		return FormatEPUB, nil
	default:
		return "", fmt.Errorf("无法从扩展名 %q 推断格式（支持 .txt / .epub）", filepath.Ext(path))
	}
}

// atomicWrite mirrors WriteFile in store/io.go: tmp + sync + rename.
// store.IO is not reused because the output path may lie outside store.Dir().
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// sanitizeFileName replaces characters that most filesystems disallow or that are easily confused.
// It does no aggressive transcoding, only blocking path separators and control characters.
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "novel"
	}
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		"\x00", "_",
	)
	return replacer.Replace(name)
}
