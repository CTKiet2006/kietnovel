package assets

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/tools"
)

//go:embed prompts
var promptsFS embed.FS

//go:embed references
var referencesFS embed.FS

//go:embed styles
var stylesFS embed.FS

//go:embed voice.md voice_zh.md
var voiceFS embed.FS

// Prompts biểu thị tập hợp các prompt được nhúng.
type Prompts struct {
	ArchitectShort   string
	ArchitectLong    string
	Writer           string // Giao thức khuôn mẫu, chứa placeholder {{VOICE}}; bản cuối ráp qua BuildWriterPrompt
	Editor           string
	ImportSegment    string // Phân tách ngữ nghĩa: nhận diện ranh giới chương/tập/phần phụ
	ImportAnalyze    string // Trích xuất sự thật từng chương
	ImportSynthesize string // Tổng hợp phân tầng và phân chia tập/cung toàn sách (BookSynthesis)
	ImportRange      string // Tóm tắt khoảng liên tục giai đoạn Map (RangeDigest)
	SimulationSource string
	SimulationMerge  string
	RevisionAnalyze  string

	// Arbiter tài phán (LLM-as-function, không bọc simulation guidance)
	ArbiterPlanStart    string
	ArbiterIntervention string
	ArbiterFailure      string
}

// Bundle biểu thị tập hợp tài nguyên tĩnh cần thiết khi chạy.
type Bundle struct {
	References tools.References
	Prompts    Prompts
	Styles     map[string]string
	Voice      string // Tiêu chuẩn hành văn, ráp qua 3 tầng ghi đè
	Language   string // "vi" hoặc "zh"
}

// LoadOptions khai báo nguồn ghi đè tầng văn phong.
type LoadOptions struct {
	BookStyleDir string // <outputDir>/style
	HomeStyleDir string // ~/.ainovel/style
}

// DefaultLoadOptions khởi tạo nguồn ghi đè dựa trên thư mục sách.
func DefaultLoadOptions(outputDir string) LoadOptions {
	var opts LoadOptions
	if outputDir != "" {
		opts.BookStyleDir = filepath.Join(outputDir, "style")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		opts.HomeStyleDir = filepath.Join(home, ".ainovel", "style")
	}
	return opts
}

// Load trả về tài nguyên tương ứng với style và ngôn ngữ truyện (mặc định vi).
func Load(style string, opts LoadOptions) Bundle {
	return LoadWithLanguage("vi", style, opts)
}

// LoadWithLanguage nạp tài nguyên theo ngôn ngữ ("vi" hoặc "zh") và phong cách chỉ định.
func LoadWithLanguage(language, style string, opts LoadOptions) Bundle {
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang != "zh" && lang != "chinese" && lang != "cn" {
		lang = "vi"
	}
	return Bundle{
		References: loadReferences(lang, style, opts),
		Prompts:    loadPrompts(lang),
		Styles:     loadStyles(lang, opts),
		Voice:      resolveAppendable(loadVoice(lang), "voice.md", opts),
		Language:   lang,
	}
}

const voicePlaceholder = "{{VOICE}}"

// BuildWriterPrompt là cổng ráp prompt duy nhất của Writer.
func BuildWriterPrompt(writerPrompt, voice, style string) string {
	out := strings.Replace(writerPrompt, voicePlaceholder, strings.TrimSpace(voice), 1)
	if style != "" {
		out += "\n\n" + style
	}
	return out
}

// OverrideVoice thay thế đoạn văn phong đã ráp (phục vụ thử nghiệm A/B).
func (b *Bundle) OverrideVoice(raw string) {
	b.Voice = raw
}

func resolveAppendable(builtin, name string, opts LoadOptions) string {
	out := builtin
	if s := readOverride(opts.HomeStyleDir, name); s != "" {
		out += "\n\n## Người dùng ghi đè văn phong toàn cục (User Global Style Override)\n\n" + s
	}
	if s := readOverride(opts.BookStyleDir, name); s != "" {
		out += "\n\n## Ghi đè văn phong cuốn sách này (Book Style Override)\n\n" + s
	}
	return out
}

func readOverride(dir, name string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

var styleNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func loadVoice(language string) string {
	if language == "zh" {
		if data, err := voiceFS.ReadFile("voice_zh.md"); err == nil {
			return string(data)
		}
	}
	return mustRead(voiceFS, "voice.md")
}

func loadReferences(language, style string, opts LoadOptions) tools.References {
	if style == "" {
		style = "default"
	}
	prefix := "references/"
	if language == "zh" {
		prefix = "references/zh/"
	}
	readRef := func(rel string) string {
		if data, err := referencesFS.ReadFile(prefix + rel); err == nil {
			return string(data)
		}
		return mustRead(referencesFS, "references/"+rel)
	}

	refs := tools.References{
		ChapterGuide:      readRef("chapter-guide.md"),
		HookTechniques:    readRef("hook-techniques.md"),
		QualityChecklist:  readRef("quality-checklist.md"),
		OutlineTemplate:   readRef("outline-template.md"),
		CharacterTemplate: readRef("character-template.md"),
		ChapterTemplate:   readRef("chapter-template.md"),
		Consistency:       readRef("consistency.md"),
		ContentExpansion:  readRef("content-expansion.md"),
		DialogueWriting:   readRef("dialogue-writing.md"),
		LongformPlanning:  readRef("longform-planning.md"),
		Differentiation:   readRef("differentiation.md"),
		AntiAITone:        resolveAppendable(readRef("anti-ai-tone.md"), "anti-ai-tone.md", opts),
	}
	if style != "" && style != "default" {
		genreDir := prefix + "genres/" + style + "/"
		if data, err := referencesFS.ReadFile(genreDir + "style-references.md"); err == nil {
			refs.StyleReference = string(data)
		} else if data, err := referencesFS.ReadFile("references/genres/" + style + "/style-references.md"); err == nil {
			refs.StyleReference = string(data)
		}
		if data, err := referencesFS.ReadFile(genreDir + "arc-templates.md"); err == nil {
			refs.ArcTemplates = string(data)
		} else if data, err := referencesFS.ReadFile("references/genres/" + style + "/arc-templates.md"); err == nil {
			refs.ArcTemplates = string(data)
		}
		relPath := filepath.Join("genres", style, "style-references.md")
		for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
			if s := readOverride(dir, relPath); s != "" {
				refs.StyleReference = s
			}
		}
	}
	return refs
}

func loadPrompts(languages ...string) Prompts {
	language := "vi"
	if len(languages) > 0 && languages[0] != "" {
		language = languages[0]
	}
	prefix := "prompts/"
	if language == "zh" {
		prefix = "prompts/zh/"
	}
	readPrompt := func(filename string) string {
		if data, err := promptsFS.ReadFile(prefix + filename); err == nil {
			return string(data)
		}
		return mustRead(promptsFS, "prompts/"+filename)
	}

	return Prompts{
		ArchitectShort:   WithSimulationGuidance(readPrompt("architect-short.md"), "architect", language),
		ArchitectLong:    WithSimulationGuidance(readPrompt("architect-long.md"), "architect", language),
		Writer:           WithSimulationGuidance(readPrompt("writer.md"), "writer", language),
		Editor:           WithSimulationGuidance(readPrompt("editor.md"), "editor", language),
		ImportSegment:    readPrompt("import-segment.md"),
		ImportAnalyze:    readPrompt("import-analyze.md"),
		ImportSynthesize: readPrompt("import-synthesize.md"),
		ImportRange:      readPrompt("import-range.md"),
		SimulationSource: readPrompt("simulation-source.md"),
		SimulationMerge:  readPrompt("simulation-merge.md"),
		RevisionAnalyze:  readPrompt("revision-analyze.md"),

		ArbiterPlanStart:    readPrompt("arbiter-plan-start.md"),
		ArbiterIntervention: readPrompt("arbiter-intervention.md"),
		ArbiterFailure:      readPrompt("arbiter-failure.md"),
	}
}

// WithSimulationGuidance nối thêm hướng dẫn mô phỏng văn phong theo vai trò và ngôn ngữ.
func WithSimulationGuidance(prompt, role string, language ...string) string {
	lang := "vi"
	if len(language) > 0 && language[0] == "zh" {
		lang = "zh"
	}
	guidance := simulationGuidanceVI
	if lang == "zh" {
		guidance = simulationGuidanceZH
	}
	return prompt + "\n\n" + strings.ReplaceAll(guidance, "{{role}}", role)
}

// OverridePrompt ghi đè prompt của vai trò cụ thể.
func (b *Bundle) OverridePrompt(file, raw string) error {
	role, ok := promptRole[file]
	if !ok {
		return fmt.Errorf("không hỗ trợ ghi đè file prompt: %s (chỉ có thể ghi đè prompt vai trò cốt lõi)", file)
	}
	wrapped := WithSimulationGuidance(raw, role, b.Language)
	switch file {
	case "architect-short.md":
		b.Prompts.ArchitectShort = wrapped
	case "architect-long.md":
		b.Prompts.ArchitectLong = wrapped
	case "writer.md":
		b.Prompts.Writer = wrapped
	case "editor.md":
		b.Prompts.Editor = wrapped
	}
	return nil
}

var promptRole = map[string]string{
	"architect-short.md": "architect",
	"architect-long.md":  "architect",
	"writer.md":          "writer",
	"editor.md":          "editor",
}

const simulationGuidanceVI = `## Hồ sơ mô phỏng văn phong (Simulation Profile)

Khi trong planning_memory hoặc working_memory của novel_context xuất hiện simulation_profile, bắt buộc phải xem đó là ràng buộc định hướng mô phỏng của tác phẩm hiện tại. {{role}} cần đọc kỹ các trường style, lexicon, plot_design, hook_design, pacing_density, reader_engagement và role_guidance.

Nguyên tắc sử dụng: Học hỏi cấu trúc, nhịp điệu, móc câu, cách giải phóng thông tin và thủ pháp cuốn hút độc giả; tuyệt đối không sao chép câu văn nguyên văn, tên nhân vật, địa danh, thiết lập độc quyền hay phân đoạn cố định. Nếu simulation_profile xung đột với yêu cầu rõ ràng của người dùng, ưu tiên tuân thủ yêu cầu của người dùng.`

const simulationGuidanceZH = `## 仿写画像

当 novel_context 的 planning_memory 或 working_memory 中存在 simulation_profile 时，必须把它视为当前作品的仿写方向约束。{{role}} 应读取其中的 style、lexicon、plot_design、hook_design、pacing_density、reader_engagement 和 role_guidance。

使用原则：借鉴结构、节奏、钩子、信息释放和吸引读者的手法；不要复制原文句子、人物、地名、专有设定或固定桥段。若 simulation_profile 与用户显式要求冲突，优先服从用户要求。`

func loadStyles(language string, opts LoadOptions) map[string]string {
	styles := make(map[string]string)
	prefix := "styles"
	if language == "zh" {
		prefix = "styles/zh"
	}
	entries, err := stylesFS.ReadDir(prefix)
	if err != nil {
		prefix = "styles"
		entries, err = stylesFS.ReadDir(prefix)
	}
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			data, err := stylesFS.ReadFile(prefix + "/" + e.Name())
			if err != nil {
				continue
			}
			styles[name] = string(data)
		}
	}
	for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
		overlayStyles(styles, dir)
	}
	return styles
}

func overlayStyles(styles map[string]string, dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(dir, "styles"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if !styleNameRe.MatchString(name) {
			slog.Warn("Bỏ qua tên file style không hợp lệ", "module", "assets", "dir", dir, "file", e.Name())
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "styles", e.Name()))
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
}

func mustRead(fs embed.FS, path string) string {
	data, err := fs.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("embed read %s: %v", path, err))
	}
	return string(data)
}
