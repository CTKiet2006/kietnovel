package assets

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/tools"
)

//go:embed prompts/*.md
var promptsFS embed.FS

//go:embed references
var referencesFS embed.FS

//go:embed styles/*.md
var stylesFS embed.FS

//go:embed voice.md voice_en.md voice_zh.md
var voiceFS embed.FS

// Prompts is the set of embedded prompts.
type Prompts struct {
	ArchitectShort   string
	ArchitectLong    string
	Writer           string // the protocol template, carries the {{VOICE}} placeholder; the final text is assembled by BuildWriterPrompt
	Editor           string
	ImportSegment    string // semantic segmentation: detect chapter / volume / ancillary-text boundaries
	ImportAnalyze    string // per-chapter fact extraction over consecutive batches
	ImportSynthesize string // layered synthesis and volume-arc division (the whole-book BookSynthesis)
	ImportRange      string // consecutive range summaries in the Map stage of a long book (RangeDigest)
	SimulationSource string
	SimulationMerge  string
	RevisionAnalyze  string

	// Arbiter adjudication prompts (LLM-as-function, without the simulation guidance wrapper).
	ArbiterPlanStart    string
	ArbiterIntervention string
	ArbiterFailure      string
}

// Bundle is the set of static resources needed at runtime.
type Bundle struct {
	References tools.References
	Prompts    Prompts
	Styles     map[string]string
	Voice      string // the writing standard (the voice layer), already assembled with the three-tier override; see docs/voice-layer.md
	Language   string // the writing language ("vi"/"zh"), recorded only, the protocol itself does not fork
}

// LoadOptions declares the override sources for the voice layer. An empty dir = skip that tier
// (eval passes the zero value to get a purely builtin, deterministic baseline, unpolluted by the
//
// user's local overrides).
// Path semantics: BookStyleDir binds to the book dir (outputDir) rather than cwd - the voice travels
type LoadOptions struct {
	BookStyleDir string // <outputDir>/style
	HomeStyleDir string // ~/.kietnovel/style
}

// DefaultLoadOptions builds the production override sources from the book directory.
func DefaultLoadOptions(outputDir string) LoadOptions {
	var opts LoadOptions
	if outputDir != "" {
		opts.BookStyleDir = filepath.Join(outputDir, "style")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		opts.HomeStyleDir = filepath.Join(home, ".kietnovel", "style")
	}
	return opts
}

// Load returns the resource bundle for the given style (default writing language: Vietnamese).
// Voice assets (voice / anti-ai-tone / styles / genre style-references) get a three-tier
// override per opts: builtin < global < this book.
func Load(style string, opts LoadOptions) Bundle {
	return LoadWithLanguage("vi", style, opts)
}

// LoadWithLanguage loads the resource bundle for the given writing language.
// There is only one set of prompts (the upstream Chinese protocol, already validated); language only
//   - affects the voice layer: vi→voice.md, en→voice_en.md, zh→voice_zh.md;
//   - and, for vi/en, appends a mandatory output-language instruction to Architect/Writer/Editor (see ApplyLanguage).
//
// This avoids maintaining two full copies of a protocol that goes stale, while keeping the output language correct.
func LoadWithLanguage(language, style string, opts LoadOptions) Bundle {
	lang := strings.ToLower(strings.TrimSpace(language))
	voiceFile := "voice.md"
	switch lang {
	case "en":
		voiceFile = "voice_en.md"
	case "zh":
		voiceFile = "voice_zh.md"
	}
	return Bundle{
		References: loadReferencesForLanguage(style, lang, opts),
		Prompts:    loadPromptsForLanguage(lang),
		Styles:     loadStylesForLanguage(lang, opts),
		Voice:      resolveAppendable(mustRead(voiceFS, voiceFile), "voice.md", opts),
		Language:   lang,
	}
}

// voicePlaceholder is the in-place insertion point for the voice section of the writer protocol template.
const voicePlaceholder = "{{VOICE}}"

// BuildWriterPrompt is the single assembly entry point for the writer system prompt, shared by
// production / eval / tests, so both A/B arms take the same path (precedent lesson: WithSimulationGuidance).
// writerPrompt is the protocol template holding the placeholder (it may already carry a simulation
// guidance suffix; the placeholder sits inside the prefix, so replacement is unaffected); style is not appended when empty.
func BuildWriterPrompt(writerPrompt, voice, style string) string {
	out := strings.Replace(writerPrompt, voicePlaceholder, strings.TrimSpace(voice), 1)
	if style != "" {
		out += "\n\n" + style
	}
	return out
}

// Per-language output directives, appended to the end of the Architect/Writer/Editor prompts.
// The protocol text itself stays upstream Chinese (validated); only here do we state "which language the output must be written in".
var languageDirectives = map[string]string{
	"vi": `## Ngôn ngữ sáng tác

Toàn bộ sản phẩm của vai trò này — tên truyện, tóm tắt, tiền đề, dàn ý, hồ sơ nhân vật, quy tắc thế giới, phục bút, bản nháp và chương hoàn chỉnh — PHẢI viết bằng Tiếng Việt tự nhiên, mượt mà, đúng chuẩn văn phong trong phần văn phong (voice) phía trên. Toàn bộ quá trình tư duy, phân tích logic và suy nghĩ nội bộ (thinking/reasoning) PHẢI thực hiện hoàn toàn bằng Tiếng Việt, không suy nghĩ bằng tiếng Anh hay tiếng Trung. Không trộn tiếng Trung hay tiếng Anh trừ tên riêng. Tên tool, tên file và các khóa checkpoint hệ thống giữ nguyên không dịch.`,

	"en": `## Writing Language

Every product of this role — story title, summary, premise, outline, character profiles, world rules, foreshadowing ledger, draft, and finished chapter — MUST be written in fluent, idiomatic English, following the prose standards given in the voice section above. The entire internal thinking and reasoning process MUST be conducted in English. Do not mix in Vietnamese or Chinese except for proper nouns. Tool names, file names, and system checkpoint keys stay untranslated.`,
}

// ApplyLanguage appends the output-language directive to Architect/Writer/Editor.
// "vi"/"en" (including the empty string, for compatibility with old configs) take effect; "zh" makes no
// change on return, since the protocol is already Chinese. Called once at startup, see cmd/kietnovel/main.go.
func (b *Bundle) ApplyLanguage(lang string) {
	key := strings.ToLower(strings.TrimSpace(lang))
	if key == "zh" {
		// The protocol itself is already Chinese, so there is nothing to declare.
		return
	}
	directive, ok := languageDirectives[key]
	if !ok {
		directive = languageDirectives["vi"] // unknown/empty -> vi (compatible with old configs)
	}
	d := "\n\n" + directive
	b.Prompts.ArchitectShort += d
	b.Prompts.ArchitectLong += d
	b.Prompts.Writer += d
	b.Prompts.Editor += d
	if key == "vi" {
		b.References.ChapterTemplate = `# Chương [X]: [Tên chương]

## Tóm tắt chương
- **Sự kiện cốt lõi**: [Một câu khái quát diễn biến chính của chương]
- **Tiếp nối chương trước**: [Giải quyết hoặc nối tiếp móc câu trước]
- **Móc câu lơ lửng**: [Móc câu kịch tính cuối chương]

---

## Nội dung chính

[Nội dung chính của chương, số từ tuân thủ theo user_rules và sở thích văn phong]

---

## Ghi chú chương
- Móc câu chương này: [Mô tả ngắn móc câu]
- Hé lộ chương sau: [Tùy chọn, 1-2 câu]
- Đánh dấu phục bút: [Nếu có gài phục bút, ghi nhận tại đây]`
	} else if key == "en" {
		b.References.ChapterTemplate = `# Chapter [X]: [Chapter Title]

## Chapter Summary
- **Core Event**: [One sentence summary]
- **Bridge from previous**: [Address previous suspense]
- **Suspense Hook**: [Ending hook]

---

## Main Text

[Chapter text content conforming to user_rules and target word count]

---

## Chapter Notes
- Suspense hook: [Brief note]
- Next chapter preview: [Optional, 1-2 sentences]
- Foreshadowing markers: [Record if any]`
	}
	// Arbiter cũng phải nhận directive. Thiếu đoạn này thì nhiệm vụ Arbiter sinh ra
	// vẫn bằng Trung, Architect làm theo nhiệm vụ đó, và tiền đề ra tiếng Trung dù
	// config đã đặt language=vi. Directive gắn vào Architect là không đủ — nó
	// chỉ nói "viết bằng tiếng Việt", còn nhiệm vụ truyền xuống lại là tiếng Trung.
	b.Prompts.ArbiterPlanStart += d
	b.Prompts.ArbiterIntervention += d
	b.Prompts.ArbiterFailure += d
}

// OverrideVoice replaces the assembled voice section wholesale with raw (used by eval for voice A/B).
// variant and baseline are still assembled through the same BuildWriterPrompt path.
func (b *Bundle) OverrideVoice(raw string) {
	b.Voice = raw
}

// resolveAppendable performs the three-tier assembly with append semantics: the builtin is kept, and
// global / this-book are appended as marked sections. With no override it returns the builtin verbatim
// (byte-for-byte unchanged - one of the voice layer acceptance criteria). "Later wins" is a priority
func resolveAppendable(builtin, name string, opts LoadOptions) string {
	out := builtin
	if s := readOverride(opts.HomeStyleDir, name); s != "" {
		out += "\n\n## 用户全局文风覆盖（以下要求优先于项目默认）\n\n" + s
	}
	if s := readOverride(opts.BookStyleDir, name); s != "" {
		out += "\n\n## 本书文风覆盖（以下要求优先于以上全部）\n\n" + s
	}
	return out
}

// readOverride reads a single file from the override dir; an empty dir, a missing file, or blank content always returns "".
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

// styleNameRe validates user-supplied style file names (without extension), rejecting path characters.
var styleNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func loadReferences(style string, opts LoadOptions) tools.References {
	return loadReferencesForLanguage(style, "zh", opts)
}

func loadReferencesForLanguage(style, lang string, opts LoadOptions) tools.References {
	if style == "" {
		style = "default"
	}
	refs := tools.References{
		ChapterGuide:      referenceForLanguage("chapter-guide", lang),
		HookTechniques:    referenceForLanguage("hook-techniques", lang),
		QualityChecklist:  referenceForLanguage("quality-checklist", lang),
		OutlineTemplate:   referenceForLanguage("outline-template", lang),
		CharacterTemplate: referenceForLanguage("character-template", lang),
		ChapterTemplate:   referenceForLanguage("chapter-template", lang),
		Consistency:       referenceForLanguage("consistency", lang),
		ContentExpansion:  referenceForLanguage("content-expansion", lang),
		DialogueWriting:   referenceForLanguage("dialogue-writing", lang),
		LongformPlanning:  referenceForLanguage("longform-planning", lang),
		Differentiation:   referenceForLanguage("differentiation", lang),
		AntiAITone:        resolveAppendable(referenceForLanguage("anti-ai-tone", lang), "anti-ai-tone.md", opts),
	}
	if style != "" && style != "default" {
		genreDir := "references/genres/" + style + "/"
		cleanLang := strings.ToLower(strings.TrimSpace(lang))
		if cleanLang != "" && cleanLang != "zh" {
			if data, err := referencesFS.ReadFile(genreDir + fmt.Sprintf("style-references_%s.md", cleanLang)); err == nil && len(data) > 0 {
				refs.StyleReference = string(data)
			}
			if data, err := referencesFS.ReadFile(genreDir + fmt.Sprintf("arc-templates_%s.md", cleanLang)); err == nil && len(data) > 0 {
				refs.ArcTemplates = string(data)
			}
		}
		if refs.StyleReference == "" {
			if data, err := referencesFS.ReadFile(genreDir + "style-references.md"); err == nil {
				refs.StyleReference = string(data)
			}
		}
		if refs.ArcTemplates == "" {
			if data, err := referencesFS.ReadFile(genreDir + "arc-templates.md"); err == nil {
				refs.ArcTemplates = string(data)
			}
		}
		// Genre style references: whole-file replacement on the same name (this book > global); when a custom style
		// has no builtin reference, only the override may supply it, with no fallback to default (a wrong reference is worse than none).
		relPath := filepath.Join("genres", style, "style-references.md")
		for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
			if s := readOverride(dir, relPath); s != "" {
				refs.StyleReference = s
			}
		}
	}
	return refs
}

func referenceForLanguage(baseName, lang string) string {
	if lang != "" && lang != "zh" {
		langPath := fmt.Sprintf("references/%s_%s.md", baseName, lang)
		if data, err := referencesFS.ReadFile(langPath); err == nil && len(data) > 0 {
			return string(data)
		}
	}
	return mustRead(referencesFS, fmt.Sprintf("references/%s.md", baseName))
}

func promptForLanguage(baseName, lang string) string {
	if lang != "" && lang != "zh" {
		langPath := fmt.Sprintf("prompts/%s_%s.md", baseName, lang)
		if data, err := promptsFS.ReadFile(langPath); err == nil && len(data) > 0 {
			return string(data)
		}
	}
	return mustRead(promptsFS, fmt.Sprintf("prompts/%s.md", baseName))
}

func loadPrompts() Prompts {
	return loadPromptsForLanguage("zh")
}

func loadPromptsForLanguage(lang string) Prompts {
	return Prompts{
		ArchitectShort:   WithSimulationGuidanceForLanguage(promptForLanguage("architect-short", lang), "architect", lang),
		ArchitectLong:    WithSimulationGuidanceForLanguage(promptForLanguage("architect-long", lang), "architect", lang),
		Writer:           WithSimulationGuidanceForLanguage(promptForLanguage("writer", lang), "writer", lang),
		Editor:           WithSimulationGuidanceForLanguage(promptForLanguage("editor", lang), "editor", lang),
		ImportSegment:    mustRead(promptsFS, "prompts/import-segment.md"),
		ImportAnalyze:    mustRead(promptsFS, "prompts/import-analyze.md"),
		ImportSynthesize: mustRead(promptsFS, "prompts/import-synthesize.md"),
		ImportRange:      mustRead(promptsFS, "prompts/import-range.md"),
		SimulationSource: mustRead(promptsFS, "prompts/simulation-source.md"),
		SimulationMerge:  mustRead(promptsFS, "prompts/simulation-merge.md"),
		RevisionAnalyze:  mustRead(promptsFS, "prompts/revision-analyze.md"),

		ArbiterPlanStart:    promptForLanguage("arbiter-plan-start", lang),
		ArbiterIntervention: promptForLanguage("arbiter-intervention", lang),
		ArbiterFailure:      promptForLanguage("arbiter-failure", lang),
	}
}

// WithSimulationGuidance appends the simulation-profile guidance to core prompts. Exported so eval and
// other external callers can reuse it for variant overrides, keeping an overridden prompt equivalent to the baseline Load produces (same wrapping path).
func WithSimulationGuidance(prompt, role string) string {
	return WithSimulationGuidanceForLanguage(prompt, role, "zh")
}

// WithSimulationGuidanceForLanguage appends localized simulation-profile guidance to core prompts.
func WithSimulationGuidanceForLanguage(prompt, role, lang string) string {
	return prompt + "\n\n" + strings.ReplaceAll(simulationGuidanceForLanguage(lang), "{{role}}", role)
}

func simulationGuidanceForLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		return `## Hồ sơ mô phỏng (Simulation Profile)

Khi trong planning_memory hoặc working_memory của novel_context xuất hiện simulation_profile, bắt buộc phải coi đó là định hướng mô phỏng văn phong của tác phẩm. {{role}} cần đọc kỹ các mục style, lexicon, plot_design, hook_design, pacing_density, reader_engagement và role_guidance.

Nguyên tắc sử dụng: học hỏi cấu trúc, nhịp điệu, móc câu, cách giải phóng thông tin và kỹ thuật cuốn hút độc giả; không sao chép nguyên văn câu từ, nhân vật, địa danh, thiết lập riêng hay tình tiết cố định từ bản gốc. Nếu simulation_profile xung đột với yêu cầu tường minh của người dùng, ưu tiên tuân thủ yêu cầu của người dùng.`
	case "en":
		return `## Simulation Profile

When simulation_profile is present in planning_memory or working_memory within novel_context, treat it as the stylistic emulation guide for this work. {{role}} should inspect style, lexicon, plot_design, hook_design, pacing_density, reader_engagement, and role_guidance.

Principles: emulate structure, pacing, hooks, information release, and engagement techniques; never copy verbatim sentences, character names, locations, unique settings, or stock plot points. If simulation_profile conflicts with explicit user instructions, user instructions take precedence.`
	default:
		return `## 仿写画像

当 novel_context 的 planning_memory 或 working_memory 中存在 simulation_profile 时，必须把它视为当前作品的仿写方向约束。{{role}} 应读取其中的 style、lexicon、plot_design、hook_design、pacing_density、reader_engagement 和 role_guidance。

使用原则：借鉴结构、节奏、钩子、信息释放和吸引读者的手法；不要复制原文句子、人物、地名、专有设定或固定桥段。若 simulation_profile 与用户显式要求冲突，优先服从用户要求。`
	}
}

// OverridePrompt replaces the role prompt for the given prompt file in the bundle with raw, running it
// through exactly the same WithSimulationGuidance wrapping as Load - eval A/B only calls it, without copying
// the wrapping logic, otherwise the baseline carries the simulation-profile suffix and the variant does not,
// making the A/B unequal. file is the prompt file name. Note: when overriding writer.md, raw must carry
// its own {{VOICE}} placeholder (protocol template semantics); to A/B only the voice, use OverrideVoice.
func (b *Bundle) OverridePrompt(file, raw string) error {
	role, ok := promptRole[file]
	if !ok {
		return fmt.Errorf("不支持覆盖的 prompt 文件: %s（仅核心提示词可覆盖）", file)
	}
	wrapped := WithSimulationGuidance(raw, role)
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

// promptRole maps core prompt file names to the simulation guidance role placeholder.
var promptRole = map[string]string{
	"architect-short.md": "architect",
	"architect-long.md":  "architect",
	"writer.md":          "writer",
	"editor.md":          "editor",
}

const simulationGuidance = `## 仿写画像

当 novel_context 的 planning_memory 或 working_memory 中存在 simulation_profile 时，必须把它视为当前作品的仿写方向约束。{{role}} 应读取其中的 style、lexicon、plot_design、hook_design、pacing_density、reader_engagement 和 role_guidance。

使用原则：借鉴结构、节奏、钩子、信息释放和吸引读者的手法；不要复制原文句子、人物、地名、专有设定或固定桥段。若 simulation_profile 与用户显式要求冲突，优先服从用户要求。`

// loadStyles enumerates the builtin style presets, then overlays styles/*.md from the override dir in
// the order global → this book (whole-file replacement on the same name; a new file name is a new style; a style is a whole voice, not merged).
func loadStyles(opts LoadOptions) map[string]string {
	return loadStylesForLanguage("zh", opts)
}

func loadStylesForLanguage(lang string, opts LoadOptions) map[string]string {
	styles := make(map[string]string)
	entries, err := stylesFS.ReadDir("styles")
	if err != nil {
		return styles
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if strings.HasSuffix(name, "_vi") || strings.HasSuffix(name, "_en") || strings.HasSuffix(name, "_zh") {
			continue
		}
		data, err := stylesFS.ReadFile("styles/" + e.Name())
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
	cleanLang := strings.ToLower(strings.TrimSpace(lang))
	if cleanLang != "" && cleanLang != "zh" {
		for name := range styles {
			langFile := fmt.Sprintf("styles/%s_%s.md", name, cleanLang)
			if data, err := stylesFS.ReadFile(langFile); err == nil && len(data) > 0 {
				styles[name] = string(data)
			}
		}
	}
	for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
		overlayStyles(styles, dir)
	}
	return styles
}

// overlayStyles overlays <dir>/styles/*.md into the styles set; illegal file names are skipped with a warning.
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
			slog.Warn("忽略非法风格文件名", "module", "assets", "dir", dir, "file", e.Name())
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
