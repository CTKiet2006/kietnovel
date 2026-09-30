package sp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// BuildSnapshot với Store nil phải lỗi rõ, không panic.
func TestSnapshotStoreNilKhongPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic với Store nil: %v", r)
		}
	}()
	if _, err := BuildSnapshot(nil); err == nil {
		t.Error("Store nil phải lỗi")
	}
}

// Audit concurrent: 20 goroutine ghi cùng file, mỗi dòng phải còn nguyên JSON.
func TestAuditConcurrentKhongXeEntry(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			writeAudit(dir, auditEntry{
				At:             time.Now(),
				Mode:           "ask",
				Question:       fmt.Sprintf("câu hỏi %d", n),
				SnapshotDigest: "abc",
				Answer:         "trả lời",
			})
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(dir, "sp.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 20 {
		t.Fatalf("được %d dòng, mong 20", len(lines))
	}
	for i, ln := range lines {
		var e auditEntry
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			t.Errorf("dòng %d xé: %v", i, err)
		}
	}
}
