package rules

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RawSource is a raw source awaiting normalization (the whole text of a rules file).
//
// After dropping YAML a rules file is just a plain natural-language prompt; normalization needs only the raw text and no longer parses front matter.
type RawSource struct {
	Label string     // source label, lands in Snapshot.Sources (e.g. global:my-style.md)
	Kind  SourceKind // priority tier
	Text  string     // raw file contents
}

// RawFileSources enumerates the .md files under the rules directories in Global -> Project order and returns their raw text.
//
// It follows the same scanning convention as readDirFromDisk (top-level .md, lexicographic order, hidden files skipped) but does not parse YAML;
// the whole text goes to the normalizer verbatim. System defaults / the startup prompt / runtime requests are supplied separately by the service.
func RawFileSources(opts LoadOptions) []RawSource {
	var out []RawSource
	out = append(out, rawDir(opts.HomeRulesDir, SourceGlobal)...)
	out = append(out, rawDir(opts.ProjectRulesDir, SourceProject)...)
	return out
}

func rawDir(dir string, kind SourceKind) []RawSource {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A missing directory is normal and skipped silently, but errors like a permission problem or a path that is really a file must leave a trace --
		// otherwise the user writes rules that silently never take effect, with zero feedback and an extremely high debugging cost (see known_rules_path_stale_readme).
		if !os.IsNotExist(err) {
			slog.Warn("规则目录读取失败，已跳过", "module", "rules", "dir", dir, "err", err)
		}
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var out []RawSource
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("规则文件读取失败，已跳过", "module", "rules", "file", path, "err", err)
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		out = append(out, RawSource{
			Label: kind.String() + ":" + name,
			Kind:  kind,
			Text:  text,
		})
	}
	return out
}
