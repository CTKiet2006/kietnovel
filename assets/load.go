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
	HomeStyleDir string // ~/.ainovel/style
}

// DefaultLoadOptions builds the production override sources from the book directory.
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
		References: loadReferences(style, opts),
		Prompts:    loadPrompts(),
		Styles:     loadStyles(opts),
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

Toàn bộ sản phẩm của vai trò này — tên truyện, tóm tắt, tiền đề, dàn ý, hồ sơ nhân vật, quy tắc thế giới, phục bút, bản nháp và chương hoàn chỉnh — PHẢI viết bằng Tiếng Việt tự nhiên, mượt mà, đúng chuẩn văn phong trong phần văn phong (voice) phía trên. Không trộn tiếng Trung hay tiếng Anh trừ tên riêng. Tên tool, tên file và các khóa checkpoint hệ thống giữ nguyên không dịch.`,

	"en": `## Writing Language

Every product of this role — story title, summary, premise, outline, character profiles, world rules, foreshadowing ledger, draft, and finished chapter — MUST be written in fluent, idiomatic English, following the prose standards given in the voice section above. Do not mix in Vietnamese or Chinese except for proper nouns. Tool names, file names, and system checkpoint keys stay untranslated.`,
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
	if style == "" {
		style = "default"
	}
	refs := tools.References{
		ChapterGuide:      mustRead(referencesFS, "references/chapter-guide.md"),
		HookTechniques:    mustRead(referencesFS, "references/hook-techniques.md"),
		QualityChecklist:  mustRead(referencesFS, "references/quality-checklist.md"),
		OutlineTemplate:   mustRead(referencesFS, "references/outline-template.md"),
		CharacterTemplate: mustRead(referencesFS, "references/character-template.md"),
		ChapterTemplate:   mustRead(referencesFS, "references/chapter-template.md"),
		Consistency:       mustRead(referencesFS, "references/consistency.md"),
		ContentExpansion:  mustRead(referencesFS, "references/content-expansion.md"),
		DialogueWriting:   mustRead(referencesFS, "references/dialogue-writing.md"),
		LongformPlanning:  mustRead(referencesFS, "references/longform-planning.md"),
		Differentiation:   mustRead(referencesFS, "references/differentiation.md"),
		AntiAITone:        resolveAppendable(mustRead(referencesFS, "references/anti-ai-tone.md"), "anti-ai-tone.md", opts),
	}
	if style != "" && style != "default" {
		genreDir := "references/genres/" + style + "/"
		if data, err := referencesFS.ReadFile(genreDir + "style-references.md"); err == nil {
			refs.StyleReference = string(data)
		}
		if data, err := referencesFS.ReadFile(genreDir + "arc-templates.md"); err == nil {
			refs.ArcTemplates = string(data)
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

func loadPrompts() Prompts {
	return Prompts{
		ArchitectShort:   WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-short.md"), "architect"),
		ArchitectLong:    WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-long.md"), "architect"),
		Writer:           WithSimulationGuidance(mustRead(promptsFS, "prompts/writer.md"), "writer"),
		Editor:           WithSimulationGuidance(mustRead(promptsFS, "prompts/editor.md"), "editor"),
		ImportSegment:    mustRead(promptsFS, "prompts/import-segment.md"),
		ImportAnalyze:    mustRead(promptsFS, "prompts/import-analyze.md"),
		ImportSynthesize: mustRead(promptsFS, "prompts/import-synthesize.md"),
		ImportRange:      mustRead(promptsFS, "prompts/import-range.md"),
		SimulationSource: mustRead(promptsFS, "prompts/simulation-source.md"),
		SimulationMerge:  mustRead(promptsFS, "prompts/simulation-merge.md"),
		RevisionAnalyze:  mustRead(promptsFS, "prompts/revision-analyze.md"),

		ArbiterPlanStart:    mustRead(promptsFS, "prompts/arbiter-plan-start.md"),
		ArbiterIntervention: mustRead(promptsFS, "prompts/arbiter-intervention.md"),
		ArbiterFailure:      mustRead(promptsFS, "prompts/arbiter-failure.md"),
	}
}

// WithSimulationGuidance appends the simulation-profile guidance to core prompts. Exported so eval and
// other external callers can reuse it for variant overrides, keeping an overridden prompt equivalent to the baseline Load produces (same wrapping path).
func WithSimulationGuidance(prompt, role string) string {
	return prompt + "\n\n" + strings.ReplaceAll(simulationGuidance, "{{role}}", role)
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
		data, err := stylesFS.ReadFile("styles/" + e.Name())
		if err != nil {
			continue
		}
		styles[name] = string(data)
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
