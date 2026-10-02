package diag

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

const (
	logTailCap   = 200 << 10 // only the last 200KB of the log is taken (a loop is a near-term phenomenon)
	sessionTail  = 80        // the number of skeleton tail entries (to see the dispatch order)
	repeatWindow = 150       // the repeat aggregation only looks at this many recent events - during a long run a normal tool accumulates hundreds of calls,
	// while a real loop is highly concentrated in the near term; using a window instead of a total avoids misjudging "normal progress" as a "dead loop".
	recentAgents = 2  // the number of additionally scanned recently active sub-agent sessions
	repeatMin    = 3  // how many repeats count as a "high-frequency signal"
	repeatTopN   = 12 // the maximum number of repeat signatures listed
)

// RuntimeCapture is the redacted result of one runtime capture. It carries runtime signals only;
// the writing state such as phase/flow/chapter is carried by Report.Stats and is not repeated here.
type RuntimeCapture struct {
	GoOS, GoArch  string
	Models        []RoleModel  // the provider/model actually in effect per session (collected from _meta)
	CurrentStep   string       // the newest checkpoint: scope.step
	StuckStep     string       // the same step repeated at the tail; "" = not stuck
	StuckCount    int          // the repeat count
	Repeats       []RepeatStat // top-N repeat signatures (a loop signal)
	DupContent    []DupStat    // the same sha text appearing repeatedly (the same passage generated over and over)
	LogKinds      map[string]int
	LogErrors     int
	LogWarns      int
	StopGuard     int
	Tail          []SkelEvent // the last N skeleton entries (to see the order)
	RedactedTexts int         // the total number of masked text blocks (redaction self-check)
	Sources       []string    // the sources actually read (self-check)
}

// RoleModel records the provider/model a session actually used.
type RoleModel struct {
	Agent, Provider, Model string
}

// RepeatStat is one repeat signature and its count.
type RepeatStat struct {
	Sig   string
	Count int
}

// DupStat is how often the same redacted text appears.
type DupStat struct {
	Sha   string
	Count int
}

// sessionLine parses one line of sessions/*.jsonl: an embedded agentcore.Message + optional _meta.
type sessionLine struct {
	agentcore.Message
	Meta *struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	} `json:"_meta"`
}

var kindRe = regexp.MustCompile(`kind=(\S+)`)

// CaptureRuntime captures the runtime signals read-only from the output directory and aggregates them in redacted form.
// A missing source degrades safely (no error); it is best-effort.
func CaptureRuntime(s *store.Store) RuntimeCapture {
	rc := RuntimeCapture{GoOS: runtime.GOOS, GoArch: runtime.GOARCH, LogKinds: map[string]int{}}

	rc.CurrentStep, rc.StuckStep, rc.StuckCount = analyzeCheckpoints(s.Checkpoints.All())
	captureSessions(s.Dir(), &rc)
	captureLog(s.Dir(), &rc)
	return rc
}

// analyzeCheckpoints takes the newest step and computes the same step repeated at the tail (a stuck signal).
func analyzeCheckpoints(cps []domain.Checkpoint) (current, stuck string, count int) {
	if len(cps) == 0 {
		return "", "", 0
	}
	key := func(c domain.Checkpoint) string { return fmt.Sprintf("%s.%s", c.Scope, c.Step) }
	current = key(cps[len(cps)-1])
	n := 1
	for i := len(cps) - 2; i >= 0; i-- {
		if key(cps[i]) == current {
			n++
		} else {
			break
		}
	}
	if n >= repeatMin {
		stuck, count = current, n
	}
	return current, stuck, count
}

// captureSessions scans the recently active Worker sessions and aggregates them in redacted form.
func captureSessions(dir string, rc *RuntimeCapture) {
	sessDir := filepath.Join(dir, "meta", "sessions")
	files := sessionFiles(sessDir)

	repeats := map[string]int{}
	dups := map[string]int{}
	models := map[string]RoleModel{}

	for _, f := range files {
		evs := scanSession(filepath.Join(sessDir, f.path), f.agent, rc, models)
		// The aggregation only looks at a near-term window: during a long run a subagent/novel_context accumulating hundreds of times is normal progress,
		// not a loop; a real dead loop is highly concentrated in the near term.
		aggregateRepeats(f.agent, tailEvents(evs, repeatWindow), repeats, dups)
		// files are ordered by activity time descending; the first non-empty session is taken as the current scene.
		if len(rc.Tail) == 0 && len(evs) > 0 {
			rc.Tail = tailEvents(evs, sessionTail)
		}
		rc.Sources = append(rc.Sources, "sessions/"+f.path)
	}

	rc.Repeats = topRepeats(repeats)
	rc.DupContent = topDups(dups)
	rc.Models = sortedModels(models)
}

type sessionFile struct {
	path  string // relative to sessDir
	agent string
}

// sessionFiles returns the recently active Worker sessions.
func sessionFiles(sessDir string) []sessionFile {
	agentsDir := filepath.Join(sessDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil
	}
	type withTime struct {
		name string
		mod  int64
	}
	var agents []withTime
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err == nil {
			agents = append(agents, withTime{e.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].mod > agents[j].mod })
	out := make([]sessionFile, 0, min(len(agents), recentAgents))
	for i, a := range agents {
		if i >= recentAgents {
			break
		}
		stem := strings.TrimSuffix(a.name, ".jsonl")
		out = append(out, sessionFile{path: filepath.Join("agents", a.name), agent: stem})
	}
	return out
}

// scanSession reads one session file, redacts it line by line and collects the event sequence plus the per-agent model.
// The repeat/same-text aggregation is not done here - aggregateRepeats computes it over the near-term window.
func scanSession(path, agent string, rc *RuntimeCapture, models map[string]RoleModel) []SkelEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var evs []SkelEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var sl sessionLine
		if json.Unmarshal(sc.Bytes(), &sl) != nil {
			continue
		}
		ev := redactMessage(agent, sl.Message)
		evs = append(evs, ev)
		rc.RedactedTexts += ev.Redacted
		if sl.Meta != nil && (sl.Meta.Provider != "" || sl.Meta.Model != "") {
			models[agent] = RoleModel{Agent: agent, Provider: sl.Meta.Provider, Model: sl.Meta.Model}
		}
	}
	return evs
}

// aggregateRepeats accumulates the repeat signatures and same-text blocks over the given event window.
func aggregateRepeats(agent string, evs []SkelEvent, repeats, dups map[string]int) {
	for _, ev := range evs {
		for _, t := range ev.Tools {
			sig := agent + " · " + t.Name
			if t.Invalid {
				sig += " (args invalid)"
			}
			repeats[sig]++
		}
		if ev.ErrClass != "" {
			repeats[agent+" · err: "+ev.ErrClass]++
		}
		if ev.TextSha != "" {
			dups[ev.TextSha]++
		}
	}
}

func tailEvents(evs []SkelEvent, n int) []SkelEvent {
	if len(evs) <= n {
		return evs
	}
	return evs[len(evs)-n:]
}

// captureLog reads the tail of the log and aggregates only structural signals (kind/error/warn/stop_guard),
// it never puts the raw log lines into the package - Detail may carry body text.
func captureLog(dir string, rc *RuntimeCapture) {
	path := filepath.Join(dir, "logs", "tui.log")
	tail, ok := readTail(path)
	if !ok {
		path = filepath.Join(dir, "logs", "headless.log")
		tail, ok = readTail(path)
	}
	if !ok {
		return
	}
	rc.Sources = append(rc.Sources, "logs/"+filepath.Base(path)+i18n.T(" (phần cuối)"))

	sc := bufio.NewScanner(bytes.NewReader(tail))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, "level=ERROR"):
			rc.LogErrors++
		case strings.Contains(line, "level=WARN"):
			rc.LogWarns++
		}
		if m := kindRe.FindStringSubmatch(line); m != nil {
			rc.LogKinds[m[1]]++
		}
		if strings.Contains(line, "stop_guard") {
			rc.StopGuard++
		}
	}
}

// readTail reads the last logTailCap bytes of a file and discards the first, possibly truncated, half-line.
func readTail(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false
	}
	size := info.Size()
	var off int64
	if size > logTailCap {
		off = size - logTailCap
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	if off > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	return data, true
}

func topRepeats(m map[string]int) []RepeatStat {
	var out []RepeatStat
	for sig, c := range m {
		if c >= repeatMin {
			out = append(out, RepeatStat{Sig: sig, Count: c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Sig < out[j].Sig
	})
	if len(out) > repeatTopN {
		out = out[:repeatTopN]
	}
	return out
}

func topDups(m map[string]int) []DupStat {
	var out []DupStat
	for sha, c := range m {
		if c >= repeatMin {
			out = append(out, DupStat{Sha: sha, Count: c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Sha < out[j].Sha
	})
	return out
}

func sortedModels(m map[string]RoleModel) []RoleModel {
	out := make([]RoleModel, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out
}
