package ctxpack

import (
	"context"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/ainovel-cli/internal/store"
)

const storeSummaryStrategyName = "store_summary"

type StoreSummaryCompactConfig struct {
	Store              *store.Store
	KeepRecentTokens   int
	SummaryTokenBudget int
}

type StoreSummaryCompactStrategy struct {
	store              *store.Store
	keepRecentTokens   int
	summaryTokenBudget int
}

func NewStoreSummaryCompact(cfg StoreSummaryCompactConfig) *StoreSummaryCompactStrategy {
	if cfg.KeepRecentTokens <= 0 {
		cfg.KeepRecentTokens = 20000
	}
	if cfg.SummaryTokenBudget <= 0 {
		cfg.SummaryTokenBudget = defaultStoreSummaryBudgetTokens
	}
	return &StoreSummaryCompactStrategy{
		store:              cfg.Store,
		keepRecentTokens:   cfg.KeepRecentTokens,
		summaryTokenBudget: cfg.SummaryTokenBudget,
	}
}

func (s *StoreSummaryCompactStrategy) Name() string { return storeSummaryStrategyName }

func (s *StoreSummaryCompactStrategy) Apply(ctx context.Context, _ []agentcore.AgentMessage, view []agentcore.AgentMessage, budget corecontext.Budget) ([]agentcore.AgentMessage, corecontext.StrategyResult, error) {
	if budget.Window <= 0 || budget.Tokens <= budget.Threshold {
		return view, corecontext.StrategyResult{Name: s.Name()}, nil
	}
	return s.apply(ctx, view, budget)
}

func (s *StoreSummaryCompactStrategy) ForceApply(ctx context.Context, transcript []agentcore.AgentMessage, view []agentcore.AgentMessage, budget corecontext.Budget) ([]agentcore.AgentMessage, corecontext.StrategyResult, error) {
	base := transcript
	if len(base) == 0 {
		base = view
	}
	return s.apply(ctx, base, budget)
}

func (s *StoreSummaryCompactStrategy) apply(_ context.Context, msgs []agentcore.AgentMessage, budget corecontext.Budget) ([]agentcore.AgentMessage, corecontext.StrategyResult, error) {
	if s.store == nil || len(msgs) == 0 {
		return msgs, corecontext.StrategyResult{Name: s.Name()}, nil
	}

	sections, ok, err := buildWriterStoreSummaryText(s.store, s.summaryTokenBudget)
	if err != nil {
		return nil, corecontext.StrategyResult{Name: s.Name()}, err
	}
	if !ok {
		return msgs, corecontext.StrategyResult{Name: s.Name()}, nil
	}

	cut := corecontext.FindCutPoint(msgs, s.keepRecentTokens)
	if cut.FirstKeptIndex <= 0 {
		return msgs, corecontext.StrategyResult{Name: s.Name()}, nil
	}
	summary := storeSummaryPreamble
	if task := leadingTask(msgs); task != "" {
		summary += "\n\n" + taskHeading + task
	}
	summary += "\n\n" + sections

	toKeep := append([]agentcore.AgentMessage(nil), msgs[cut.FirstKeptIndex:]...)
	tokensBefore := corecontext.EstimateTotal(msgs)
	result := make([]agentcore.AgentMessage, 0, 1+len(toKeep))
	result = append(result, corecontext.ContextSummary{
		Summary:      summary,
		TokensBefore: tokensBefore,
		Timestamp:    time.Now(),
	})
	result = append(result, toKeep...)

	tokensAfter := corecontext.EstimateTotal(result)
	if tokensAfter >= tokensBefore {
		return msgs, corecontext.StrategyResult{Name: s.Name()}, nil
	}

	info := &corecontext.SummaryInfo{
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
		MessagesBefore: len(msgs),
		MessagesAfter:  len(result),
		CompactedCount: cut.FirstKeptIndex,
		KeptCount:      len(toKeep),
		IsSplitTurn:    cut.IsSplitTurn,
		SummaryLen:     len([]rune(summary)),
		Duration:       time.Millisecond,
	}
	if budget.Tokens > budget.Threshold && tokensAfter > budget.Threshold {
		info.Duration = 2 * time.Millisecond
	}

	return result, corecontext.StrategyResult{
		Applied:     true,
		TokensSaved: max(0, tokensBefore-tokensAfter),
		Name:        s.Name(),
		Info:        info,
	}, nil
}

const (
	storeSummaryPreamble = "以下内容来自小说持久化 store，用于在压缩后恢复写作上下文。"
	taskHeading          = "## 当前任务\n"
)

// leadingTask 取回协调器下发的任务：首次压缩来自首条 user 消息，之后来自上一份摘要。
// store 摘要与 LLM 摘要（WriterSummaryPrompt）都把"当前任务"作为固定一节，按下一个标题结束。
func leadingTask(msgs []agentcore.AgentMessage) string {
	switch first := msgs[0].(type) {
	case agentcore.Message:
		if first.Role == agentcore.RoleUser {
			return first.TextContent()
		}
	case corecontext.ContextSummary:
		if _, rest, ok := strings.Cut(first.Summary, taskHeading); ok {
			task, _, _ := strings.Cut(rest, "\n## ")
			return strings.TrimSpace(task)
		}
	}
	return ""
}
