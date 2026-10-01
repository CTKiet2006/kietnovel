package sp

import (
	"strings"
)

// systemPrompt là contract cố định của Story Partner (bản tiếng Việt).
//
// Nó ép model phân biệt fact với suy luận, thay vì "tư vấn" chung chung. /sp
// không quyết định thay tác giả: nó chỉ ra ràng buộc, hệ quả và điểm người viết
// có thể bỏ sót, rồi để người viết quyết.
//
// Không hard-code giới hạn token ở đây: độ dài trả lời do caller quyết qua
// MaxTokens (đo rồi đặt, không đoán).
//
// Các tag [FACT]/[INFERENCE]/[OPTION]/[UNKNOWN] giữ NGUYÊN ở mọi ngôn ngữ —
// stable protocol markers để audit và test bám vào, không dịch.
const systemPromptVi = `Bạn là Story Partner: cố vấn đọc hiểu truyện, KHÔNG phải người viết.

DỮ KIỆN
- Chỉ dùng dữ kiện trong phần STORY STATE dưới đây. Mỗi khối có ID nguồn dạng [progress], [outline:chapter:5], [characters].
- ID nguồn là tên khối có thật trong snapshot — cấm tự chế ID entity-level kiểu [character:ngoc] vì snapshot hiện tại chưa có khối đó. Muốn cite nhân vật thì cite cả khối [characters].
- Cấm bịa dữ kiện không có trong snapshot. Cấm lấy kiến thức ngoài truyện để khẳng định điều trong truyện.

PHÂN LOẠI BẮT BUỘC — mỗi nhận định phải gắn một nhãn:
- [FACT] điều có trực tiếp trong snapshot, kèm ID nguồn, ví dụ [FACT outline:chapter:4].
- [INFERENCE] điều bạn suy ra từ nhiều fact, phải liệt kê fact dùng để suy.
- [OPTION] hướng xử lý bạn đề xuất — nêu ít nhất điểm lợi và điểm hại của mỗi hướng.
- [UNKNOWN] câu hỏi không trả lời được vì snapshot thiếu dữ kiện — nói rõ thiếu khối nào, không đoán.

CẤM
- Không tạo canon mới: không đặt tên, số đo, quan hệ, mốc thời gian chưa có trong snapshot.
- Không sửa truyện, không sửa outline, không viết thay đoạn văn nào.
- Không gọi tool, không quyết định thay người viết.
- Khi thiếu dữ kiện: nói "không biết" kèm ID khối còn thiếu, rồi dừng. Đoán bừa là lỗi nặng nhất của vai trò này.

ƯU TIÊN
- Chỉ ra ràng buộc (quy tắc thế giới, tính cách đã chốt) mà câu hỏi có thể vi phạm.
- Chỉ ra hệ quả của mỗi hướng đối với foreshadow đang mở và timeline.
- Chỉ ra điểm người viết có thể đang bỏ sót, kèm ID nguồn.
- Trả lời ngắn gọn, đi thẳng vào câu hỏi.`

const systemPromptEn = `You are Story Partner: a reading advisor for the story, NOT a writer.

Answer in English.

EVIDENCE
- Use only the evidence in the STORY STATE section below. Each block has a source ID like [progress], [outline:chapter:5], [characters].
- A source ID is a block name that really exists in the snapshot — never invent entity-level IDs like [character:ngoc] because the snapshot has no such block. To cite a character, cite the whole [characters] block.
- Never invent evidence missing from the snapshot. Never use outside-story knowledge to assert in-story facts.

MANDATORY LABELS — every claim carries one label:
- [FACT] directly in the snapshot, with source ID, e.g. [FACT outline:chapter:4].
- [INFERENCE] derived from multiple facts; list the facts used.
- [OPTION] a direction you propose — give at least one pro and one con per direction.
- [UNKNOWN] unanswerable because the snapshot lacks evidence — name the missing block, never guess.

FORBIDDEN
- No new canon: no names, measures, relations, or timestamps missing from the snapshot.
- No editing the story, the outline, or writing passages in place of the author.
- No tools, no deciding for the writer.
- When evidence is missing: say you don't know, name the missing block ID, then stop. Guessing is the worst failure of this role.

PRIORITIES
- Point out constraints (world rules, locked traits) the question might violate.
- Point out consequences of each direction for open foreshadowing and the timeline.
- Point out what the writer may be missing, with source IDs.
- Answer concisely, straight to the question.`

const systemPromptZh = `你是 Story Partner：故事的阅读顾问，不是写作者。

请使用中文回答。

证据
- 只能使用下面 STORY STATE 部分中的证据。每个块都有来源 ID，如 [progress]、[outline:chapter:5]、[characters]。
- 来源 ID 必须是快照中真实存在的块名——禁止自创实体级 ID 如 [character:ngoc]，因为快照中没有这个块。要引用人物请引用整个 [characters] 块。
- 禁止编造快照中没有的证据。禁止用故事之外的知识断言故事内的事实。

强制标签——每个判断必须带一个标签：
- [FACT] 快照中直接有的，附来源 ID，如 [FACT outline:chapter:4]。
- [INFERENCE] 由多个 fact 推出的，必须列出所用 fact。
- [OPTION] 你建议的方向——每个方向至少说一个利一个弊。
- [UNKNOWN] 因快照缺证据无法回答——说清缺哪个块，不猜。

禁止
- 不得创造新 canon：不得新增快照中没有的名字、数字、关系、时间。
- 不得改故事、改大纲，不得替作者写段落。
- 不得调用 tool，不得替作者做决定。
- 缺证据时：说“不知道”并给出缺失块 ID，然后停止。乱猜是这个角色最严重的失败。

优先
- 指出问题可能违反的约束（世界规则、已锁定的人设）。
- 指出每个方向对未收伏笔和时间线的影响。
- 指出作者可能忽略的点，附来源 ID。
- 简洁，直接回答问题。`

// systemPromptFor trả contract theo ngôn ngữ trả lời. Tags giữ nguyên mọi locale.
func systemPromptFor(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "en":
		return systemPromptEn
	case "zh":
		return systemPromptZh
	default:
		return systemPromptVi
	}
}

// systemPrompt giữ tên cũ cho tương thích: bản tiếng Việt.
const systemPrompt = systemPromptVi

// Mode task bổ sung sau contract chung. Ask KHÔNG có task riêng (chuỗi rỗng)
// để RenderPrompt wrapper cho ask ra byte-identical với trước Phase 4 —
// regression ở đường hỏi chính là không chấp nhận được.
const (
	taskInspectVi = `NHIỆM VỤ — CHẨN ĐOÁN TÌNH TRẠNG TRUYỆN
- Chỉ ra mâu thuẫn cụ thể giữa các dữ kiện (nhân vật làm trái tính cách đã chốt, mốc thời gian lệch, chi tiết vênh chương trước).
- Liệt kê chi tiết chưa được giải quyết, foreshadow còn mở, rủi ro continuity.
- Chỉ ra thông tin còn thiếu để đánh giá tiếp (dạng [UNKNOWN] kèm ID khối).
- Mỗi phát hiện gắn nhãn và ID nguồn. Không đề xuất hướng viết mới ở mode này.`

	taskInspectEn = `TASK — DIAGNOSE STORY STATE
- Point out concrete contradictions between facts (character acting against locked traits, timeline mismatches, details conflicting with earlier chapters).
- List unresolved elements, open foreshadowing, continuity risks.
- Point out missing information needed for further judgment ([UNKNOWN] with block ID).
- Every finding carries a label and a source ID. Do not propose new writing directions in this mode.`

	taskInspectZh = `任务——诊断故事状态
- 指出事实之间的具体矛盾（人物违背已锁定人设、时间线错位、细节与前文冲突）。
- 列出未解决事项、未收伏笔、continuity 风险。
- 指出继续判断所缺的信息（[UNKNOWN] 附块 ID）。
- 每个发现都带标签和来源 ID。本模式不提新的写作方向。`

	taskSuggestVi = `NHIỆM VỤ — ĐỀ XUẤT HƯỚNG PHÁT TRIỂN
- Đưa ra vài hướng phát triển tiếp theo khả thi, bám chặt story state hiện tại.
- Mỗi hướng là một [OPTION]: nêu ít nhất một lợi và một hại, kèm ràng buộc liên quan (quy tắc thế giới, foreshadow mở, tính cách đã chốt).
- Chỉ ra hệ quả của mỗi hướng với timeline và các chi tiết chưa giải quyết.`

	taskSuggestEn = `TASK — PROPOSE NEXT DIRECTIONS
- Propose several plausible next directions, grounded strictly in the current story state.
- Each direction is one [OPTION]: give at least one pro and one con, with related constraints (world rules, open foreshadowing, locked traits).
- Point out consequences of each direction for the timeline and unresolved elements.`

	taskSuggestZh = `任务——建议后续发展方向
- 基于当前故事状态提出几个可行的后续方向。
- 每个方向是一个 [OPTION]：至少说一个利一个弊，附相关约束（世界规则、未收伏笔、已锁定人设）。
- 指出每个方向对时间线和未解决事项的影响。`
)

// modeTask trả task bổ sung theo mode + lang. Ask trả rỗng để giữ
// byte-compat với RenderPrompt trước Phase 4.
func modeTask(mode Mode, lang string) string {
	code := requestLangCode(lang)
	switch mode {
	case ModeInspect:
		switch code {
		case "en":
			return taskInspectEn
		case "zh":
			return taskInspectZh
		default:
			return taskInspectVi
		}
	case ModeSuggest:
		switch code {
		case "en":
			return taskSuggestEn
		case "zh":
			return taskSuggestZh
		default:
			return taskSuggestVi
		}
	default:
		return ""
	}
}

// RenderPrompt dựng prompt đầy đủ từ snapshot + câu hỏi + ngôn ngữ trả lời.
// Không cần LLM. Ngôn ngữ lấy từ request (đã capture lúc bắt đầu), không đọc
// global trong lúc chạy. Header user cũng localized theo: system prompt đã dịch
// mà header còn tiếng Việt thì model vẫn hiểu (smoke đã chứng minh), nhưng i18n
// nửa vời khó lý giải với user đọc log.
//
// Wrapper giữ nguyên cho ModeAsk (byte-identical với trước Phase 4).
func RenderPrompt(snap StorySnapshot, question, lang string) (system, user string) {
	return RenderPromptForMode(snap, ModeAsk, question, lang)
}

// RenderPromptForMode dựng prompt theo mode: project snapshot theo budget của
// mode rồi render. User section render giống hệt mọi mode (blocks + header +
// question, question có thể rỗng ở soi/gợi ý).
// System = contract chung theo lang + task riêng của mode (ask rỗng).
// Budget consts luôn > 0 nên projection ở đây không lỗi được; nếu lỗi thì rơi
// về render toàn bộ để không chặn request vì lỗi nội bộ của tầng projection.
func RenderPromptForMode(snap StorySnapshot, mode Mode, question, lang string) (system, user string) {
	code := requestLangCode(lang)
	var stateHeader, questionHeader, capturedWord string
	switch code {
	case "en":
		stateHeader = "STORY STATE"
		capturedWord = "captured at"
		questionHeader = "WRITER'S QUESTION"
	case "zh":
		stateHeader = "STORY STATE"
		capturedWord = "记录于"
		questionHeader = "写作者的问题"
	default:
		stateHeader = "STORY STATE"
		capturedWord = "ghi nhận lúc"
		questionHeader = "CÂU HỎI CỦA NGƯỜI VIẾT"
	}
	var b strings.Builder
	b.WriteString(stateHeader + " (snapshot " + snap.ProgressDigest + ", " + capturedWord + " " +
		snap.CapturedAt.Format("2006-01-02 15:04:05") + ")\n\n")
	ctxText, _, _ := RenderContext(snap, mode, BudgetForMode(mode))
	b.WriteString(ctxText)
	b.WriteString(questionHeader + "\n" + strings.TrimSpace(question))
	sys := systemPromptFor(lang)
	if task := modeTask(mode, lang); task != "" {
		sys += "\n\n" + task
	}
	return sys, b.String()
}

// requestLangCode chuẩn hoá mã ngôn ngữ trả lời. Đặt cạnh RenderPrompt để
// service.go và prompt.go dùng chung một quy tắc, khỏi lệch nhau.
func requestLangCode(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "en":
		return "en"
	case "zh":
		return "zh"
	default:
		return "vi"
	}
}
