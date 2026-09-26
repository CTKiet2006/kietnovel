package assets

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/infra/capability/pack"
	"github.com/voocel/ainovel-cli/internal/infra/capability/prompt"
)

// §7.2：每个内置 Worker 用到的 Slot 都必须有官方基线文本，官方包也不声明无人使用的 Slot。
func TestOfficialPackCoversEveryBuiltinSlot(t *testing.T) {
	loaded, err := pack.LoadFS(OfficialPack())
	if err != nil {
		t.Fatalf("load official pack: %v", err)
	}
	definitions, err := prompt.BuiltinCapabilities()
	if err != nil {
		t.Fatalf("builtin capabilities: %v", err)
	}
	used := make(map[string]bool)
	for _, definition := range definitions {
		for _, slot := range definition.Worker.PromptSlots {
			used[string(slot)] = true
		}
	}
	for slot := range used {
		if strings.TrimSpace(loaded.Manifest.PromptOverlays[slot]) == "" {
			t.Errorf("官方包缺少 Slot %s 的基线文本", slot)
		}
	}
	for slot := range loaded.Manifest.PromptOverlays {
		if !used[slot] {
			t.Errorf("官方包声明了没有 Worker 使用的 Slot %s", slot)
		}
	}
}
