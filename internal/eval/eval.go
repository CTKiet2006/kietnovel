package eval

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
)

// Command is the entry point of the `kietnovel eval` subcommand and returns the process exit code:
// 0=PASS/WARN, 1=some case FAILed, 2=usage/config error.
//
// Clear flow: load config → load cases → schedule runs as single/A-B → collect → grade → aggregate → report.
func Command(argv []string) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	casesPath := fs.String("cases", "", "case 目录或单个 .json 文件（必填）")
	variantDir := fs.String("variant", "", "variant prompt 覆盖目录（含 writer.md 等核心提示词）")
	configPath := fs.String("config", "", "配置文件路径（缺省用默认路径）")
	outDir := fs.String("out", "", "报告输出目录（缺省 workspace/evals/<run_id>）")
	maxChapters := fs.Int("max-chapters", -1, "覆盖所有 case 的章数上限（-1=不覆盖）")
	timeout := fs.Duration("timeout", 30*time.Minute, "单 case 墙钟上限（0=不限）")
	repeat := fs.Int("repeat", 1, "每个 case 重复运行次数（降低模型随机性影响）")
	ci := fs.Bool("ci", false, "CI 模式：抑制逐事件进度输出，仅打印最终结论（退出码已反映门禁，无需此 flag 也生效）")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if strings.TrimSpace(*casesPath) == "" {
		fmt.Fprintln(os.Stderr, "eval: 缺少 --cases")
		fs.Usage()
		return 2
	}
	if *repeat <= 0 {
		fmt.Fprintln(os.Stderr, "eval: --repeat 必须大于 0")
		return 2
	}

	// When eval's -config points at a standalone file it is loaded as a single file (reproducible, not polluted by this machine's global/project config);
	// otherwise the default two-layer global+project merge is used.
	loadConfig := bootstrap.LoadConfig
	if strings.TrimSpace(*configPath) != "" {
		loadConfig = func() (bootstrap.Config, error) { return bootstrap.LoadConfigFile(*configPath) }
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: 加载配置失败: %v\n", err)
		return 2
	}

	cases, err := LoadCases(*casesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: 加载 case 失败: %v\n", err)
		return 2
	}

	variantPrompts, err := loadVariant(*variantDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: 加载 variant 失败: %v\n", err)
		return 2
	}

	runID := time.Now().Format("20060102-150405")
	if *outDir == "" {
		*outDir = filepath.Join("workspace", "evals", runID)
	}
	variantName := ""
	if *variantDir != "" {
		variantName = filepath.Base(*variantDir)
	}

	mode := "single"
	if variantName != "" {
		mode = "ab"
	}
	fmt.Fprintf(os.Stderr, "eval run %s · %d cases · mode=%s · variant=%s · repeat=%d\n",
		runID, len(cases), mode, orNone(variantName), *repeat)

	caseResults := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		if *maxChapters >= 0 {
			c.MaxChapters = *maxChapters
		}
		fmt.Fprintf(os.Stderr, "\n▶ %s (%s)\n", c.ID, c.Category)

		style := c.Style
		if style == "" {
			style = cfg.Style
		}
		var progressW io.Writer
		if !*ci {
			progressW = os.Stderr // CI mode prints events silently one by one, keeping the log clean
		}

		if variantName == "" {
			runs := make([]RunResult, 0, *repeat)
			for i := 1; i <= *repeat; i++ {
				bundle := assets.Load(style, assets.LoadOptions{}) // purely built-in, deterministic baseline, not polluted by this machine's overrides
				dir := runDir(*outDir, c.ID, ArmSingle, i, *repeat)
				res := runOne(cfg, bundle, c, dir, *timeout, progressW)
				res.Arm, res.Repeat = ArmSingle, i
				runs = append(runs, RunResult{Arm: ArmSingle, Repeat: i, Result: res})
				fmt.Fprintf(os.Stderr, "  → single#%d %s\n", i, res.Outcome)
			}
			caseResults = append(caseResults, NewSingleRunsCaseResult(c, runs))
			continue
		}

		runs := make([]RunResult, 0, *repeat*2)
		deltas := make([]Delta, 0, *repeat)
		for i := 1; i <= *repeat; i++ {
			baseBundle := assets.Load(style, assets.LoadOptions{})
			baseDir := runDir(*outDir, c.ID, ArmBaseline, i, *repeat)
			base := runOne(cfg, baseBundle, c, baseDir, *timeout, progressW)
			base.Arm, base.Repeat = ArmBaseline, i
			runs = append(runs, RunResult{Arm: ArmBaseline, Repeat: i, Result: base})
			fmt.Fprintf(os.Stderr, "  → baseline#%d %s\n", i, base.Outcome)

			varBundle := assets.Load(style, assets.LoadOptions{})
			if err := applyVariant(&varBundle, variantPrompts); err != nil {
				fmt.Fprintf(os.Stderr, "eval: variant 覆盖失败: %v\n", err)
				return 2
			}
			varDir := runDir(*outDir, c.ID, ArmVariant, i, *repeat)
			variant := runOne(cfg, varBundle, c, varDir, *timeout, progressW)
			variant.Arm, variant.Repeat = ArmVariant, i
			runs = append(runs, RunResult{Arm: ArmVariant, Repeat: i, Result: variant})
			delta := GradeDelta(c, base, variant)
			deltas = append(deltas, delta)
			fmt.Fprintf(os.Stderr, "  → variant#%d %s · delta %s\n", i, variant.Outcome, delta.Outcome)
		}
		caseResults = append(caseResults, NewABCaseResult(c, runs, deltas))
	}

	suite := Aggregate(runID, mode, variantName, *repeat, caseResults)
	if err := WriteReport(suite, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "eval: 写报告失败: %v\n", err)
		return 2
	}

	fmt.Fprintf(os.Stderr, "\n%s\n报告: %s\n", Summary(suite), filepath.Join(*outDir, "report.md"))
	if suite.Gate == Fail {
		return 1
	}
	return 0
}

func runOne(cfg bootstrap.Config, bundle assets.Bundle, c Case, dir string, timeout time.Duration, progressW io.Writer) Result {
	runErr := RunCase(cfg, bundle, c, RunOptions{
		OutputDir: dir,
		Timeout:   timeout,
		Progress:  progressW,
	})
	col := Collect(dir, runErr)
	return Grade(c, col)
}

func runDir(outDir, caseID, arm string, repeat, totalRepeats int) string {
	if totalRepeats <= 1 {
		if arm == ArmSingle {
			return filepath.Join(outDir, "artifacts", caseID)
		}
		return filepath.Join(outDir, "artifacts", caseID, arm)
	}
	if arm == ArmSingle {
		return filepath.Join(outDir, "artifacts", caseID, fmt.Sprintf("r%d", repeat))
	}
	return filepath.Join(outDir, "artifacts", caseID, fmt.Sprintf("r%d", repeat), arm)
}

// loadVariant reads every *.md under the variant directory (filename → content). An empty directory returns an empty map.
func loadVariant(dir string) (map[string]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = string(data)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("variant 目录无 *.md 文件: %s", dir)
	}
	return out, nil
}

func applyVariant(b *assets.Bundle, prompts map[string]string) error {
	for file, raw := range prompts {
		// voice.md is the standalone variant entry point of the style layer: it only replaces the style section and leaves the protocol template alone,
		// and assembly still goes through the same BuildWriterPrompt path (docs/voice-layer.md §3.6).
		if file == "voice.md" {
			b.OverrideVoice(raw)
			continue
		}
		if err := b.OverridePrompt(file, raw); err != nil {
			return err
		}
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}
