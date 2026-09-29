package rules

import (
	"regexp"
	"strings"
)

// Lint is the built-in product floor check: it scans the body for machinery residue, is unrelated to user rules, and always runs at commit time.
// It shares Check's contract -- facts only (iron law one), no flow blocking, adjudicated by review/the user.
//
// The current three kinds (all empirically observed defects from real long-run output):
//   - markdown_residue: leftover ** bolding and # heading lines outside the first line (exporting to txt would expose the raw symbols)
//   - non_cjk_fragments: runs of Latin letters (the model mixing languages, e.g. a bare "pattern" inside Chinese prose)
func Lint(text string) []Violation {
	var vs []Violation
	vs = appendMarkdownResidue(vs, text)
	vs = appendNonCJKFragments(vs, text)
	return vs
}

func appendMarkdownResidue(vs []Violation, text string) []Violation {
	if n := strings.Count(text, "**"); n > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "**",
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	headings := 0
	seenContent := false
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		// A # heading on the first non-empty line is legitimate chapter-file format (not keyed to a line number, so leading blank lines are tolerated)
		first := !seenContent
		seenContent = true
		if !first && strings.HasPrefix(t, "#") {
			headings++
		}
	}
	if headings > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "#",
			Actual:   headings,
			Severity: SeverityWarning,
		})
	}
	return vs
}

var latinFragmentRe = regexp.MustCompile(`[A-Za-z]{2,}`)

// appendNonCJKFragments reports the total count of Latin fragments plus deduplicated examples.
// Legitimate English in contemporary settings (brand names, abbreviations) hits this too -- a warning-level fact that review adjudicates per genre.
func appendNonCJKFragments(vs []Violation, text string) []Violation {
	matches := latinFragmentRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return vs
	}
	seen := make(map[string]struct{})
	var examples []string
	for _, m := range matches {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		if len(examples) < 3 {
			examples = append(examples, m)
		}
	}
	return append(vs, Violation{
		Rule:     "non_cjk_fragments",
		Target:   strings.Join(examples, "、"),
		Actual:   len(matches),
		Severity: SeverityWarning,
	})
}
