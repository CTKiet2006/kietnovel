package sp

import "time"

// Mode là chế độ hỏi của Story Partner. Phase 1 chỉ có Ask; Soi và GoiY là
// prompt + contract khác trên cùng nền, làm sau.
type Mode string

const (
	ModeAsk Mode = "ask"
)

// Request là một câu hỏi gửi tới advisor.
type Request struct {
	Mode     Mode
	Question string
	// Language là ngôn ngữ advisor TRẢ LỜI (vi/en/zh). Capture lúc request bắt
	// đầu — user đổi UI giữa chừng thì request đang chạy vẫn đúng ngôn ngữ cũ.
	// Service KHÔNG tự đọc global UI state trong lúc chạy.
	// Rỗng thì rơi về vi. Không liên quan ngôn ngữ truyện đang viết.
	Language string
}

// ContextBlock là một khối dữ kiện trong snapshot. Mỗi block có stable source
// ID để prompt bắt model giữ citation — người viết nhìn vào biết câu trả lời dựa
// vào đâu, và sau này debug được "tại sao lần đó /sp nói sai".
type ContextBlock struct {
	// ID ổn định theo nội dung tham chiếu, ví dụ "outline:chapter:5".
	ID string
	// Kind là loại block: progress, premise, outline, summary, character,
	// world, timeline, foreshadow, review.
	Kind    string
	Content string
}

// StorySnapshot là read-model riêng cho Story Partner.
//
// Không copy ctxpack của Writer: ctxpack phục vụ viết chương (cửa sổ ngữ cảnh,
// style stats), còn snapshot này phục vụ TRẢ LỜI CÂU HỎI — chỉ những gì đã được
// Store ghi nhận. Không bao giờ chứa live stream của Writer đang viết dở.
type StorySnapshot struct {
	CapturedAt time.Time
	// Chapter là số chương đang viết tại lúc chụp. Để audit biết câu hỏi được
	// hỏi trong bối cảnh chương nào — không có nó thì digest chỉ nói "dữ kiện
	// gì" mà không nói "lúc nào".
	Chapter        int
	ProgressDigest string
	Blocks         []ContextBlock
}

// Block trả block theo ID, nil khi snapshot không có dữ kiện đó.
// Dùng để test "thiếu dữ kiện" mà không cần LLM.
func (s StorySnapshot) Block(id string) *ContextBlock {
	for i := range s.Blocks {
		if s.Blocks[i].ID == id {
			return &s.Blocks[i]
		}
	}
	return nil
}

// Result là kết quả một lượt hỏi.
type Result struct {
	Answer         string
	SnapshotDigest string
	CapturedAt     time.Time
	// Chapter là số chương đang viết lúc chụp snapshot (từ progress). UI hiện
	// để người đọc biết câu trả lời gắn với thời điểm nào của truyện.
	Chapter  int
	Provider string
	Model    string
	// InputTokens/OutputTokens để audit và accounting, không cần struct Usage
	// đầy đủ của agentcore ở tầng này.
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
	CostUSD      float64
}
