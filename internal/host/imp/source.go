package imp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Supported source encoding labels, written into the Manifest and progress events; no silent fallback (RFC §7.1).
const (
	encodingUTF8    = "utf-8"
	encodingUTF8BOM = "utf-8-bom"
	encodingGB18030 = "gb18030"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// decoded is one decode result: text + the encoding actually chosen.
type decoded struct {
	text     string
	encoding string
}

// decodeSource decodes in the order UTF-8 / UTF-8 BOM / GB18030 and returns the chosen encoding.
// It fails outright when the bytes cannot be decoded reliably or a replacement character appears; the error carries the detection result, so a "GB18030 attempt" is never hidden as a silent fallback.
func decodeSource(raw []byte) (decoded, error) {
	if bytes.HasPrefix(raw, utf8BOM) {
		body := raw[len(utf8BOM):]
		if !utf8.Valid(body) {
			return decoded{}, fmt.Errorf("声明 UTF-8 BOM 但内容不是合法 UTF-8")
		}
		return decoded{text: string(body), encoding: encodingUTF8BOM}, nil
	}
	if utf8.Valid(raw) {
		return decoded{text: string(raw), encoding: encodingUTF8}, nil
	}
	out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	if err != nil {
		return decoded{}, fmt.Errorf("既不是合法 UTF-8，GB18030 解码也失败：%w", err)
	}
	if !utf8.Valid(out) {
		return decoded{}, fmt.Errorf("GB18030 解码结果仍非合法 UTF-8，无法可靠解码")
	}
	if i := bytes.IndexRune(out, utf8.RuneError); i >= 0 {
		return decoded{}, fmt.Errorf("GB18030 解码出现替换字符（U+FFFD @ 字节 %d），无法可靠解码；请确认文件编码", i)
	}
	return decoded{text: string(out), encoding: encodingGB18030}, nil
}

// normalize performs only conversions that do not change literary content: CRLF/CR unified to LF.
// It preserves blank lines, indentation, title lines and body characters; it never deletes leading text, empty chapters, ads or so-called trailing noise (RFC §7.2).
// The BOM was already stripped during the decodeSource stage.
func normalize(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// Ingest reads the source file, decodes and normalizes it, and atomically creates the meta/import/ workspace snapshot via directory rename.
// It returns the workspace handle and Manifest; the caller emits progress events from them.
func Ingest(bookDir, sourcePath string, in Intent) (*Workspace, *Manifest, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("读取源文件：%w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, fmt.Errorf("源文件为空：%s", sourcePath)
	}
	dec, err := decodeSource(raw)
	if err != nil {
		return nil, nil, err
	}
	normBytes := []byte(normalize(dec.text))

	m := Manifest{
		Version:          workspaceSchemaVersion,
		SourceName:       filepath.Base(sourcePath),
		RawSHA256:        Digest(raw),
		NormalizedSHA256: Digest(normBytes),
		Encoding:         dec.encoding,
		SizeBytes:        int64(len(raw)),
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if in.Version == 0 {
		in.Version = workspaceSchemaVersion
	}

	ws, err := createWorkspace(bookDir, m, in, normBytes)
	if err != nil {
		return nil, nil, err
	}
	return ws, &m, nil
}

// SourceUnit is a stable coordinate the model can reference (RFC §7.3).
// ID is only for display and model references; every ordering/containment/increase check uses the numeric (Line, Part) order, and lexicographic comparison of ID strings is forbidden.
type SourceUnit struct {
	ID        string `json:"id"`   // L1257；超预算行拆为 L1257.1、L1257.2
	Line      int    `json:"line"` // 1 起
	Part      int    `json:"part"` // 0=整行；虚拟分片 1..N
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	Text      string `json:"text"`
}

// unitLess defines a total order over SourceUnit: Line first, then Part, both compared numerically (A1 amendment).
func unitLess(a, b SourceUnit) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Part < b.Part
}

// buildSourceUnits builds the stable coordinate table from the normalized text.
// A normal line becomes one unit; when a line exceeds maxUnitBytes, several virtual units are generated, split only on UTF-8 character boundaries,
// without rewriting source.txt, inserting soft line breaks, or altering any source character (RFC §7.3). maxUnitBytes<=0 means no splitting.
func buildSourceUnits(normalized []byte, maxUnitBytes int) []SourceUnit {
	var units []SourceUnit
	n := len(normalized)
	line := 0
	offset := 0
	for offset < n {
		nl := bytes.IndexByte(normalized[offset:], '\n')
		lineEnd := n
		if nl >= 0 {
			lineEnd = offset + nl
		}
		line++
		if maxUnitBytes > 0 && lineEnd-offset > maxUnitBytes {
			part := 0
			s := offset
			for s < lineEnd {
				e := s + maxUnitBytes
				if e >= lineEnd {
					e = lineEnd
				} else {
					for e > s && !utf8.RuneStart(normalized[e]) {
						e--
					}
					if e == s { // 单个超长 rune 的极端兜底
						e = s + maxUnitBytes
					}
				}
				part++
				units = append(units, SourceUnit{
					ID: fmt.Sprintf("L%d.%d", line, part), Line: line, Part: part,
					StartByte: s, EndByte: e, Text: string(normalized[s:e]),
				})
				s = e
			}
		} else {
			units = append(units, SourceUnit{
				ID: fmt.Sprintf("L%d", line), Line: line, Part: 0,
				StartByte: offset, EndByte: lineEnd, Text: string(normalized[offset:lineEnd]),
			})
		}
		if nl < 0 {
			break
		}
		offset = lineEnd + 1
	}
	return units
}

// resolveBoundaryByte maps a boundary decision to an exact byte position:
// with no anchor it takes the unit's start; with an anchor it requires a unique verbatim hit inside that unit, then maps to a byte offset (RFC §8.3).
func resolveBoundaryByte(unitByID map[string]SourceUnit, unitID, anchor string) (int, error) {
	u, ok := unitByID[unitID]
	if !ok {
		return 0, fmt.Errorf("边界引用不存在的 unit：%s", unitID)
	}
	if anchor == "" {
		return u.StartByte, nil
	}
	switch strings.Count(u.Text, anchor) {
	case 0:
		return 0, fmt.Errorf("锚点 %q 不在 unit %s 内", anchor, unitID)
	case 1:
		return u.StartByte + strings.Index(u.Text, anchor), nil
	default:
		return 0, fmt.Errorf("锚点 %q 在 unit %s 内不唯一", anchor, unitID)
	}
}
