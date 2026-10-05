package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// AuditFoundationTool takes the Architect's semantic audit verdict on the already-persisted foundation.
// Literary and cross-file semantics are judged by the model; the tool only guarantees that the audit version, the verdict and the state transition stay consistent.
type AuditFoundationTool struct {
	store *store.Store
}

func NewAuditFoundationTool(store *store.Store) *AuditFoundationTool {
	return &AuditFoundationTool{store: store}
}

func (t *AuditFoundationTool) Name() string { return "audit_foundation" }
func (t *AuditFoundationTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Thẩm định tính nhất quán ngữ nghĩa giữa book, premise, outline, characters, world_rules và compass đã lưu đĩa. " +
			"Bắt buộc phải gọi lại novel_context trước và truyền nguyên vẹn chuỗi foundation_status.fingerprint."
	case "en":
		return "Audit cross-file semantic consistency among persisted book, premise, outline, characters, world_rules, and compass. " +
			"Must call novel_context first and pass foundation_status.fingerprint verbatim."
	default:
		return "审查已落盘的 book、premise、outline、characters、world_rules 与 compass 是否语义一致。" +
			"必须先重新调用 novel_context，并原样传入 foundation_status.fingerprint。"
	}
}
func (t *AuditFoundationTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Thẩm định thiết lập"
	case "en":
		return "Audit foundation"
	default:
		return "审查设定"
	}
}
func (t *AuditFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *AuditFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *AuditFoundationTool) StrictSchema() bool                     { return true }

func (t *AuditFoundationTool) Schema() map[string]any {
	artDesc := "存在问题的工件，如 book/premise/characters/layered_outline/world_rules/compass"
	descDesc := "跨文件语义问题"
	evDesc := "来自已落盘内容的具体冲突证据"
	sugDesc := "推荐修改方向；无需建议时为 null"
	fpDesc := "novel_context 返回的 foundation_status.fingerprint"
	readyDesc := "所有基础设定是否已语义一致，可以进入写作"
	sumDesc := "审查结论摘要"
	issuesDesc := "发现的跨文件语义问题；ready=true 时为空数组"

	switch toolLang(t.store) {
	case "vi":
		artDesc = "Thành phẩm có vấn đề (book/premise/characters/layered_outline/world_rules/compass)"
		descDesc = "Vấn đề ngữ nghĩa xung đột chéo giữa các file"
		evDesc = "Bằng chứng xung đột cụ thể từ nội dung đã lưu đĩa"
		sugDesc = "Hướng sửa đổi đề xuất; null nếu không cần"
		fpDesc = "foundation_status.fingerprint trả về từ novel_context"
		readyDesc = "Tất cả thiết lập đã nhất quán ngữ nghĩa và sẵn sàng chuyển sang viết hay chưa"
		sumDesc = "Tóm tắt kết luận thẩm định"
		issuesDesc = "Các vấn đề xung đột ngữ nghĩa; mảng rỗng nếu ready=true"
	case "en":
		artDesc = "Problematic artifact (book/premise/characters/layered_outline/world_rules/compass)"
		descDesc = "Cross-file semantic issue"
		evDesc = "Specific conflict evidence from persisted artifacts"
		sugDesc = "Suggested fix; null if none"
		fpDesc = "foundation_status.fingerprint returned by novel_context"
		readyDesc = "Whether all foundation settings are semantically consistent and ready for drafting"
		sumDesc = "Audit summary"
		issuesDesc = "Identified cross-file semantic issues; empty array if ready=true"
	}

	issue := schema.Object(
		schema.Property("artifact", schema.String(artDesc)).Required(),
		schema.Property("description", schema.String(descDesc)).Required(),
		schema.Property("evidence", schema.String(evDesc)).Required(),
		schema.Property("suggestion", llmcontract.Nullable(schema.String(sugDesc))).Required(),
	)
	return schema.Object(
		schema.Property("fingerprint", schema.String(fpDesc)).Required(),
		schema.Property("ready", schema.Bool(readyDesc)).Required(),
		schema.Property("summary", schema.String(sumDesc)).Required(),
		schema.Property("issues", schema.Array(issuesDesc, issue)).Required(),
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
		switch toolLang(t.store) {
		case "vi":
			return nil, fmt.Errorf("khi ready=false bắt buộc phải nêu rõ issues cụ thể: %w", errs.ErrToolArgs)
		case "en":
			return nil, fmt.Errorf("when ready=false, specific issues must be provided: %w", errs.ErrToolArgs)
		default:
			return nil, fmt.Errorf("ready=false 时必须给出具体 issues: %w", errs.ErrToolArgs)
		}
	}
	for i, issue := range audit.Issues {
		if strings.TrimSpace(issue.Artifact) == "" || strings.TrimSpace(issue.Description) == "" || strings.TrimSpace(issue.Evidence) == "" {
			switch toolLang(t.store) {
			case "vi":
				return nil, fmt.Errorf("issues[%d] bắt buộc phải có artifact, description và evidence: %w", i, errs.ErrToolArgs)
			case "en":
				return nil, fmt.Errorf("issues[%d] must contain artifact, description, and evidence: %w", i, errs.ErrToolArgs)
			default:
				return nil, fmt.Errorf("issues[%d] 必须包含 artifact、description 和 evidence: %w", i, errs.ErrToolArgs)
			}
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
		nextAction := "按 issues 修正对应基础设定，重新调用 novel_context 后再次审查"
		lang := ""
		if t.store != nil && t.store.BookLanguage != nil {
			lang, _ = t.store.BookLanguage.Load()
		}
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "vi":
			nextAction = "Sửa đổi các thiết lập cơ bản tương ứng theo issues, gọi lại novel_context rồi thẩm định lại"
		case "en":
			nextAction = "Fix corresponding foundation settings according to issues, call novel_context again and re-audit"
		}
		result["next_action"] = nextAction
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
