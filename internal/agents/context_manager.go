package agents

import (
	"log/slog"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// contextManagerConfig 聚合 ContextManager 的全部配置参数。
type contextManagerConfig struct {
	Model            agentcore.ChatModel
	ContextWindow    int
	ReserveTokens    int
	Agent            string
	CommitProjected  bool
	Summary          *corecontext.FullSummaryConfig
	ToolMicrocompact *corecontext.ToolResultMicrocompactConfig
	ExtraStrategies  []corecontext.Strategy
}

func newContextManager(cfg contextManagerConfig) *corecontext.ContextEngine {
	var sc corecontext.FullSummaryConfig
	if cfg.Summary != nil {
		sc = *cfg.Summary
	}
	sc.Model = cfg.Model

	var tc corecontext.ToolResultMicrocompactConfig
	if cfg.ToolMicrocompact != nil {
		tc = *cfg.ToolMicrocompact
	}

	strategies := []corecontext.Strategy{
		corecontext.NewToolResultMicrocompact(tc),
	}
	strategies = append(strategies, cfg.ExtraStrategies...)
	strategies = append(strategies, corecontext.NewFullSummary(sc))

	var commitStrategies []string
	if cfg.CommitProjected {
		commitStrategies = make([]string, len(strategies))
		for i, strategy := range strategies {
			commitStrategies[i] = strategy.Name()
		}
	}

	engine := corecontext.NewEngine(corecontext.EngineConfig{
		ContextWindow:    cfg.ContextWindow,
		ReserveTokens:    cfg.ReserveTokens,
		CommitStrategies: commitStrategies,
		Strategies:       strategies,
	})

	callback := contextRewriteCallback(cfg.Agent)
	engine.SetProjectHook(callback)
	engine.SetRecoverHook(callback)
	return engine
}

// roleContextProfile 描述 Architect / Editor 这类"单任务、多次读取"Worker 的压缩档案：
// 只清理旧的 novel_context 结果（落盘数据可随时重读），写工具结果与章节原文保留；
// 仍超限时用角色专属提示词做全量摘要。
type roleContextProfile struct {
	Agent           string
	KeepRecentReads int // 保留最近几次 novel_context 结果不清理
	Summary         corecontext.FullSummaryConfig
}

// newRoleContextManager 按当前模型窗口构建该档案的 ContextManager。
func newRoleContextManager(p roleContextProfile, model agentcore.ChatModel, window int, contextToolName string) *corecontext.ContextEngine {
	summary := p.Summary
	return newContextManager(contextManagerConfig{
		Model:           model,
		ContextWindow:   window,
		ReserveTokens:   bootstrap.CompactReserveTokens(window),
		Agent:           p.Agent,
		CommitProjected: true,
		ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
			KeepRecent:      p.KeepRecentReads,
			MinResultTokens: 200,
			Classifier:      func(toolName string) bool { return toolName == contextToolName },
		},
		Summary: &summary,
	})
}

// contextRewriteCallback 创建上下文重写的日志回调。
// 新架构简化为只写 slog,不再写 runtime queue 和 UIEvent。
func contextRewriteCallback(agent string) func(corecontext.RewriteEvent) {
	return func(ev corecontext.RewriteEvent) {
		attrs := []any{
			"module", "context",
			"agent", agent,
			"reason", ev.Reason,
			"strategy", ev.Strategy,
			"committed", ev.Committed,
			"tokens_before", ev.TokensBefore,
			"tokens_after", ev.TokensAfter,
		}
		if info := ev.Info; info != nil {
			attrs = append(attrs,
				"msgs_before", info.MessagesBefore,
				"msgs_after", info.MessagesAfter,
				"compacted", info.CompactedCount,
				"kept", info.KeptCount,
				"duration_ms", info.Duration.Milliseconds(),
			)
		}
		slog.Warn("上下文重写", attrs...)
	}
}
