package tui

import (
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// commandRegistry tra cứu lệnh theo tên. Tên chuẩn (Name) là tiếng Anh và cũng
// là ID nội bộ khi ID trống — runtime chỉ xử lý ID, không xử lý ngôn ngữ.
type commandRegistry struct {
	specs []slashCommandSpec
}

func newCommandRegistry(specs []slashCommandSpec) commandRegistry {
	return commandRegistry{specs: append([]slashCommandSpec(nil), specs...)}
}

func (r commandRegistry) Visible() []slashCommandSpec {
	var out []slashCommandSpec
	for _, spec := range r.specs {
		if !spec.Hidden {
			out = append(out, spec)
		}
	}
	return out
}

func (r commandRegistry) Find(name string) (slashCommandSpec, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return slashCommandSpec{}, false
	}
	for _, spec := range r.specs {
		if spec.matches(name) {
			return spec, true
		}
	}
	return slashCommandSpec{}, false
}

func (r commandRegistry) PaletteItems() []commandPaletteItem {
	return r.PaletteItemsIn(i18n.Language())
}

func (r commandRegistry) PaletteItemsIn(lang string) []commandPaletteItem {
	var items []commandPaletteItem
	for _, spec := range r.Visible() {
		// Name hiển thị theo locale; Aliases giữ tên chuẩn + alias cũ để gõ
		// kiểu nào cũng khớp (người đổi UI không mất lệnh quen thuộc).
		aliases := append([]string{spec.Name}, spec.Aliases...)
		items = append(items, commandPaletteItem{
			Name:        spec.DisplayName(lang),
			Aliases:     aliases,
			Usage:       spec.UsageText(lang),
			Description: spec.Description,
			AutoExecute: spec.AutoExecute,
		})
	}
	return items
}
