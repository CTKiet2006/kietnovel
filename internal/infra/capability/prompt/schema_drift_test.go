package prompt

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/domain/narrative"
)

// 工具 Schema 与宿主解码的结构体是两份手工维护的真理源。宿主按严格模式解码工具参数：
// Schema 公开了结构体没有的字段，模型照 Schema 填写就会被硬拒——连撞到提交保护停机。
// 这条检查按每个 Worker 的实际 Schema 逐路径比对 json tag，不需要人工登记的映射。
func TestToolSchemaFieldsAreDecodable(t *testing.T) {
	definitions, err := BuiltinCapabilities()
	if err != nil {
		t.Fatalf("built-in capabilities: %v", err)
	}
	submission := append(jsonFields(narrative.Submission{}), "reason", "workspace_key", "workspace_version", "workspace_versions")
	cases := []struct {
		tool, path string
		fields     []string
	}{
		{ToolAuthorityRead, "", jsonFields(narrative.Query{})},
		{ToolWorkspacePutChapter, "chapter", jsonFields(narrative.ChapterDraft{})},
		{ToolWorkspacePutChapter, "chapter.blocks[]", jsonFields(model.ManuscriptBlock{})},
		{ToolWorkspacePutReview, "findings[]", jsonFields(narrative.Finding{})},
		{ToolVerdictSubmit, "checks[]", jsonFields(model.RequirementCheck{})},
		{ToolProposalSubmit, "", submission},
		{ToolProposalSubmit, "compass", jsonFields(model.Compass{})},
		{ToolProposalSubmit, "volumes[]", jsonFields(narrative.VolumeEdit{})},
		{ToolProposalSubmit, "arcs[]", jsonFields(narrative.ArcEdit{})},
		{ToolProposalSubmit, "chapters[]", jsonFields(narrative.ChapterEdit{})},
		{ToolProposalSubmit, "entities[]", jsonFields(narrative.EntityEdit{})},
		{ToolProposalSubmit, "facts[]", jsonFields(narrative.FactEdit{})},
		{ToolProposalSubmit, "confirm_facts[]", jsonFields(narrative.FactRef{})},
		{ToolProposalSubmit, "remove_facts[]", jsonFields(narrative.FactRef{})},
	}
	for _, definition := range definitions {
		for _, tool := range definition.Worker.Tools {
			var schema map[string]any
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				t.Fatalf("%s schema: %v", tool.Name, err)
			}
			for _, c := range cases {
				if c.tool != tool.Name {
					continue
				}
				properties, err := schemaProperties(schema, c.path)
				if err != nil {
					continue // 该 Worker 不公开这条路径
				}
				for name := range properties {
					if !slices.Contains(c.fields, name) {
						t.Errorf("%s/%s/%s 公开了宿主不解码的字段 %s", definition.Worker.ID, c.tool, c.path, name)
					}
				}
			}
		}
	}
}

func jsonFields(value any) []string {
	var names []string
	domain := reflect.TypeOf(value)
	for i := 0; i < domain.NumField(); i++ {
		name := strings.Split(domain.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			names = append(names, name)
		}
	}
	return names
}

// schemaProperties 沿 "a.b[]" 这样的路径取到目标对象的 properties。
func schemaProperties(schema map[string]any, path string) (map[string]any, error) {
	node := schema
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			break
		}
		properties, ok := node["properties"].(map[string]any)
		if !ok {
			return nil, errSchemaPath(path, segment, "上一级没有 properties")
		}
		name, isArray := strings.CutSuffix(segment, "[]")
		child, ok := properties[name].(map[string]any)
		if !ok {
			return nil, errSchemaPath(path, segment, "schema 里没有这个字段")
		}
		if isArray {
			if child, ok = child["items"].(map[string]any); !ok {
				return nil, errSchemaPath(path, segment, "数组没有 items")
			}
		}
		node = child
	}
	properties, ok := node["properties"].(map[string]any)
	if !ok {
		return nil, errSchemaPath(path, path, "目标不是带 properties 的对象")
	}
	return properties, nil
}

type schemaPathError struct{ path, segment, reason string }

func (e schemaPathError) Error() string {
	return "路径 " + e.path + " 在 " + e.segment + " 处断开：" + e.reason
}

func errSchemaPath(path, segment, reason string) error {
	return schemaPathError{path, segment, reason}
}
