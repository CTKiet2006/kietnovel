package utils

// ThinkingSep is the separator between the thinking text and the main body.
// The observer inserts this marker before a thinking segment and the TUI switches render style on it.
const ThinkingSep = "\x02"

// TruncateRunes truncates by rune count and appends an ellipsis; anything within n is returned unchanged.
func TruncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
