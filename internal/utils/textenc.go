package utils

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// DecodeText decodes the bytes of a user-supplied text file into UTF-8: on invalid UTF-8 it transcodes from GB18030
// (a superset of GBK) -- most Chinese web-novel txt files are GBK-encoded and read straight as UTF-8 they come out
// as pure mojibake. Byte sequences that are not GBK are replaced with U+FFFD by the decoder (mojibake anyway, which the caller's
// zero-match fallback error guides the user about). Finally the UTF-8 BOM is stripped (otherwise it would ride along in start-of-line matches).
func DecodeText(data []byte) string {
	if !utf8.Valid(data) {
		if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
			data = decoded
		}
	}
	return strings.TrimPrefix(string(data), "\uFEFF")
}
