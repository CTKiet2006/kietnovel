package eval

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/entry/startup"
	"github.com/CTKiet2006/kietnovel/internal/host"
)

// RunOptions controls a single case run.
type RunOptions struct {
	OutputDir string        // isolated output directory (required)
	Timeout   time.Duration // wall-clock ceiling for a single case; 0 means unlimited
	Progress  io.Writer     // progress line output (optional, nil means nothing is printed)
}

// RunCase drives one case: assemble the host → start → advance up to the chapter ceiling → Abort when the ceiling is hit.
// bundle has already had the variant overrides applied by the caller (if any). The returned error is the "runtime error" (the basis for a hard fail);
// a normal finish or a normal capped stop both return nil.
//
// RunCase exclusively owns and resets OutputDir: StartPrepared only resets progress/checkpoints and does not clear chapters/
// foundation and the other artifacts; reusing an old directory lets residual artifacts pollute diag and novel_context. So it is cleared before the run, to guarantee isolation.
func RunCase(cfg bootstrap.Config, bundle assets.Bundle, c Case, opts RunOptions) error {
	if strings.TrimSpace(opts.OutputDir) == "" {
		return fmt.Errorf("RunCase: 缺少 OutputDir")
	}
	if err := os.RemoveAll(opts.OutputDir); err != nil {
		return fmt.Errorf("清理输出目录: %w", err)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录: %w", err)
	}
	cfg.OutputDir = opts.OutputDir
	if c.Style != "" {
		cfg.Style = c.Style
	}

	eng, err := host.New(cfg, bundle, host.WithFileLog("headless.log", false))
	if err != nil {
		return fmt.Errorf("装配 host: %w", err)
	}
	defer eng.Close()
	if logErr := eng.FileLogError(); logErr != nil {
		return fmt.Errorf("评测文件日志不可用: %w", logErr)
	}

	prompt, err := startup.PrepareQuick(c.Prompt)
	if err != nil {
		return err
	}
	if err := eng.PrepareUserRules(prompt); err != nil {
		return fmt.Errorf("准备用户规则: %w", err)
	}
	if err := eng.StartPrepared(prompt); err != nil {
		return fmt.Errorf("启动: %w", err)
	}

	return drive(eng, c.MaxChapters, opts)
}

// driveEngine is the minimal engine interface that drive consumes (*host.Host satisfies it naturally). It was extracted so that
// the drain-to-Done discipline can be covered by a deterministic test — this concurrent logic once fell into a send-on-closed-channel trap.
type driveEngine interface {
	Events() <-chan host.Event
	Stream() <-chan string
	Done() <-chan struct{}
	Snapshot() host.UISnapshot
	Abort() bool
}

// drive consumes the engine event stream, aborts on reaching the chapter ceiling or on timeout, and waits for Done to wrap up.
//
// Key discipline: whether it finishes normally, hits the chapter ceiling or times out, it must drain to Done before returning. The host's background waitDone
// sends to done once, while eng.Close() (RunCase's defer) closes done — returning early triggers Close,
// which races with waitDone's send over the channel close and panics (send on closed channel). headless relies on the same "Done
// first, Close second". It must also drain Events and Stream to avoid blocking the engine.
func drive(eng driveEngine, maxChapters int, opts RunOptions) error {
	var timeoutCh <-chan time.Time
	if opts.Timeout > 0 {
		t := time.NewTimer(opts.Timeout)
		defer t.Stop()
		timeoutCh = t.C
	}

	aborted, timedOut := false, false
	// finish is called after draining to Done (or after the channel closes): on timeout it returns an error, otherwise it ends normally.
	finish := func() error {
		if timedOut {
			return fmt.Errorf("运行超时（%s）", opts.Timeout)
		}
		return nil
	}
	for {
		select {
		case ev, ok := <-eng.Events():
			if !ok {
				return finish()
			}
			if opts.Progress != nil && strings.TrimSpace(ev.Summary) != "" {
				fmt.Fprintf(opts.Progress, "    [%s] %s\n", ev.Category, ev.Summary)
			}
			if !aborted && capReached(eng.Snapshot(), maxChapters) {
				eng.Abort()
				aborted = true
				timeoutCh = nil // the capped-stop condition is reached, so switch to a normal wrap-up and drop the timeout bound (avoiding misreading a successful capped stop as a timeout)
			}
		case <-eng.Stream():
			// Drain the streaming deltas without consuming the content — eval does not care about the body stream, only about persisted facts.
		case _, ok := <-eng.Done():
			if !ok {
				return finish()
			}
			return finish()
		case <-timeoutCh:
			eng.Abort() // aborted must be false here (a cap stop nils out timeoutCh)
			aborted, timedOut = true, true
			timeoutCh = nil // disable the timer, keep draining until Done, and let finish return the timeout error
		}
	}
}

// capReached reports whether the capped-stop condition is met. maxChapters>0 uses the number of completed chapters; <=0 is treated as "planning-type",
// where planning completing (entering writing or already complete) is enough to stop.
func capReached(snap host.UISnapshot, maxChapters int) bool {
	if maxChapters <= 0 {
		return snap.Phase == string(domain.PhaseWriting) || snap.Phase == string(domain.PhaseComplete)
	}
	return snap.CompletedCount >= maxChapters
}
