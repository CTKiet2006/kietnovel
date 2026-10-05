package host

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// Cold-start co-create: clarify the requirements from scratch and produce the writing instructions for the whole book.
const coCreateSystemPrompt = `你是一个小说共创助手。你的任务不是直接开始写小说，而是通过多轮简短对话帮助用户澄清创作需求，并持续整理出一段可直接交给创作引擎的中文创作指令。

每一轮回复严格按以下 XML 格式输出，包含四个标签，依次出现，每个标签都必须有正确的开闭标签：

<reply>
给用户看的中文自然回复：先回应用户的输入，再最多提出 1 到 2 个当前最关键的问题。如果信息已足够开始创作，告诉用户可以按 Ctrl+S 开始。
</reply>

<draft>
当前完整的创作指令草稿，使用 Markdown：直接从二级标题开始，例如 "## 主题"、"## 关键要素"、"## 待澄清信息"；用项目符号列出要点。每一轮都要在已有结论上**累积更新**，吸收用户最新意图；即使本轮没有新增也要把完整草稿原样再写一次——不要省略、不要写"（保持上一轮）"之类的占位。
</draft>
` + coCreateProtocolTail

func coCreateSystemPromptFor(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		return `Bạn là trợ lý đồng sáng tác tiểu thuyết. Nhiệm vụ của bạn không phải là bắt đầu viết ngay chính văn, mà thông qua vài lượt trò chuyện ngắn gọn giúp người dùng làm rõ nhu cầu sáng tác, liên tục đúc kết thành một bản chỉ đạo sáng tác Tiếng Việt hoàn chỉnh có thể chuyển thẳng cho bộ máy sáng tác.

Mỗi lượt phản hồi phải xuất ra đúng định dạng XML với 4 thẻ theo thứ tự, mỗi thẻ bắt buộc có cặp thẻ đóng/mở chuẩn xác:

<reply>
Phản hồi tự nhiên bằng Tiếng Việt cho người dùng: Trước hết hồi đáp nội dung người dùng vừa nhập, sau đó nêu tối đa 1 đến 2 câu hỏi then chốt nhất lúc này. Nếu thông tin đã đủ để bắt đầu sáng tác, hãy thông báo người dùng có thể nhấn phím Ctrl+S để bắt đầu.
</reply>

<draft>
Bản thảo chỉ đạo sáng tác đầy đủ hiện tại, dùng Markdown: Bắt đầu trực tiếp từ tiêu đề cấp 2, ví dụ "## Chủ đề", "## Yếu tố then chốt", "## Thông tin cần làm rõ"; dùng gạch đầu dòng liệt kê các điểm cốt lõi. Mỗi lượt đều phải CẬP NHẬT TÍCH LŨY trên kết luận đã có, hấp thu ý đồ mới nhất của người dùng; ngay cả khi lượt này không có thông tin mới cũng phải viết lại toàn bộ bản thảo đầy đủ nguyên vẹn — tuyệt đối không lược bỏ, không viết dạng giữ chỗ như "(giữ nguyên lượt trước)".
</draft>
` + coCreateProtocolTailVi
	case "en":
		return `You are a novel co-creation assistant. Your task is not to draft chapters directly, but through concise multi-turn dialogue help the user clarify creative intent and iteratively curate an actionable English writing brief for the generation engine.

Format every reply strictly with the four XML tags in order, each with proper opening and closing tags:

<reply>
Natural English reply: address the user's input, then pose at most 1-2 critical questions. If details suffice to begin drafting, inform the user they can press Ctrl+S to start.
</reply>

<draft>
Current full writing brief in Markdown: begin directly from H2 headers (e.g. "## Theme", "## Core Elements", "## Clarifications"); list key points with bullets. Cumulatively update upon prior conclusions every turn; even without additions, reproduce the full draft verbatim without placeholders.
</draft>
` + coCreateProtocolTailEn
	default:
		return coCreateSystemPrompt
	}
}

// Staged co-create: the novel is already partly written, plan where the "next stages" go. The caller must
// append the current story state summary after this prompt (a "## 当前故事状态" section) so the model plans on top of the existing text.
const stageCoCreateSystemPrompt = `你是一个小说"阶段共创"助手。这本小说已经写了一部分（进度见下方"当前故事状态"）。用户暂停下来，想和你一起规划"后续阶段"的走向，再继续创作。

你的任务不是续写正文，而是通过多轮简短对话帮用户想清楚后面这一段（接下来若干章 / 下一弧 / 下一卷）要往哪走，并持续整理出一段"后续方向 brief"，供创作引擎据此推进。

铁律：所有建议必须与"当前故事状态"里已发生的剧情、人物、伏笔一致，绝不推翻或忽略已写内容；只规划"后续怎么走"，不重新设计整本书。

每一轮回复严格按以下 XML 格式输出，包含四个标签，依次出现，每个标签都必须有正确的开闭标签：

<reply>
给用户看的中文自然回复：先回应用户的输入，再最多提出 1 到 2 个当前最关键的问题。如果后续方向已足够清晰，告诉用户可以按 Ctrl+S 把方向交给创作引擎、继续创作。
</reply>

<draft>
当前完整的"后续方向 brief"，使用 Markdown：直接从二级标题开始，例如 "## 后续走向"、"## 关键转折"、"## 要收的伏笔"、"## 节奏与篇幅"；用项目符号列出要点。每一轮都要在已有结论上**累积更新**，吸收用户最新意图；即使本轮没有新增也要把完整 brief 原样再写一次——不要省略、不要写"（保持上一轮）"之类的占位。
</draft>
` + coCreateProtocolTail

func stageCoCreateSystemPromptFor(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		return `Bạn là trợ lý "đồng sáng tác theo giai đoạn" của tiểu thuyết. Cuốn tiểu thuyết này đã viết được một phần (xem tiến độ ở mục "Trạng thái câu chuyện hiện tại" bên dưới). Người dùng tạm dừng lại để cùng bạn quy hoạch hướng đi cho "giai đoạn tiếp theo" trước khi tiếp tục sáng tác.

Nhiệm vụ của bạn không phải là viết tiếp chính văn, mà thông qua vài lượt trò chuyện ngắn gọn giúp người dùng định hình rõ ràng đoạn sắp tới (vài chương tiếp theo / arc tiếp theo / quyển tiếp theo) sẽ đi về đâu, và liên tục đúc kết thành một bản "tóm tắt hướng đi tiếp theo" để chuyển giao cho bộ máy sáng tác đẩy tiếp.

Thiết luật: Toàn bộ gợi ý bắt buộc phải nhất quán với tình tiết, nhân vật và phục bút đã xảy ra trong "Trạng thái câu chuyện hiện tại", tuyệt đối không phủ nhận hoặc bỏ qua nội dung đã viết; chỉ quy hoạch "bước tiếp theo đi thế nào", không thiết kế lại cả cuốn sách.

Mỗi lượt phản hồi phải xuất ra đúng định dạng XML với 4 thẻ theo thứ tự, mỗi thẻ bắt buộc có cặp thẻ đóng/mở chuẩn xác:

<reply>
Phản hồi tự nhiên bằng Tiếng Việt cho người dùng: Trước hết hồi đáp nội dung người dùng vừa nhập, sau đó nêu tối đa 1 đến 2 câu hỏi then chốt nhất lúc này. Nếu hướng đi tiếp theo đã đủ rõ ràng, hãy thông báo người dùng có thể nhấn Ctrl+S để chuyển hướng đi cho bộ máy sáng tác tiếp tục viết.
</reply>

<draft>
Bản tóm tắt hướng đi tiếp theo đầy đủ hiện tại, dùng Markdown: Bắt đầu trực tiếp từ tiêu đề cấp 2, ví dụ "## Hướng đi tiếp theo", "## Bước ngoặt then chốt", "## Phục bút cần thu hồi", "## Nhịp điệu và dung lượng"; dùng gạch đầu dòng liệt kê các điểm cốt lõi. Mỗi lượt đều phải CẬP NHẬT TÍCH LŨY trên kết luận đã có; ngay cả khi không có thông tin mới cũng phải viết lại toàn bộ bản brief đầy đủ — không lược bỏ, không viết dạng giữ chỗ.
</draft>
` + coCreateProtocolTailVi
	case "en":
		return `You are a staged co-creation assistant. The novel has already been partially written (progress in "Current Story State" below). The user paused to map out the next stage before continuing.

Your task is not to write chapter prose directly, but to help determine where the upcoming chapters/arc/volume should head, curating an actionable trajectory brief for the writing engine.

Iron Law: All suggestions must remain consistent with established plot, characters, and foreshadowing in "Current Story State"; plan where the story goes next, do not redesign the whole book.

Format every reply strictly with the four XML tags in order, each with proper opening and closing tags:

<reply>
Natural English reply: address user input, pose at most 1-2 critical questions. If direction is clear, let the user know they can press Ctrl+S to proceed.
</reply>

<draft>
Current trajectory brief in Markdown: begin with H2 headers (e.g. "## Next Trajectory", "## Key Reversals", "## Foreshadowing Resolution", "## Pacing & Length"). Cumulatively update upon prior conclusions; reproduce full brief verbatim without placeholders.
</draft>
` + coCreateProtocolTailEn
	default:
		return stageCoCreateSystemPrompt
	}
}

// coCreateProtocolTail is the output protocol tail shared by both co-create modes (<ready> / <suggestions> + the output spec).
// The two modes differ only in the opening context and the <draft> semantics; the protocol is identical.
const coCreateProtocolTail = `
<ready>false</ready>

<suggestions>
1-3 条"用户接下来可能想说的话"，每行一条以 "- " 开头。这是用户卡壳时的引导，
按数字键填入输入框，用户可再编辑后发送。

要求：
- 站在用户口吻，像用户对你说的话，不要写成助手反问。
- 每条不超过 25 字，多样化句式，避免千篇一律。
- 给倾向 / 选择 / 补充意图，不要一句话替用户写完整设定。
</suggestions>

输出规范：
- 必须使用四个 XML 标签：<reply> / <draft> / <ready> / <suggestions>，每个都必须完整开闭。
- 标签名只能小写英文，不要改写成 <REPLY> / <REWRITE> / <回复> 等任何变体。
- 标签外不要添加任何说明、思考或代码围栏。
- <draft> 内允许多行 Markdown，直接换行书写，不需要任何转义。
- <ready> 只写 true 或 false。信息已足够时填 true。
- <ready>true</ready> 时 <suggestions> 可以为空（保留空标签 <suggestions></suggestions> 即可）。`

const coCreateProtocolTailVi = `
<ready>false</ready>

<suggestions>
1-3 câu "người dùng có thể muốn nói tiếp theo", mỗi dòng bắt đầu bằng "- ". Đây là gợi ý định hướng khi người dùng chưa nghĩ ra, có thể nhấn phím số để điền vào ô nhập.

Yêu cầu:
- Đứng dưới góc nhìn và giọng điệu của người dùng (như lời người dùng nói với bạn), không viết thành câu hỏi ngược lại của trợ lý.
- Mỗi câu không quá 25 chữ, câu từ đa dạng, tránh rập khuôn.
- Đưa ra thiên hướng / lựa chọn / ý đồ bổ sung, không viết thay toàn bộ thiết lập trong một câu.
</suggestions>

Quy cách xuất:
- Bắt buộc dùng 4 thẻ XML: <reply> / <draft> / <ready> / <suggestions>, mỗi thẻ phải mở/đóng đầy đủ.
- Tên thẻ chỉ viết chữ thường tiếng Anh, không đổi sang chữ hoa hay biến thể khác.
- Không thêm bất kỳ lời giải thích hay khối mã markdown nào bên ngoài các thẻ.
- <draft> cho phép nhiều dòng Markdown, xuống dòng tự nhiên không cần escape.
- <ready> chỉ điền true hoặc false. Khi thông tin đã đủ thì điền true.
- Khi <ready>true</ready>, thẻ <suggestions> có thể để trống: <suggestions></suggestions>.`

const coCreateProtocolTailEn = `
<ready>false</ready>

<suggestions>
1-3 lines of "what the user might want to say next", each prefixed with "- ". These guide the user if stuck.

Requirements:
- Written from the user's perspective, not as assistant questions.
- Under 25 words per line, varied sentence structures.
- Suggest direction / options / supplemental intent, rather than writing complete settings.
</suggestions>

Output Specification:
- Must use all four XML tags: <reply> / <draft> / <ready> / <suggestions>, fully opened and closed.
- Tag names strictly lowercase English.
- No commentary, thinking, or markdown code fences outside tags.
- <draft> allows multiline Markdown directly.
- <ready> strictly true or false. Use true when information suffices.
- When <ready>true</ready>, <suggestions> may be empty: <suggestions></suggestions>.`

// CoCreateProgressKind identifies the content type of a streaming callback.
const (
	CoCreateProgressThinking = "thinking"
	CoCreateProgressReply    = "reply"
)

// Four-part XML tag output. The XML style is more robust than bracket markers - Claude / GPT training data is
// full of <thinking>...</thinking> style formats, so the model almost never rewrites <reply> as <REWRITE>
// or another variant; the closing tags also make mid-stream truncation more precise (no need to hunt the next marker to cut the tail).
const (
	tagReply       = "reply"
	tagDraft       = "draft"
	tagReady       = "ready"
	tagSuggestions = "suggestions"
)

func coCreateStream(ctx context.Context, models *bootstrap.ModelSet, sessions *store.SessionStore, sysPrompt string, history []CoCreateMessage, onProgress func(kind, text string)) (reply CoCreateReply, err error) {
	if len(history) == 0 {
		return CoCreateReply{}, fmt.Errorf("cocreate history is empty")
	}

	model := models.ForRole("thinking")

	msgs := []agentcore.Message{agentcore.SystemMsg(sysPrompt)}
	for _, item := range history {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Role)) {
		case "assistant":
			msgs = append(msgs, assistantMsg(content))
		default:
			msgs = append(msgs, agentcore.UserMsg(content))
		}
	}

	var raw, thinking strings.Builder

	// Debugging occasional problems such as "cocreate empty response" requires seeing what the model actually returned.
	// Every round is written end to end to <output>/meta/sessions/cocreate.jsonl, next to the real writing session logs.
	start := time.Now()
	defer func() {
		if sessions == nil {
			return
		}
		if logErr := sessions.LogCoCreate(coCreateLogEntry{
			Time:         time.Now(),
			DurationMS:   time.Since(start).Milliseconds(),
			InputHistory: history,
			RawResponse:  raw.String(),
			RawLen:       len([]rune(raw.String())),
			Thinking:     thinking.String(),
			ParsedReply:  reply.Message,
			ParsedDraft:  reply.Prompt,
			ParsedReady:  reply.Ready,
			ParsedSugs:   reply.Suggestions,
			Error:        errString(err),
		}); logErr != nil {
			slog.Warn("共创会话日志落盘失败", "module", "cocreate", "err", logErr)
		}
	}()

	streamCh, err := model.GenerateStream(ctx, msgs, nil, agentcore.WithMaxTokens(2048))
	if err != nil {
		return CoCreateReply{}, fmt.Errorf("cocreate generate: %w", err)
	}

	var streamed bool
	for ev := range streamCh {
		switch ev.Type {
		case agentcore.StreamEventThinkingDelta:
			thinking.WriteString(ev.Delta)
			if onProgress != nil {
				onProgress(CoCreateProgressThinking, thinking.String())
			}
		case agentcore.StreamEventTextDelta:
			streamed = true
			raw.WriteString(ev.Delta)
			if onProgress != nil {
				onProgress(CoCreateProgressReply, extractReplyPreview(raw.String()))
			}
		case agentcore.StreamEventDone:
			if !streamed {
				raw.WriteString(ev.Message.TextContent())
			}
		case agentcore.StreamEventError:
			if ev.Err != nil {
				return CoCreateReply{}, fmt.Errorf("cocreate generate: %w", ev.Err)
			}
			return CoCreateReply{}, fmt.Errorf("cocreate generate failed")
		}
	}

	// Channel fallback: reasoning models (R1 / GLM-Z1 / QwQ etc.) occasionally write the complete answer into
	// reasoning_content and never switch back to the final answer channel, so raw is empty while thinking
	// holds all four parts. Observed in meta/sessions/cocreate.jsonl - parsing thinking directly as raw works,
	// because the protocol layer already degrades (no [REPLY] marker means the whole block is treated as reply), and after the rescue the UI experience is identical.
	rawText := raw.String()
	if strings.TrimSpace(rawText) == "" {
		if t := strings.TrimSpace(thinking.String()); t != "" {
			rawText = t
		}
	}
	reply, err = parseCoCreateResponse(rawText)
	return reply, err
}

// coCreateLogEntry is the shape of one line written to meta/sessions/cocreate.jsonl.
// Field names follow the jsonl ad-hoc lookup habit (snake_case) so jq filters read naturally.
type coCreateLogEntry struct {
	Time         time.Time         `json:"time"`
	DurationMS   int64             `json:"duration_ms"`
	InputHistory []CoCreateMessage `json:"input_history"`
	RawResponse  string            `json:"raw_response"`
	RawLen       int               `json:"raw_len"`
	Thinking     string            `json:"thinking,omitempty"`
	ParsedReply  string            `json:"parsed_reply"`
	ParsedDraft  string            `json:"parsed_draft"`
	ParsedReady  bool              `json:"parsed_ready"`
	ParsedSugs   []string          `json:"parsed_sugs,omitempty"`
	Error        string            `json:"error,omitempty"`
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func assistantMsg(text string) agentcore.Message {
	return agentcore.Message{
		Role:      agentcore.RoleAssistant,
		Content:   []agentcore.ContentBlock{agentcore.TextBlock(text)},
		Timestamp: time.Now(),
	}
}

// parseCoCreateResponse parses the XML tag output. If the model does not follow the protocol (it just speaks plain language),
// the whole block is shown as reply and draft stays empty so the session keeps the previous round.
func parseCoCreateResponse(raw string) (CoCreateReply, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CoCreateReply{}, fmt.Errorf("cocreate empty response")
	}

	reply, draft, ready, suggestions := splitCoCreateMarkers(raw)
	if reply == "" {
		// The model did not follow the XML protocol: the whole block becomes the reply.
		return CoCreateReply{Message: raw, Prompt: "", Ready: false, Raw: raw}, nil
	}
	return CoCreateReply{
		Message:     reply,
		Prompt:      draft,
		Ready:       ready,
		Suggestions: suggestions,
		Raw:         raw,
	}, nil
}

// splitCoCreateMarkers splits the text by the four XML tags.
// A tag may be missing (mid-stream or omitted by the model) and the missing part maps to an empty / false / nil field.
// When a closing tag is missing, extractTagContent runs to the end of the string and still parses as best it can.
func splitCoCreateMarkers(s string) (reply, draft string, ready bool, suggestions []string) {
	reply = extractTagContent(s, tagReply)
	draft = extractTagContent(s, tagDraft)
	readyStr := strings.ToLower(extractTagContent(s, tagReady))
	ready = readyStr == "true" || readyStr == "yes"
	suggestions = parseSuggestions(extractTagContent(s, tagSuggestions))
	return
}

// extractTagContent pulls the text between <tag>...</tag> out of s.
// It covers three occasional failure modes so we do not fall straight to degradation and lose fields:
//  1. opening tag without a closing tag (mid-stream) -> cut at the next known opening tag
//  2. closing tag without an opening tag (a model typo, e.g. <suggestions> written as <uggestions>) -> start
//     from the end of the most recent known fully closed tag, up to </tag>
//  3. reply with no opening tag at all (the model opens in plain language and appends </reply> at the end) -> from the start up to </reply>
func extractTagContent(s, tag string) string {
	open := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	oIdx := strings.Index(s, open)
	if oIdx >= 0 {
		rest := s[oIdx+len(open):]
		if cIdx := strings.Index(rest, closeTag); cIdx >= 0 {
			return strings.TrimSpace(rest[:cIdx])
		}
		// opening tag without a closing tag -> cut at the next known opening tag
		for _, other := range []string{"<reply>", "<draft>", "<ready>", "<suggestions>"} {
			if other == open {
				continue
			}
			if idx := strings.Index(rest, other); idx >= 0 {
				rest = rest[:idx]
			}
		}
		return strings.TrimSpace(rest)
	}

	// closing tag without an opening tag -> start from the end of the most recent known fully closed tag, up to </tag>.
	if cIdx := strings.Index(s, closeTag); cIdx >= 0 {
		prefix := s[:cIdx]
		start := 0
		for _, t := range []string{"</reply>", "</draft>", "</ready>", "</suggestions>"} {
			if t == closeTag {
				continue
			}
			if i := strings.LastIndex(prefix, t); i >= 0 {
				if end := i + len(t); end > start {
					start = end
				}
			}
		}
		return strings.TrimSpace(prefix[start:])
	}
	return ""
}

// parseSuggestions pulls each line out of the <suggestions> section and strips list prefixes such as "- " / "* " / "1. ".
// At most 3 are kept; blank lines, too-short lines (<2 characters) and lines that look like an XML tag (leftovers of the
// typo opening-tag fallback, e.g. <uggestions>) are ignored.
func parseSuggestions(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// a line that looks like an XML tag -> skip (guards against typo opening-tag pollution)
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") {
			continue
		}
		// strip the list prefix
		switch {
		case strings.HasPrefix(line, "- "):
			line = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "* "):
			line = strings.TrimSpace(line[2:])
		case isOrderedSuggestion(line):
			line = stripOrderedPrefix(line)
		}
		if len([]rune(line)) < 2 {
			continue
		}
		out = append(out, line)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// isOrderedSuggestion reports whether a line starts like "1. " / "12. " (digits + dot + space).
func isOrderedSuggestion(line string) bool {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func stripOrderedPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return line
	}
	return strings.TrimSpace(line[i+2:])
}

// extractReplyPreview is the streaming preview: while raw is still growing it hands the UI something displayable.
// It finds the content after <reply> and cuts at </reply> or before the next opening tag <draft>.
// When the model half-complies (missing the <reply> opening tag), everything from the start to </reply> or <draft> counts as the reply.
func extractReplyPreview(raw string) string {
	trimmed := strings.TrimSpace(raw)
	open := "<" + tagReply + ">"
	closeTag := "</" + tagReply + ">"
	draftOpen := "<" + tagDraft + ">"

	rest := trimmed
	if rIdx := strings.Index(trimmed, open); rIdx >= 0 {
		rest = trimmed[rIdx+len(open):]
	}
	if cIdx := strings.Index(rest, closeTag); cIdx >= 0 {
		return strings.TrimSpace(rest[:cIdx])
	}
	if dIdx := strings.Index(rest, draftOpen); dIdx >= 0 {
		rest = rest[:dIdx]
	}
	return strings.TrimSpace(rest)
}
