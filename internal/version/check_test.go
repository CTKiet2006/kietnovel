package version

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// roundTripFunc 把函数适配为 http.RoundTripper。生产代码固定请求 api.github.com，
// 测试经 Transport 拦截注入响应，不触网。
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func releasePayload(tag, body string) string {
	b, err := json.Marshal(map[string]any{"tag_name": tag, "body": body, "assets": []any{}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
		wantErr         bool
	}{
		{latest: "v1.2.4", current: "v1.2.3", want: true},
		{latest: "v1.2.3", current: "v1.2.3"},
		{latest: "v1.2.2", current: "v1.2.3"},
		{latest: "v1.3.0", current: "v1.2.9", want: true},
		{latest: "v2.0.0", current: "v1.9.9", want: true},
		{latest: "1.2.4", current: "v1.2.3", want: true},
		{latest: "v1.2.3-rc.1", current: "v1.2.2", want: true},
		{latest: "v1.2.3", current: "v1.2.3-rc.1", want: true},
		{latest: "v1.2.3+build.2", current: "v1.2.3+build.1"},
		{latest: "nightly", current: "v1.0.0", wantErr: true},
		{latest: "", current: "v1.0.0", wantErr: true},
		{latest: "v1.2.4", current: "dev", wantErr: true},
	}
	for _, c := range cases {
		got, err := isNewer(c.latest, c.current)
		if (err != nil) != c.wantErr {
			t.Fatalf("isNewer(%q, %q) error = %v, wantErr %v", c.latest, c.current, err, c.wantErr)
		}
		if got != c.want {
			t.Fatalf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestCheckUpdateDevSkipsNetwork(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(200, releasePayload("v9.9.9", "notes")), nil
	})}
	res, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "dev",
		Client:         client,
	})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if res.UpdateAvailable || calls != 0 {
		t.Fatalf("dev 构建应跳过检查: res=%+v calls=%d", res, calls)
	}
}

func TestCheckUpdateFetchesThenCaches(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(200, releasePayload("v1.2.4", "## New Features\n* feat: x")), nil
	})}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	opts := CheckOptions{CurrentVersion: "v1.2.3", Client: client, CachePath: cachePath}

	res, err := CheckUpdate(context.Background(), opts)
	if err != nil {
		t.Fatalf("first CheckUpdate: %v", err)
	}
	if !res.UpdateAvailable || res.Latest != "v1.2.4" || res.FromCache {
		t.Fatalf("first result = %+v", res)
	}
	if res.Notes == "" {
		t.Fatalf("release notes 应随结果带回")
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("缓存未落盘: %v", err)
	}

	res2, err := CheckUpdate(context.Background(), opts)
	if err != nil {
		t.Fatalf("second CheckUpdate: %v", err)
	}
	if !res2.FromCache {
		t.Fatalf("第二次应命中缓存: %+v", res2)
	}
	if calls != 1 {
		t.Fatalf("缓存节流失效: 联网 %d 次, want 1", calls)
	}
}

func TestCheckUpdateCacheExpiry(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(200, releasePayload("v1.2.4", "")), nil
	})}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	// 距上次检查超过 MaxAge 的缓存应视为过期，重新联网。
	stale := checkCache{LastCheck: time.Now().Add(-2 * time.Hour), Latest: "v1.2.4"}
	if err := os.WriteFile(cachePath, mustJSON(stale), 0o644); err != nil {
		t.Fatalf("write stale cache: %v", err)
	}
	_, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3", Client: client, CachePath: cachePath, MaxAge: time.Hour,
	})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if calls != 1 {
		t.Fatalf("过期缓存应触发联网: calls=%d", calls)
	}
}

func TestCheckUpdateDoesNotFallbackToStaleCache(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(500, "rate limited"), nil
	})}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	stale := checkCache{LastCheck: time.Now().Add(-48 * time.Hour), Latest: "v1.2.4", Notes: "old notes"}
	if err := os.WriteFile(cachePath, mustJSON(stale), 0o644); err != nil {
		t.Fatalf("write stale cache: %v", err)
	}
	res, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3", Client: client, CachePath: cachePath,
	})
	if err == nil {
		t.Fatal("网络失败不应被过期缓存掩盖")
	}
	if res != nil {
		t.Fatalf("失败时不应返回过期结果: %+v", res)
	}
	if calls != 1 {
		t.Fatalf("过期缓存后应尝试联网一次: calls=%d", calls)
	}
}

func TestCheckUpdateRepairsCorruptCacheAndReportsIt(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(200, releasePayload("v1.2.4", "notes")), nil
	})}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	if err := os.WriteFile(cachePath, []byte(`{broken`), 0o600); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}
	opts := CheckOptions{CurrentVersion: "v1.2.3", Client: client, CachePath: cachePath}

	res, err := CheckUpdate(context.Background(), opts)
	if res == nil || !res.UpdateAvailable {
		t.Fatalf("重新请求后应返回有效结果: %+v", res)
	}
	if err == nil || !strings.Contains(err.Error(), "load update check cache") {
		t.Fatalf("损坏缓存必须暴露伴随错误，得到: %v", err)
	}

	res, err = CheckUpdate(context.Background(), opts)
	if err != nil || res == nil || !res.FromCache {
		t.Fatalf("修复后的缓存应可直接命中: res=%+v err=%v", res, err)
	}
	if calls != 1 {
		t.Fatalf("有效缓存不应再次联网: calls=%d", calls)
	}
}

func TestCheckUpdateReturnsResultWithCacheWriteError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return httpResp(200, releasePayload("v1.2.4", "notes")), nil
	})}
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write parent file: %v", err)
	}

	res, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3",
		Client:         client,
		CachePath:      filepath.Join(parentFile, "update-check.json"),
	})
	if res == nil || !res.UpdateAvailable {
		t.Fatalf("缓存写失败不应丢失联网结果: %+v", res)
	}
	if err == nil || !strings.Contains(err.Error(), "write update check cache") {
		t.Fatalf("缓存写失败必须返回伴随错误，得到: %v", err)
	}
}

func TestCheckUpdateErrorWithoutCache(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return httpResp(500, "boom"), nil
	})}
	_, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3",
		Client:         client,
		CachePath:      filepath.Join(t.TempDir(), "absent.json"),
	})
	if err == nil {
		t.Fatalf("无缓存且网络失败应返回 error 交调用方记录")
	}
}

func TestCheckUpdateMissingTag(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return httpResp(200, `{"assets": []}`), nil
	})}
	_, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3",
		Client:         client,
		CachePath:      filepath.Join(t.TempDir(), "absent.json"),
	})
	if err == nil {
		t.Fatalf("release 缺 tag_name 应报错")
	}
}

func TestCheckUpdateInvalidTag(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return httpResp(200, releasePayload("nightly", "")), nil
	})}
	_, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3",
		Client:         client,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid latest version") {
		t.Fatalf("非法 release tag 必须报错，得到: %v", err)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
