package prompt

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// 审阅发现只经 workspace_put_review 写入，最终仍过 ReviewVerdict.Validate。
// 这一侧曾是裸 object，模型按猜测写成 note+directive_id，review:r5 连撞三次 submission_blocked。
func TestReviewFindingSchemaIsConstrained(t *testing.T) {
	definition, err := BuiltinCapability(model.OperationReviewRange)
	if err != nil {
		t.Fatalf("review capability: %v", err)
	}
	var items map[string]any
	for _, tool := range definition.Worker.Tools {
		if tool.Name != ToolWorkspacePutReview {
			continue
		}
		var parsed struct {
			Properties struct {
				Findings struct {
					Items map[string]any `json:"items"`
				} `json:"findings"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &parsed); err != nil {
			t.Fatalf("%s schema: %v", tool.Name, err)
		}
		items = parsed.Properties.Findings.Items
	}
	if len(items) == 0 {
		t.Fatalf("%s 的 findings 没有条目 schema：模型会按任意对象写", ToolWorkspacePutReview)
	}

	properties, _ := items["properties"].(map[string]any)
	severity, _ := properties["severity"].(map[string]any)
	enum, _ := severity["enum"].([]any)
	got := make([]string, 0, len(enum))
	for _, value := range enum {
		text, _ := value.(string)
		got = append(got, text)
	}
	// 枚举必须与领域取值一致，否则模型写得出 Validate 不认的 severity。
	if want := []string{string(model.FindingBlocking), string(model.FindingNote)}; !reflect.DeepEqual(got, want) {
		t.Errorf("severity 枚举 = %v, want %v", got, want)
	}
	// 只有 blocking 能链接要求——这条约束校验器会拒，schema 必须先讲清楚。
	for _, field := range []string{"severity", "requirement"} {
		constraint, _ := properties[field].(map[string]any)
		if description, _ := constraint["description"].(string); description == "" {
			t.Errorf("%s 缺少说明：模型只能靠猜，撞了规则也不知道改哪个字段", field)
		}
	}
	required, _ := items["required"].([]any)
	if len(required) != 3 {
		t.Errorf("必填字段 = %v，want chapter / severity / note", required)
	}
}

// 三态核验（D62）的枚举必须与领域取值一致，否则模型写得出 Validate 不认的状态。
func TestVerdictCheckStatusMatchesDomain(t *testing.T) {
	var parsed struct {
		Properties struct {
			Checks struct {
				Items struct {
					Properties struct {
						Status struct {
							Enum        []string `json:"enum"`
							Description string   `json:"description"`
						} `json:"status"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"checks"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(verdictSubmitSchema), &parsed); err != nil {
		t.Fatalf("verdict schema: %v", err)
	}
	status := parsed.Properties.Checks.Items.Properties.Status
	if want := []string{model.CheckSatisfied, model.CheckViolated, model.CheckPending}; !reflect.DeepEqual(status.Enum, want) {
		t.Errorf("checks.status 枚举 = %v, want %v", status.Enum, want)
	}
	if !strings.Contains(status.Description, "settle") {
		t.Errorf("checks.status 没讲清 settle 项不得 pending：%q", status.Description)
	}
}

// authority_read 曾要求 kind/id/revision：模型得先学会存储种类和文档 ID 才能回查，
// 这些 ID 随后流进正文（D66）。回查只按故事语言定位，每个参数都说清返回什么。
func TestAuthorityReadSpeaksStoryLanguage(t *testing.T) {
	for _, kind := range []model.OperationKind{model.OperationDevelopPlan, model.OperationWriteChapter, model.OperationReviewRange} {
		definition, err := BuiltinCapability(kind)
		if err != nil {
			t.Fatalf("capability %s: %v", kind, err)
		}
		for _, tool := range definition.Worker.Tools {
			if tool.Name != ToolAuthorityRead {
				continue
			}
			var parsed struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(tool.InputSchema, &parsed); err != nil {
				t.Fatalf("%s schema: %v", kind, err)
			}
			names := slices.Sorted(maps.Keys(parsed.Properties))
			if want := []string{"arc", "chapter", "entity", "volume"}; !slices.Equal(names, want) {
				t.Errorf("%s 的 authority_read 参数 = %v, want %v", kind, names, want)
			}
			for name, property := range parsed.Properties {
				if property.Description == "" {
					t.Errorf("%s 的 authority_read.%s 没说明返回什么", kind, name)
				}
			}
		}
	}
}

// 校验器拒绝时必须说清改哪个字段，否则模型只会原样重试到 submission_blocked。
func TestReviewFindingRejectionNamesTheFieldToFix(t *testing.T) {
	verdict := model.ReviewVerdict{
		Revision: 2, ChapterIDs: []string{"ch-001"}, ReviewKey: "final_review_v1",
		Status: model.ReviewPass,
		Basis: model.EvidenceBasis{Documents: []model.DocumentBasis{{
			Ref: model.DocumentRef{Kind: model.DocumentManuscript, ID: "ch-001"}, Revision: 2}}},
		Findings: []model.ReviewFinding{{
			ChapterID: "ch-001", Severity: model.FindingNote, Note: "满足", Requirement: "intent:required:0",
		}},
	}
	err := verdict.Validate()
	if err == nil {
		t.Fatal("note 发现链接核验项却通过了校验")
	}
	for _, want := range []string{"requirement", "checks", string(model.FindingBlocking)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息没提到 %q，模型无法自纠：%v", want, err)
		}
	}
}
