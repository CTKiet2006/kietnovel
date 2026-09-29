package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/agents/ctxpack"
	"github.com/CTKiet2006/kietnovel/internal/agents/guard"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/CTKiet2006/kietnovel/internal/tools"
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/agentcore/subagent"
)

// agentToRole normalizes a subagent name into a role name that ModelSet recognizes.
// architect_short / architect_long both share the same architect role configuration.
// Synonymous with host.agentRoleName; build and host do not depend on each other, so each keeps its own copy.
func agentToRole(name string) string {
	if strings.HasPrefix(name, "architect_") {
		return "architect"
	}
	return name
}

// promptCacheBase derives a stable short hash from the book directory as the prompt cache identity prefix: the same book
// shares a routing bucket across process restarts without leaking local paths to the provider. The role suffix is appended by the caller,
// and each subagent spawn appends "#seq" on top (one key per session).
func promptCacheBase(bookDir string) string {
	sum := sha256.Sum256([]byte(bookDir))
	return "nvl-" + hex.EncodeToString(sum[:6])
}

// subagentMaxRetries is the LLM retry ceiling for every Worker.
// Backoff policy: exponential backoff (bounded by the maxDelay ceiling), deferring to the server's Retry-After when there is one.
// Tools only start once a complete Assistant message has been committed, so stream-idle / 503 /
// brief network blips can be retried safely inside a Worker without replaying tool side effects.
const subagentMaxRetries = 7

// UsageRecorder is the optional usage callback for BuildWorkers; its signature matches OnMessage,
// it is called once per agent message, and the Host layer does the aggregating. task is the task text of this spawn
// used as the session identity, so cache-chain-break detection can reset the baseline per session.
// nil means no tracking.
type UsageRecorder func(agentName, task string, msg agentcore.AgentMessage)

// ApplyThinking applies one specific role's reasoning effort to its Workers (used by the runtime /model command).
// architect → both architect_* subagents; writer/editor → the matching subagent.
// An empty level = inherit the model/provider default. Other role names are ignored.
type ApplyThinking func(role string, level agentcore.ThinkingLevel)

// ParseThinkingLevel converts a config string into an agentcore.ThinkingLevel.
// "" is legal (= no override / inherit); anything else must be one of off/low/medium/high/xhigh/max,
// otherwise it returns an error (at startup that degrades to empty with a warning; at runtime the error is echoed back to the user).
func ParseThinkingLevel(s string) (agentcore.ThinkingLevel, error) {
	lv := agentcore.NormalizeThinkingLevel(agentcore.ThinkingLevel(s))
	switch lv {
	case "", agentcore.ThinkingOff, agentcore.ThinkingLow, agentcore.ThinkingMedium,
		agentcore.ThinkingHigh, agentcore.ThinkingXHigh, agentcore.ThinkingMax:
		return lv, nil
	default:
		return "", fmt.Errorf("无效推理强度 %q（可选：off/low/medium/high/xhigh/max）", s)
	}
}

func ResolveThinkingForModel(model agentcore.ChatModel, level agentcore.ThinkingLevel) (agentcore.ThinkingLevel, bool) {
	level = agentcore.NormalizeThinkingLevel(level)
	// For a plain chat model with no thinking support, an explicit off is not a no-op but an illegal argument.
	if cp, ok := model.(llm.CapabilityProvider); ok && cp.Capabilities().Thinking.Supported == llm.SupportNo {
		return agentcore.ThinkingAuto, level == agentcore.ThinkingAuto
	}
	return llm.ThinkingPolicyFor(model).Resolve(level)
}

func AvailableThinkingForModel(model agentcore.ChatModel) []agentcore.ThinkingLevel {
	if cp, ok := model.(llm.CapabilityProvider); ok && cp.Capabilities().Thinking.Supported == llm.SupportNo {
		return []agentcore.ThinkingLevel{agentcore.ThinkingAuto}
	}
	return llm.ThinkingPolicyFor(model).Available
}

// roleThinking resolves the reasoning effort in effect for a role; an illegal value degrades to empty (no override) with a warning.
func roleThinking(cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	lv, err := ParseThinkingLevel(cfg.ResolveReasoningEffort(role))
	if err != nil {
		slog.Warn("忽略无效推理强度配置", "module", "agent", "role", role, "err", err)
		return ""
	}
	return lv
}

func resolvedRoleThinking(model agentcore.ChatModel, cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	resolved, _ := ResolveThinkingForModel(model, roleThinking(cfg, role))
	return resolved
}

// BuildWorkers assembles three Workers (architect_short/long, writer, editor) into a programmatically
// callable subagent.Runner. The Engine calls its typed entry points directly, with no LLM tool layer
// (docs/engine-rfc.md §1).
// It returns the Runner, WriterRestorePack and ApplyThinking (the runtime /model command links each role's reasoning effort;
// each Worker's ContextManager is rebuilt automatically via the factory).
// onGuardBlock is optional (nil-safe): the block/escalation audit callback for each Worker's StopGuard.
func BuildWorkers(
	cfg bootstrap.Config,
	store *store.Store,
	styleStats *tools.StyleStatsIndex,
	models *bootstrap.ModelSet,
	bundle assets.Bundle,
	recordUsage UsageRecorder,
	onGuardBlock guard.BlockHook,
) (*subagent.Runner, *ctxpack.WriterRestorePack, ApplyThinking) {
	// Shared tools
	contextTool := tools.NewContextTool(store, bundle.References, cfg.Style, styleStats)
	readChapter := tools.NewReadChapterTool(store)

	architectTools := []agentcore.Tool{
		contextTool,
		tools.NewSaveBookTool(store),
		tools.NewSaveFoundationTool(store),
		tools.NewReviseOutlineTool(store),
		tools.NewResolveOutlineFeedbackTool(store),
		tools.NewAuditFoundationTool(store),
	}
	architectLongTools := append([]agentcore.Tool(nil), architectTools...)
	architectLongTools = append(architectLongTools, tools.NewExpandNextArcTool(store))
	writerTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewPlanChapterTool(store),
		tools.NewDraftChapterTool(store),
		tools.NewEditChapterTool(store),
		tools.NewCheckConsistencyTool(store),
		tools.NewCommitChapterTool(store, styleStats),
	}
	editorTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewSaveReviewTool(store),
		tools.NewSaveArcSummaryTool(store),
		tools.NewSaveVolumeSummaryTool(store),
	}

	// Provider failover is only logged, not reported to the host
	reportFailover := func(ev bootstrap.FailoverEvent) {
		slog.Warn("provider 切换",
			"module", "agent",
			"role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err,
		)
	}

	architectModel := models.ForRoleWithFailover("architect", reportFailover)
	writerModel := models.ForRoleWithFailover("writer", reportFailover)
	editorModel := models.ForRoleWithFailover("editor", reportFailover)

	// The ContextManager is rebuilt by the factory on every call and its window follows model swaps dynamically (see the factory below).
	architectProvider, architectModelName, _ := models.CurrentSelection("architect")
	architectContextWindow, architectSource := cfg.ResolveContextWindow(architectProvider, architectModelName)
	bootstrap.LogContextWindowChoice("architect", architectModelName, architectContextWindow, architectSource)

	writerProvider, writerModelName, _ := models.CurrentSelection("writer")
	writerContextWindow, writerSource := cfg.ResolveContextWindow(writerProvider, writerModelName)
	bootstrap.LogContextWindowChoice("writer", writerModelName, writerContextWindow, writerSource)

	editorProvider, editorModelName, _ := models.CurrentSelection("editor")
	editorContextWindow, editorSource := cfg.ResolveContextWindow(editorProvider, editorModelName)
	bootstrap.LogContextWindowChoice("editor", editorModelName, editorContextWindow, editorSource)

	// modelLookup attaches _meta:{provider,model} to every assistant message as it writes the session,
	// so replay no longer depends on the "current ModelSet" to back out historical cost, and switching models mid-run still computes it exactly.
	modelLookup := func(agentName string) (string, string) {
		role := agentToRole(agentName)
		provider, name, _ := models.CurrentSelection(role)
		return provider, name
	}
	baseOnMsg := store.Sessions.SubAgentLogger(modelLookup)
	onMsg := func(agentName, task string, msg agentcore.AgentMessage) {
		baseOnMsg(agentName, task, msg)
		if recordUsage != nil {
			recordUsage(agentName, task, msg)
		}
	}

	// Prompt cache: one base per book, one name per role, one key per session (subagent spawns append #seq).
	// The OpenAI family uses prompt_cache_key for routing affinity; the Claude family uses cache_control rolling breakpoints
	// (a system floor plus the tip of the last message). When the provider does not support it, agentcore silently drops it based on capability,
	// because in multi-turn sessions the cache-read benefit is always positive, so there is no toggle.
	cacheBase := promptCacheBase(store.Dir())

	architectStopGuardFactory := func(_, _ string) agentcore.StopGuard {
		return guard.NewArchitectStopGuard(store, onGuardBlock)
	}
	// The ContextManager for Architect / Editor is rebuilt per run against the current model, and its window follows model swaps.
	roleContextFactory := func(profile roleContextProfile) func(agentcore.ChatModel) agentcore.ContextManager {
		return func(model agentcore.ChatModel) agentcore.ContextManager {
			window, _ := models.ResolveContextWindow(bootstrap.ModelProvider(model), bootstrap.ModelName(model))
			return newRoleContextManager(profile, model, window, contextTool.Name())
		}
	}
	architectThinking, _ := ResolveThinkingForModel(architectModel, roleThinking(cfg, "architect"))
	architectShort := subagent.Config{
		Name:                  "architect_short",
		Description:           "短篇规划师：为单卷、单冲突、高密度故事生成紧凑设定与扁平大纲",
		Model:                 architectModel,
		SystemPrompt:          bundle.Prompts.ArchitectShort,
		Tools:                 architectTools,
		MaxTurns:              15,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         architectThinking,
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-architect_short",
		ContextManagerFactory: roleContextFactory(architectContextProfile),
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			return foundationReadyResult(toolName, result)
		},
		StopGuardFactory: architectStopGuardFactory,
	}
	architectLong := subagent.Config{
		Name:                  "architect_long",
		Description:           "长篇规划师：为连载型、可持续升级的故事生成分层设定与卷弧大纲",
		Model:                 architectModel,
		SystemPrompt:          bundle.Prompts.ArchitectLong,
		Tools:                 architectLongTools,
		MaxTurns:              20,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         architectThinking,
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-architect_long",
		ContextManagerFactory: roleContextFactory(architectContextProfile),
		StopAfterToolResult:   architectLongShouldStopAfterToolResult,
		StopGuardFactory:      architectStopGuardFactory,
	}

	// The only assembly path: the protocol template's {{VOICE}} placeholder is filled in place with the style section, then the style presets are appended.
	// eval's voice A/B goes through the same function, so both arms are equivalent (docs/voice-layer.md §3.2).
	writerPrompt := assets.BuildWriterPrompt(bundle.Prompts.Writer, bundle.Voice, bundle.Styles[cfg.Style])

	restore := &ctxpack.WriterRestorePack{}
	restore.Refresh(store)

	writer := subagent.Config{
		Name:             "writer",
		Description:      "创作者：自主完成一章的构思、写作、自审和提交",
		Model:            writerModel,
		SystemPrompt:     writerPrompt,
		Tools:            writerTools,
		MaxTurns:         30,
		MaxRetries:       subagentMaxRetries,
		ThinkingLevel:    resolvedRoleThinking(writerModel, cfg, "writer"),
		StopAfterTools:   []string{"commit_chapter"},
		OnMessage:        onMsg,
		CacheLastMessage: "ephemeral",
		PromptCacheKey:   cacheBase + "-writer",
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return guard.NewWriterStopGuard(store, onGuardBlock)
		},
		ContextManagerFactory: func(model agentcore.ChatModel) agentcore.ContextManager {
			// Rebuild the context manager per chapter against the current writer model.
			window, _ := models.ResolveContextWindow(bootstrap.ModelProvider(model), bootstrap.ModelName(model))
			return newContextManager(contextManagerConfig{
				Model:         model,
				ContextWindow: window,
				ReserveTokens: bootstrap.CompactReserveTokens(window),
				Agent:         "writer",
				// Commit the projection so later turns do not keep rewriting the request prefix.
				CommitProjected: true,
				ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
					MinResultTokens: 200,
				},
				ExtraStrategies: []corecontext.Strategy{
					ctxpack.NewStoreSummaryCompact(ctxpack.StoreSummaryCompactConfig{
						Store:            store,
						KeepRecentTokens: 20000,
					}),
				},
				Summary: &corecontext.FullSummaryConfig{
					PostSummaryHooks:    []corecontext.PostSummaryHook{restore.Hook()},
					SystemPrompt:        ctxpack.WriterSummarySystemPrompt,
					SummaryPrompt:       ctxpack.WriterSummaryPrompt,
					UpdateSummaryPrompt: ctxpack.WriterUpdateSummaryPrompt,
					TurnPrefixPrompt:    ctxpack.WriterTurnPrefixPrompt,
				},
			})
		},
	}

	editor := subagent.Config{
		Name:                  "editor",
		Description:           "审阅者：阅读原文，从结构和审美两个层面发现问题",
		Model:                 editorModel,
		SystemPrompt:          bundle.Prompts.Editor,
		Tools:                 editorTools,
		MaxTurns:              20,
		MaxRetries:            subagentMaxRetries,
		ThinkingLevel:         resolvedRoleThinking(editorModel, cfg, "editor"),
		OnMessage:             onMsg,
		CacheLastMessage:      "ephemeral",
		PromptCacheKey:        cacheBase + "-editor",
		ContextManagerFactory: roleContextFactory(editorContextProfile),
		// Stop as soon as a terminal artifact matches. A terminal exit still consults the StopGuard (contract test TestContract_
		// TerminalToolExitConsultsStopGuard), and the task-aware NewEditorStopGuard is responsible for
		// vetoing the "dispatched to write an arc summary but only reviewed" early exit, so save_review can hard stop safely.
		StopAfterToolResult: func(toolName string, _ json.RawMessage) bool {
			return toolName == "save_review" || toolName == "save_arc_summary" || toolName == "save_volume_summary"
		},
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return guard.NewEditorStopGuard(store, task, onGuardBlock)
		},
	}

	runner := subagent.NewRunner(architectShort, architectLong, writer, editor)

	// Link each role's reasoning effort at runtime (used by the /model command).
	applyThinking := func(role string, level agentcore.ThinkingLevel) {
		switch role {
		case "architect":
			level, _ = ResolveThinkingForModel(models.ForRole("architect"), level)
			runner.SetThinkingLevel("architect_short", level)
			runner.SetThinkingLevel("architect_long", level)
		case "writer", "editor":
			level, _ = ResolveThinkingForModel(models.ForRole(role), level)
			runner.SetThinkingLevel(role, level)
		}
	}

	return runner, restore, applyThinking
}

type saveFoundationResult struct {
	Type            string `json:"type"`
	FoundationReady bool   `json:"foundation_ready"`
}

func decodeSaveFoundationResult(toolName string, result json.RawMessage) saveFoundationResult {
	if toolName != "save_foundation" {
		return saveFoundationResult{}
	}
	var r saveFoundationResult
	_ = json.Unmarshal(result, &r)
	return r
}

func architectLongShouldStopAfterToolResult(toolName string, result json.RawMessage) bool {
	if foundationReadyResult(toolName, result) {
		return true
	}
	if toolName == "expand_next_arc" {
		return true
	}
	r := decodeSaveFoundationResult(toolName, result)
	switch r.Type {
	case "complete_book":
		return true
	default:
		return false
	}
}

func foundationReadyResult(toolName string, result json.RawMessage) bool {
	if toolName != "audit_foundation" {
		return false
	}
	var r struct {
		FoundationReady bool `json:"foundation_ready"`
	}
	return json.Unmarshal(result, &r) == nil && r.FoundationReady
}
