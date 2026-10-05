package chapterfacts

import (
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/voocel/agentcore/schema"
)

// Properties returns the JSON Schema fields shared by complete chapter facts (default Chinese).
func Properties(includeFeedback bool) []schema.Prop {
	return PropertiesForLanguage(includeFeedback, "zh")
}

// PropertiesForLanguage returns the localized JSON Schema fields for chapter facts.
func PropertiesForLanguage(includeFeedback bool, lang string) []schema.Prop {
	cleanLang := strings.ToLower(strings.TrimSpace(lang))

	textListDesc := func(zh, vi, en string) func(string) map[string]any {
		return func(override string) map[string]any {
			desc := zh
			switch cleanLang {
			case "vi":
				desc = vi
			case "en":
				desc = en
			}
			if override != "" {
				desc = override
			}
			return schema.Array(desc, schema.String(desc))
		}
	}

	timeDesc, eventDesc, relADesc, relBDesc, relDesc := "故事内时间", "事件", "角色 A", "角色 B", "本章结束时关系"
	fsIdDesc, fsActDesc, fsDescDesc := "伏笔 ID", "操作", "plant 描述，其它操作为 null"
	stEntityDesc, stFieldDesc, stOldDesc, stNewDesc, stReasonDesc := "实体", "属性", "变化前值", "变化后值", "原因"
	titleDesc, summaryDesc, hookDesc, strandDesc := "最终标题", "章节摘要", "章末钩子", "主导叙事线"
	tlEventsDesc, fsUpdatesDesc, relChangesDesc, stChangesDesc, castDesc := "时间线事件", "伏笔操作", "关系变化", "状态变化", "新配角"
	castNameDesc, castRoleDesc := "姓名", "定位"
	devDesc, sugDesc, fbDesc := "偏离大纲的描述", "对后续大纲的调整建议", "对后续大纲的建议对象；必须直接传 JSON object，不要传字符串化 JSON"

	switch cleanLang {
	case "vi":
		timeDesc, eventDesc, relADesc, relBDesc, relDesc = "Thời gian trong truyện", "Sự kiện", "Nhân vật A", "Nhân vật B", "Quan hệ khi kết thúc chương"
		fsIdDesc, fsActDesc, fsDescDesc = "Mã phục bút", "Thao tác", "Mô tả khi plant, thao tác khác là null"
		stEntityDesc, stFieldDesc, stOldDesc, stNewDesc, stReasonDesc = "Thực thể", "Thuộc tính", "Giá trị cũ", "Giá trị mới", "Nguyên nhân"
		titleDesc, summaryDesc, hookDesc, strandDesc = "Tiêu đề chính thức", "Tóm tắt chương", "Móc câu cuối chương", "Tuyến tự sự chủ đạo"
		tlEventsDesc, fsUpdatesDesc, relChangesDesc, stChangesDesc, castDesc = "Sự kiện dòng thời gian", "Cập nhật phục bút", "Thay đổi quan hệ", "Thay đổi trạng thái", "Nhân vật phụ mới"
		castNameDesc, castRoleDesc = "Họ tên", "Định vị vai trò"
		devDesc, sugDesc, fbDesc = "Mô tả điểm lệch dàn ý", "Đề xuất điều chỉnh dàn ý tiếp theo", "Đối tượng đề xuất cho dàn ý tiếp theo; truyền trực tiếp JSON object"
	case "en":
		timeDesc, eventDesc, relADesc, relBDesc, relDesc = "In-story time", "Event", "Character A", "Character B", "Relationship at chapter end"
		fsIdDesc, fsActDesc, fsDescDesc = "Foreshadow ID", "Action", "Description for plant, null for other actions"
		stEntityDesc, stFieldDesc, stOldDesc, stNewDesc, stReasonDesc = "Entity", "Field", "Old value", "New value", "Reason"
		titleDesc, summaryDesc, hookDesc, strandDesc = "Final title", "Chapter summary", "End-of-chapter hook", "Dominant narrative strand"
		tlEventsDesc, fsUpdatesDesc, relChangesDesc, stChangesDesc, castDesc = "Timeline events", "Foreshadowing updates", "Relationship changes", "State changes", "New cast intros"
		castNameDesc, castRoleDesc = "Name", "Role"
		devDesc, sugDesc, fbDesc = "Deviation description", "Suggestions for outline adjustment", "Outline feedback object; pass JSON object directly"
	}

	charList := textListDesc("涉及角色", "Nhân vật liên quan", "Characters involved")("")
	castList := textListDesc("出场角色", "Nhân vật xuất hiện", "Characters present")("")
	eventsList := textListDesc("关键事件", "Sự kiện then chốt", "Key events")("")

	timeline := schema.Object(
		schema.Property("time", schema.String(timeDesc)).Required(),
		schema.Property("event", schema.String(eventDesc)).Required(),
		schema.Property("characters", charList).Required(),
	)
	foreshadow := schema.Object(
		schema.Property("id", schema.String(fsIdDesc)).Required(),
		schema.Property("action", schema.Enum(fsActDesc, "plant", "advance", "resolve")).Required(),
		schema.Property("description", llmcontract.Nullable(schema.String(fsDescDesc))).Required(),
	)
	relationship := schema.Object(
		schema.Property("character_a", schema.String(relADesc)).Required(),
		schema.Property("character_b", schema.String(relBDesc)).Required(),
		schema.Property("relation", schema.String(relDesc)).Required(),
	)
	stateChange := schema.Object(
		schema.Property("entity", schema.String(stEntityDesc)).Required(),
		schema.Property("field", schema.String(stFieldDesc)).Required(),
		schema.Property("old_value", llmcontract.Nullable(schema.String(stOldDesc))).Required(),
		schema.Property("new_value", schema.String(stNewDesc)).Required(),
		schema.Property("reason", llmcontract.Nullable(schema.String(stReasonDesc))).Required(),
	)
	props := []schema.Prop{
		schema.Property("title", schema.String(titleDesc)).Required(),
		schema.Property("summary", schema.String(summaryDesc)).Required(),
		schema.Property("characters", castList).Required(),
		schema.Property("key_events", eventsList).Required(),
		schema.Property("timeline_events", schema.Array(tlEventsDesc, timeline)).Required(),
		schema.Property("foreshadow_updates", schema.Array(fsUpdatesDesc, foreshadow)).Required(),
		schema.Property("relationship_changes", schema.Array(relChangesDesc, relationship)).Required(),
		schema.Property("state_changes", schema.Array(stChangesDesc, stateChange)).Required(),
		schema.Property("cast_intros", schema.Array(castDesc, schema.Object(
			schema.Property("name", schema.String(castNameDesc)).Required(),
			schema.Property("brief_role", schema.String(castRoleDesc)).Required(),
		))).Required(),
		schema.Property("hook_type", llmcontract.Nullable(schema.Enum(hookDesc, domain.HookTypes()...))).Required(),
		schema.Property("dominant_strand", llmcontract.Nullable(schema.Enum(strandDesc, domain.DominantStrands()...))).Required(),
	}
	if includeFeedback {
		feedback := schema.Object(
			schema.Property("deviation", schema.String(devDesc)).Required(),
			schema.Property("suggestion", schema.String(sugDesc)).Required(),
		)
		feedback["description"] = fbDesc
		props = append(props, schema.Property("feedback", llmcontract.Nullable(feedback)).Required())
	}
	return props
}

// Validate checks the deterministic constraints shared by normal commits and manual revisions.
func Validate(facts domain.ChapterFacts) error {
	if strings.TrimSpace(facts.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(facts.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if len(facts.KeyEvents) == 0 {
		return fmt.Errorf("key_events must contain at least one event")
	}
	if err := validateTextItems("characters", facts.Characters); err != nil {
		return err
	}
	if err := validateTextItems("key_events", facts.KeyEvents); err != nil {
		return err
	}
	for i, event := range facts.TimelineEvents {
		if strings.TrimSpace(event.Time) == "" || strings.TrimSpace(event.Event) == "" {
			return fmt.Errorf("timeline_events[%d] requires time and event", i)
		}
		if err := validateTextItems(fmt.Sprintf("timeline_events[%d].characters", i), event.Characters); err != nil {
			return err
		}
	}
	for i, update := range facts.ForeshadowUpdates {
		if strings.TrimSpace(update.ID) == "" {
			return fmt.Errorf("foreshadow_updates[%d].id is required", i)
		}
		switch update.Action {
		case "plant":
			if strings.TrimSpace(update.Description) == "" {
				return fmt.Errorf("foreshadow_updates[%d] plant requires description", i)
			}
		case "advance", "resolve":
		default:
			return fmt.Errorf("foreshadow_updates[%d].action invalid: %q", i, update.Action)
		}
	}
	for i, change := range facts.RelationshipChanges {
		if strings.TrimSpace(change.CharacterA) == "" || strings.TrimSpace(change.CharacterB) == "" || strings.TrimSpace(change.Relation) == "" {
			return fmt.Errorf("relationship_changes[%d] requires character_a, character_b and relation", i)
		}
		if change.CharacterA == change.CharacterB {
			return fmt.Errorf("relationship_changes[%d] cannot relate a character to itself", i)
		}
	}
	for i, change := range facts.StateChanges {
		if strings.TrimSpace(change.Entity) == "" || strings.TrimSpace(change.Field) == "" || strings.TrimSpace(change.NewValue) == "" {
			return fmt.Errorf("state_changes[%d] requires entity, field and new_value", i)
		}
	}
	for i, intro := range facts.CastIntros {
		if strings.TrimSpace(intro.Name) == "" || strings.TrimSpace(intro.BriefRole) == "" {
			return fmt.Errorf("cast_intros[%d] requires name and brief_role", i)
		}
	}
	if facts.HookType != "" && !domain.ValidHookType(facts.HookType) {
		return fmt.Errorf("invalid hook_type %q", facts.HookType)
	}
	if facts.DominantStrand != "" && !domain.ValidDominantStrand(facts.DominantStrand) {
		return fmt.Errorf("invalid dominant_strand %q", facts.DominantStrand)
	}
	if facts.Feedback != nil && (strings.TrimSpace(facts.Feedback.Deviation) == "" || strings.TrimSpace(facts.Feedback.Suggestion) == "") {
		return fmt.Errorf("feedback requires deviation and suggestion")
	}
	return nil
}

func validateTextItems(name string, items []string) error {
	for i, item := range items {
		if strings.TrimSpace(item) == "" {
			return fmt.Errorf("%s[%d] cannot be empty", name, i)
		}
	}
	return nil
}
