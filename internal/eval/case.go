// Package eval is ainovel-cli's offline evaluation harness.
//
// The design starting point: the evaluators (the deterministic diagnostics diag, the whole-book stylestat, the seven-dimension
// rubric) already exist in the project, so eval is only a thin layer — it drives cases in batches, collects the output, and maps
// diag Findings and case contracts onto gates, then aggregates a report. One definition of the facts, never re-judged in the evaluation layer. See docs/evaluation-system.md.
//
// The deterministic mainline currently covered: single-path gates, baseline/variant A/B deltas, repeat aggregation and stylestat regression.
// LLM Judge remains an optional later layer and must not pollute the deterministic gates.
package eval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// caseIDPattern restricts the case id to safe characters: the id is concatenated into the output directory and cleaned by RunCase's RemoveAll,
// so path characters like . and / are forbidden, ruling out "../" path traversal that would delete outside the workspace.
var caseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

const defaultDeltaRatio = 0.3

// Case is one evaluation sample: a writing requirement plus a set of fact-level assertions.
type Case struct {
	ID            string   `json:"id"`
	Category      string   `json:"category"`       // evaluation layer: smoke/workflow/quality/longform/recovery/steering
	Role          string   `json:"role,omitempty"` // role under test: writer/architect/editor (orthogonal to Category)
	Description   string   `json:"description,omitempty"`
	Prompt        string   `json:"prompt"`                   // the user's writing requirement
	Style         string   `json:"style,omitempty"`          // the config style being overridden
	MaxChapters   int      `json:"max_chapters"`             // chapter ceiling; 0 means run only until planning completes (entering writing)
	TargetPrompts []string `json:"target_prompts,omitempty"` // the prompt files this case mainly exercises (informational)
	Rubric        string   `json:"rubric,omitempty"`         // LLM Judge scorecard (enabled in Phase 3)
	Expect        Expect   `json:"expect"`
	Gate          Gate     `json:"gate"`
}

// Expect holds the case-level contract assertions — it only declares the expectations that diag's general rules cannot cover and that are strongly tied to this case.
type Expect struct {
	Phase                string   `json:"phase,omitempty"`                  // the expected final phase
	MinCompletedChapters int      `json:"min_completed_chapters,omitempty"` // the minimum number of chapters that must complete
	RequiredCheckpoints  []string `json:"required_checkpoints,omitempty"`   // of the form "chapter:1:commit" / "arc:1:1:arc_summary" / "global:layered_outline"
	NoPending            []string `json:"no_pending,omitempty"`             // signals that must be cleared at the end: pending_commit/pending_steer/last_commit/last_review
}

// Gate holds this case's gate thresholds. Only MaxSeverity is used in this phase; the remaining fields are reserved for the A/B (regression) phase,
// they are parsed but do not take part in gating — they are kept so case files can be written against the full schema in docs/evaluation-system.md.
type Gate struct {
	MaxSeverity string `json:"max_severity,omitempty"` // the highest severity a diag Finding may have (default warning): anything above it is a hard fail

	MaxCostDeltaRatio     *float64 `json:"max_cost_delta_ratio,omitempty"`
	MaxToolCallDeltaRatio *float64 `json:"max_tool_call_delta_ratio,omitempty"`
	StylestatRegression   string   `json:"stylestat_regression,omitempty"`
}

// Validate checks the case's required fields.
func (c *Case) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("case 缺少 id")
	}
	if !caseIDPattern.MatchString(c.ID) {
		return fmt.Errorf("case id 非法 %q：仅允许小写字母/数字/下划线/连字符，且不含路径字符", c.ID)
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("case %q 缺少 prompt", c.ID)
	}
	if c.Gate.MaxSeverity == "" {
		c.Gate.MaxSeverity = "warning"
	}
	if !validSeverity(c.Gate.MaxSeverity) {
		return fmt.Errorf("case %q 的 gate.max_severity 非法: %s", c.ID, c.Gate.MaxSeverity)
	}
	if c.Gate.MaxCostDeltaRatio == nil {
		c.Gate.MaxCostDeltaRatio = float64Ptr(defaultDeltaRatio)
	}
	if c.Gate.MaxToolCallDeltaRatio == nil {
		c.Gate.MaxToolCallDeltaRatio = float64Ptr(defaultDeltaRatio)
	}
	if c.Gate.StylestatRegression == "" {
		c.Gate.StylestatRegression = "warn"
	}
	if !validStylestatGate(c.Gate.StylestatRegression) {
		return fmt.Errorf("case %q 的 gate.stylestat_regression 非法: %s", c.ID, c.Gate.StylestatRegression)
	}
	return nil
}

func float64Ptr(v float64) *float64 { return &v }

func validStylestatGate(s string) bool {
	switch s {
	case "warn", "block", "off":
		return true
	default:
		return false
	}
}

// LoadCases loads cases from a single .json file or from a directory. Under a directory every *.json is loaded recursively, sorted by id.
func LoadCases(path string) ([]Case, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var files []string
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".json") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		files = []string{path}
	}

	var cases []Case
	seen := map[string]string{}
	for _, f := range files {
		c, err := loadCaseFile(f)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[c.ID]; dup {
			return nil, fmt.Errorf("case id 重复: %q（%s 与 %s）", c.ID, prev, f)
		}
		seen[c.ID] = f
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("未找到任何 case: %s", path)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

func loadCaseFile(path string) (Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Case{}, err
	}
	var c Case
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields() // a mistyped field errors out immediately instead of being silently ignored
	if err := dec.Decode(&c); err != nil {
		return Case{}, fmt.Errorf("解析 case %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Case{}, err
	}
	return c, nil
}
