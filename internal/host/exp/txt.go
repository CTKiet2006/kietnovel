package exp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// chapterTitleIndex looks up a title by chapter number, returning an empty string when it is missing.
type chapterTitleIndex map[int]string

func buildTitleIndex(outline []domain.OutlineEntry) chapterTitleIndex {
	idx := make(chapterTitleIndex, len(outline))
	for _, e := range outline {
		if e.Title != "" {
			idx[e.Chapter] = e.Title
		}
	}
	return idx
}

// chapterLocation is where a chapter sits in the layered outline. Only the volume information the export layout needs is
// kept -- arcs do not enter the export (from a reader's point of view an arc is an over-fine internal structure).
type chapterLocation struct {
	VolumeIdx       int
	VolumeTitle     string
	IsFirstOfVolume bool
}

// buildLocations builds {chapter -> location} following the global chapter order of the layered outline.
// Chapter numbers are rebuilt by the same rules as FlattenOutline (accumulating in volume-then-arc order),
// so they stay consistent with the chapter numbers in Progress.CompletedChapters. The arc level is still traversed (global numbering requires it),
// but it does not land in location -- the export only inserts a separator at a volume start.
func buildLocations(volumes []domain.VolumeOutline) map[int]chapterLocation {
	if len(volumes) == 0 {
		return nil
	}
	locs := make(map[int]chapterLocation)
	ch := 0
	for _, v := range volumes {
		firstOfVol := true
		for _, a := range v.Arcs {
			for range a.Chapters {
				ch++
				locs[ch] = chapterLocation{
					VolumeIdx:       v.Index,
					VolumeTitle:     v.Title,
					IsFirstOfVolume: firstOfVol,
				}
				firstOfVol = false
			}
		}
	}
	return locs
}

// chapterHeaderRe matches a first-line Markdown heading that carries a chapter number (the CJK forms "# 第N章" / "## 第 12 章").
var chapterHeaderRe = regexp.MustCompile(`^#+\s+第.+?章`)

// atxTitleRe extracts the text of an ATX heading (# title).
var atxTitleRe = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)

// stripChapterTitleHeader strips the first line when it is a chapter title that would duplicate the exporter's unified title.
// Two cases: (1) "# 第N章 ..." (carrying a chapter number); (2) a markdown heading whose text is exactly this chapter's title
// (the writer often puts the bare chapter name as a heading on the body's first line, e.g. "# 边村浮生", duplicating the
// exporter-generated "第 N 章 边村浮生"). Other h1s (e.g. "# 序章") count as part of the body and are kept.
// The caller is responsible for TrimSpace first, so leading blank lines are out of scope.
func stripChapterTitleHeader(content, title string) string {
	first, rest, hasNewline := strings.Cut(content, "\n")
	if !isChapterTitleLine(first, title) {
		return content
	}
	if !hasNewline {
		return ""
	}
	return strings.TrimLeft(rest, "\n")
}

func isChapterTitleLine(line, title string) bool {
	if chapterHeaderRe.MatchString(line) {
		return true
	}
	if title = strings.TrimSpace(title); title == "" {
		return false
	}
	m := atxTitleRe.FindStringSubmatch(line)
	return len(m) == 2 && strings.TrimSpace(m[1]) == title
}

// renderTXT concatenates the final text.
//
// Chapter order is given by chapters (the caller has already de-duplicated them in ascending chapter order). bodies/titleIdx/locations
// are all handled as "missing means degrade": without a title only "第 N 章" is emitted, and without a layered location the outline is treated as flat.
func renderTXT(
	novelName string,
	chapters []int,
	titleIdx chapterTitleIndex,
	locations map[int]chapterLocation,
	bodies map[int]string,
) string {
	var b strings.Builder

	if name := strings.TrimSpace(novelName); name != "" {
		b.WriteString("《")
		b.WriteString(name)
		b.WriteString("》\n\n")
	}

	useLayered := len(locations) > 0

	for i, ch := range chapters {
		if useLayered {
			if loc, ok := locations[ch]; ok && loc.IsFirstOfVolume {
				b.WriteString("\n═══════════════════════════════════════════\n")
				fmt.Fprintf(&b, "           第 %d 卷  %s\n", loc.VolumeIdx, strings.TrimSpace(loc.VolumeTitle))
				b.WriteString("═══════════════════════════════════════════\n\n")
			}
		}

		title := strings.TrimSpace(titleIdx[ch])
		if title != "" {
			fmt.Fprintf(&b, "第 %d 章  %s\n\n", ch, title)
		} else {
			fmt.Fprintf(&b, "第 %d 章\n\n", ch)
		}

		body := stripChapterTitleHeader(strings.TrimSpace(bodies[ch]), title)
		b.WriteString(body)
		b.WriteString("\n")
		if i < len(chapters)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}
