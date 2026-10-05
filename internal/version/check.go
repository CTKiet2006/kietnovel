package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultRepo 是版本检查与自更新默认指向的上游仓库。
const DefaultRepo = "voocel/ainovel-cli"

// DefaultCheckInterval 是两次联网检查之间的最小间隔。GitHub 匿名 API 限流
// 60 req/h，且发版节奏是天级，更频繁的检查只有打扰没有收益。
const DefaultCheckInterval = 24 * time.Hour

// CheckOptions 是一次版本检查的输入。CachePath 由调用方传入（通常为
// 配置目录下的 update-check.json）；本包不依赖 bootstrap，保持 version
// 作为叶子包的依赖方向。
type CheckOptions struct {
	Repo           string
	CurrentVersion string
	Client         *http.Client
	CachePath      string        // 空 = 不落盘，每次调用都联网
	MaxAge         time.Duration // 缓存有效期；<=0 取 DefaultCheckInterval
}

// CheckResult 是版本检查的结论。Notes 携带 release 原文（markdown），
// 调用方展示前必须按自身输出介质清理；升级与否由用户决定。
type CheckResult struct {
	Latest          string
	Current         string
	Notes           string
	UpdateAvailable bool
	FromCache       bool
}

// checkCache 是 CachePath 的落盘结构。
type checkCache struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
	Notes     string    `json:"notes"`
}

// CheckUpdate 查询上游最新 release 并判断是否需要提醒升级。只读不写任何
// 二进制；是否升级、何时升级完全由用户经 `ainovel-cli update` 决定。
// 缓存或网络失败均通过 error 暴露；缓存写入失败时仍返回已经取得的检查结果。
func CheckUpdate(ctx context.Context, opts CheckOptions) (*CheckResult, error) {
	current := Normalize(opts.CurrentVersion)
	if current == "dev" {
		// 本地构建没有可比较的版本语义，跳过检查避免把任意构建误报成"可升级"。
		return &CheckResult{Current: current}, nil
	}
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultCheckInterval
	}

	var cacheErr error
	if opts.CachePath != "" {
		c, err := loadCache(opts.CachePath)
		switch {
		case err == nil:
			age := time.Since(c.LastCheck)
			if age < 0 {
				cacheErr = fmt.Errorf("update check cache timestamp is in the future: %s", c.LastCheck.Format(time.RFC3339))
			} else if age <= maxAge {
				result, resultErr := c.result(current)
				if resultErr == nil {
					return result, nil
				}
				cacheErr = fmt.Errorf("validate update check cache: %w", resultErr)
			}
		case errors.Is(err, os.ErrNotExist):
			// 首次检查没有缓存是正常状态。
		default:
			cacheErr = fmt.Errorf("load update check cache: %w", err)
		}
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	rel, err := fetchRelease(ctx, client, repo, "latest")
	if err != nil {
		return nil, errors.Join(cacheErr, err)
	}
	if rel.TagName == "" {
		return nil, errors.Join(cacheErr, fmt.Errorf("release 缺少 tag_name"))
	}

	result, err := newCheckResult(rel.TagName, current, rel.Body, false)
	if err != nil {
		return nil, errors.Join(cacheErr, err)
	}
	if err := writeCache(opts.CachePath, rel); err != nil {
		cacheErr = errors.Join(cacheErr, fmt.Errorf("write update check cache: %w", err))
	}
	// 缓存错误作为伴随错误返回，调用方应记录它，但仍可使用检查结果。
	return result, cacheErr
}

func newCheckResult(latest, current, notes string, fromCache bool) (*CheckResult, error) {
	updateAvailable, err := isNewer(latest, current)
	if err != nil {
		return nil, err
	}
	return &CheckResult{
		Latest:          latest,
		Current:         current,
		Notes:           notes,
		UpdateAvailable: updateAvailable,
		FromCache:       fromCache,
	}, nil
}

// loadCache 读取并校验缓存；文件不存在、损坏和字段缺失由调用方分别处理。
func loadCache(path string) (*checkCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c checkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode cache: %w", err)
	}
	if c.Latest == "" || c.LastCheck.IsZero() {
		return nil, fmt.Errorf("cache missing latest or last_check")
	}
	return &c, nil
}

// result 把缓存内容转换为检查结论（FromCache=true）。
func (c *checkCache) result(current string) (*CheckResult, error) {
	return newCheckResult(c.Latest, current, c.Notes, true)
}

// writeCache 原子落盘本次检查结果。目录不存在则创建。
func writeCache(path string, rel *release) error {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.Marshal(checkCache{LastCheck: time.Now(), Latest: rel.TagName, Notes: rel.Body})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-check-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// isNewer 使用完整 SemVer 规则比较版本；非法版本显式返回错误，避免静默漏报。
func isNewer(latest, current string) (bool, error) {
	latest = Normalize(latest)
	current = Normalize(current)
	if !semver.IsValid(latest) {
		return false, fmt.Errorf("invalid latest version %q", latest)
	}
	if !semver.IsValid(current) {
		return false, fmt.Errorf("invalid current version %q", current)
	}
	return semver.Compare(latest, current) > 0, nil
}
