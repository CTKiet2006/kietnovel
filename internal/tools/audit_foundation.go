package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

// AuditFoundationTool 接收 Architect 对已落盘基础设定的语义审查结论。
// 文学与跨文件语义由模型判断；工具只保证审查版本、结论和状态迁移一致。
type AuditFoundationTool struct {
	store *store.Store
}

func NewAuditFoundationTool(store *store.Store) *AuditFoundationTool {
	return &AuditFoundationTool{store: store}
}

func (t *AuditFoundationTool) Name() string { return "audit_foundation" }
func (t *AuditFoundationTool) Description() string {
	return "审查已落盘的 book、premise、outline、characters、world_rules 与 compass 是否语义一致。" +
		"必须先重新调用 novel_context，并原样传入 foundation_status.fingerprint。"
}
func (t *AuditFoundationTool) Label() string                          { return "审查设定" }
func (t *AuditFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *AuditFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *AuditFoundationTool) StrictSchema() bool                     { return true }

func (t *AuditFoundationTool) Schema() map[string]any {
	issue := schema.Object(
		schema.Property("artifact", schema.String("存在问题的工件，如 book/premise/characters/layered_outline/world_rules/compass")).Required(),
		schema.Property("description", schema.String("跨文件语义问题")).Required(),
		schema.Property("evidence", schema.String("来自已落盘内容的具体冲突证据")).Required(),
		schema.Property("suggestion", llmcontract.Nullable(schema.String("推荐修改方向；无需建议时为 null"))).Required(),
	)
	return schema.Object(
		schema.Property("fingerprint", schema.String("novel_context 返回的 foundation_status.fingerprint")).Required(),
		schema.Property("ready", schema.Bool("所有基础设定是否已语义一致，可以进入写作")).Required(),
		schema.Property("summary", schema.String("审查结论摘要")).Required(),
		schema.Property("issues", schema.Array("发现的跨文件语义问题；ready=true 时为空数组", issue)).Required(),
	)
}

func (t *AuditFoundationTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var audit domain.FoundationAudit
	if err := json.Unmarshal(args, &audit); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if strings.TrimSpace(audit.Fingerprint) == "" {
		return nil, fmt.Errorf("fingerprint is required: %w", errs.ErrToolArgs)
	}
	if strings.TrimSpace(audit.Summary) == "" {
		return nil, fmt.Errorf("summary is required: %w", errs.ErrToolArgs)
	}

	missing, err := t.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w: %w", errs.ErrStoreRead, err)
	}
	for _, item := range missing {
		if item != "foundation_audit" {
			return nil, fmt.Errorf("基础设定尚缺 %s，不能审查: %w", item, errs.ErrToolPrecondition)
		}
	}
	current, err := t.store.FoundationFingerprint()
	if err != nil {
		return nil, fmt.Errorf("fingerprint foundation: %w: %w", errs.ErrStoreRead, err)
	}
	if audit.Fingerprint != current {
		return nil, fmt.Errorf("基础设定已发生变化；请重新调用 novel_context 获取最新 fingerprint 后再审查: %w", errs.ErrToolConflict)
	}
	if audit.Ready && len(audit.Issues) > 0 {
		return nil, fmt.Errorf("ready=true 时 issues 必须为空: %w", errs.ErrToolArgs)
	}
	if !audit.Ready && len(audit.Issues) == 0 {
		return nil, fmt.Errorf("ready=false 时必须给出具体 issues: %w", errs.ErrToolArgs)
	}
	for i, issue := range audit.Issues {
		if strings.TrimSpace(issue.Artifact) == "" || strings.TrimSpace(issue.Description) == "" || strings.TrimSpace(issue.Evidence) == "" {
			return nil, fmt.Errorf("issues[%d] 必须包含 artifact、description 和 evidence: %w", i, errs.ErrToolArgs)
		}
	}

	if err := t.store.Outline.SaveFoundationAudit(audit); err != nil {
		return nil, fmt.Errorf("save foundation audit: %w: %w", errs.ErrStoreWrite, err)
	}
	result := map[string]any{
		"foundation_ready": audit.Ready,
		"issues":           audit.Issues,
	}
	if !audit.Ready {
		result["next_action"] = "按 issues 修正对应基础设定，重新调用 novel_context 后再次审查"
		return json.Marshal(result)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "foundation_audit", "meta/foundation_audit.json"); err != nil {
		return nil, fmt.Errorf("checkpoint foundation audit: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		return nil, fmt.Errorf("enter writing phase: %w: %w", errs.ErrStoreWrite, err)
	}
	result["phase"] = string(domain.PhaseWriting)
	return json.Marshal(result)
}
