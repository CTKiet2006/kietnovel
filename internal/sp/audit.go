package sp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// auditMu tuần tự hoá ghi audit trong cùng tiến trình.
//
// O_APPEND của kernel đã nguyên tử ở mức offset cho entry nhỏ, nhưng hai
// goroutine cùng mở hai handle rồi Write xen kẽ vẫn có thể xé entry khi Go chia
// nhỏ syscall. Mutex này rẻ hơn nhiều so với một dòng log hỏng — mà log hỏng thì
// audit mất đúng tác dụng truy nguyên nhân.
var auditMu sync.Mutex

// auditEntry là một dòng audit cho mỗi request /sp.
//
// Để debug câu "tại sao lần đó /sp nói sai": mở entry, xem snapshot digest,
// biết chính xác lúc đó advisor đã nhìn thấy dữ kiện gì. Không có digest thì
// audit chỉ là nhật ký kể chuyện, không truy được nguyên nhân.
type auditEntry struct {
	At             time.Time     `json:"at"`
	Mode           string        `json:"mode"`
	Question       string        `json:"question"`
	SnapshotDigest string        `json:"snapshot_digest"`
	Chapter        int           `json:"chapter"`
	Provider       string        `json:"provider"`
	Model          string        `json:"model"`
	Answer         string        `json:"answer"`
	InputTokens    int           `json:"input_tokens"`
	OutputTokens   int           `json:"output_tokens"`
	DurationMs     int64         `json:"duration_ms"`
	Duration       time.Duration `json:"-"`
}

// writeAudit ghi một dòng JSON vào <dir>/sp.jsonl. Không nằm trong meta/: audit
// là dữ liệu vận hành, không phải dữ liệu truyện — backup/export/sync truyện
// không được quét qua nó.
//
// Lỗi ghi audit KHÔNG chặn kết quả: người dùng đã có câu trả lời, mất một dòng
// log còn hơn mất câu trả lời.
func writeAudit(dir string, e auditEntry) {
	if dir == "" {
		return
	}
	auditMu.Lock()
	defer auditMu.Unlock()
	e.DurationMs = int64(e.Duration / time.Millisecond)
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "sp.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}
