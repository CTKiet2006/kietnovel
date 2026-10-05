package utils

// ThinkingSep 是思考文本与正文之间的分隔标记。
// observer 在思考文本段前插入此标记，TUI 据此切换渲染样式。
const ThinkingSep = "\x02"

// TruncateRunes 按 rune 截断并补省略号；n 以内原样返回。
func TruncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
