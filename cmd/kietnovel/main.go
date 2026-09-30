package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/entry/headless"
	"github.com/CTKiet2006/kietnovel/internal/entry/startup"
	"github.com/CTKiet2006/kietnovel/internal/entry/tui"
	"github.com/CTKiet2006/kietnovel/internal/eval"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/CTKiet2006/kietnovel/internal/rules"
	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// headlessMode ghi lại lần này có khởi động headless không, để die quyết định có tạm dừng khi lỗi thoát không.
var headlessMode bool

// firstNonEmpty trả giá trị đầu tiên khác rỗng. Dùng để fallback: ui_language
// trống thì lấy language, thay vì rẽ nhánh ở nhiều chỗ.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func main() {
	// Subcommand chặn trước khi parse flag thường: eval là harness chấm offline, hệ tham số độc lập.
	if len(os.Args) > 1 && os.Args[1] == "eval" {
		os.Exit(eval.Command(os.Args[2:]))
	}

	opts, args, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		die("flags: %v", err)
	}
	if opts.Version {
		buildversion.Print(os.Stdout, versionInfo())
		return
	}
	if opts.Update {
		if err := runSelfUpdate(opts.UpdateVersion); err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
		}
		return
	}
	headlessMode = opts.Headless

	// Dẫn lần đầu
	if bootstrap.NeedsSetup() {
		if opts.Headless {
			die("error: chế độ headless không hỗ trợ thiết lập lần đầu, hãy chạy TUI một lần để hoàn tất cấu hình")
		}
		setupCfg, err := bootstrap.RunSetup()
		if err != nil {
			die("setup: %v", err)
		}
		// Dẫn xong thì chạy tiếp với cấu hình vừa sinh
		runWithConfig(setupCfg, opts, args)
		return
	}

	// Tải cấu hình
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		// Config tồn tại nhưng không dùng được (thiếu provider, JSON hỏng, ...).
		// Không chết ở đây với một dòng lỗi trừng trọng: nói rõ nguyên nhân và
		// đường thoát, vì người dùng không thể tự biết sửa ở đâu.
		die("%v\n\n%s", err, repairHint())
	}

	runWithConfig(cfg, opts, args)
}

// die xử lý thoát lỗi chí mạng thống nhất: in ra stderr, ghi đĩa vào ~/.kietnovel/last-error.log,
// và tạm dừng đợi Enter ở terminal tương tác (không headless) — khi double-click khởi động, console đóng ngay
// theo tiến trình thoát, không dừng thì lỗi thoáng qua rồi mất, đúng căn nguyên khiến người dùng ở issue #37 không đường dò.
func die(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
	if path := bootstrap.WriteStartupError(msg); path != "" {
		fmt.Fprintf(os.Stderr, "(chi tiết lỗi đã ghi vào %s)\n", path)
	}
	if !headlessMode && stdinIsTerminal() {
		fmt.Fprint(os.Stderr, "\nNhấn Enter để thoát...")
		fmt.Fscanln(os.Stdin)
	}
	os.Exit(1)
}

// repairHint chỉ người dùng cách thoát khi cấu hình không dùng được.
//
// Cần vì lỗi cấu hình trả về dạng "thiếu provider (bắt buộc)" — người không rà
// code không biết sửa ở đâu, và xoá thẳng cả thư mục là mất truyện đang viết.
// Nêu đúng đường dẫn, và nhắc chỉ xoá CONFIG, không đụng output/.
func repairHint() string {
	p := bootstrap.DefaultConfigPath()
	if p == "" {
		p = "~/.kietnovel/config.json"
	}
	return strings.Join([]string{
		"Cách sửa: mở file cấu hình và điền provider + model + api_key, hoặc xoá nó để chạy lại Setup Wizard:",
		"",
		"    notepad \"" + p + "\"        # sửa tay",
		"    del \"" + p + "\"              # xoá để dẫn lại từ đầu",
		"",
		"Chỉ xoá file cấu hình. Thư mục output/ giữ toàn bộ truyện của bạn, đừng xoá.",
	}, "\n")
}

// stdinIsTerminal xét stdin có nối vào terminal (thiết bị ký tự) không. Double-click khởi động / terminal tương tác
// là true; pipe, redirect, CI là false. Xấp xỉ không dependency, đủ phân biệt có nên dừng hay không.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runWithConfig(cfg bootstrap.Config, opts cliOptions, args []string) {
	rules.EnsureHomeRulesDir()

	if len(args) > 0 {
		die("error: không còn hỗ trợ truyền nhu cầu tiểu thuyết trực tiếp qua dòng lệnh, hãy khởi động rồi nhập trong ô nhập liệu TUI")
	}

	// FillDefaults phải chạy trước khi tải asset: OutputDir là trường runtime, giá trị mặc định chuẩn hóa ở đây —
	// nếu không, dưới cấu hình mặc định thì override văn phong cấp sách <thư mục sách>/style/ không bao giờ được tải.
	cfg.FillDefaults()
	// Ngôn ngữ giao diện TUI. Phải đặt trước mọi thứ gọi i18n.T, và trước khi dựng
	// TUI, vì bảng dịch được tra khi render chứ không lúc khởi tạo struct.
	// Giao diện và ngôn ngữ sáng tác là hai lựa chọn riêng. ui_language trống thì
	// lấy language, để cấu hình cũ giữ nguyên hành vi.
	i18n.SetLanguage(firstNonEmpty(cfg.UILanguage, cfg.Language))
	// Ngôn ngữ sáng tác (vi/en/zh): chọn lớp voice + chỉ dẫn buộc đúng ngôn ngữ đầu ra.
	// Giao thức prompt giữ nguyên bản gốc (đã kiểm chứng), không dịch.
	bundle := assets.LoadWithLanguage(cfg.Language, cfg.Style, assets.DefaultLoadOptions(cfg.OutputDir))
	bundle.ApplyLanguage(cfg.Language)
	if opts.Headless {
		prompt, err := loadPrompt(opts)
		if err != nil {
			die("error: %v", err)
		}
		if err := headless.Run(cfg, bundle, headless.Options{Prompt: prompt}); err != nil {
			die("error: %v", err)
		}
		return
	}
	if opts.Prompt != "" || opts.PromptFile != "" {
		die("error: --prompt/--prompt-file chỉ dùng được trong chế độ --headless")
	}
	if err := tui.Run(cfg, bundle, versionInfo()); err != nil {
		die("error: %v", err)
	}
}

type cliOptions struct {
	Headless      bool
	Prompt        string
	PromptFile    string
	Version       bool
	Update        bool
	UpdateVersion string
}

// parseCLIOptions trích CLI flag, trả về tùy chọn và tham số còn lại.
func parseCLIOptions(argv []string) (cliOptions, []string, error) {
	var opts cliOptions
	var args []string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--version", "-v":
			opts.Version = true
		case "version":
			if i+1 < len(argv) {
				return opts, nil, fmt.Errorf("version không nhận tham số")
			}
			opts.Version = true
		case "update":
			if opts.Update {
				return opts, nil, fmt.Errorf("update chỉ được chỉ định một lần")
			}
			opts.Update = true
			if i+1 < len(argv) {
				if strings.HasPrefix(argv[i+1], "-") {
					return opts, nil, fmt.Errorf("update chỉ nhận một tham số phiên bản tùy chọn")
				}
				opts.UpdateVersion = argv[i+1]
				i++
			}
			if i+1 < len(argv) {
				return opts, nil, fmt.Errorf("update chỉ nhận một tham số phiên bản tùy chọn")
			}
		case "--headless":
			opts.Headless = true
		case "--prompt":
			if i+1 >= len(argv) {
				return opts, nil, fmt.Errorf("--prompt thiếu giá trị")
			}
			opts.Prompt = argv[i+1]
			i++
		case "--prompt-file":
			if i+1 >= len(argv) {
				return opts, nil, fmt.Errorf("--prompt-file thiếu giá trị")
			}
			opts.PromptFile = argv[i+1]
			i++
		default:
			args = append(args, argv[i])
		}
	}
	if opts.Prompt != "" && opts.PromptFile != "" {
		return opts, nil, fmt.Errorf("--prompt và --prompt-file không được dùng đồng thời")
	}
	if opts.Version && (opts.Update || opts.Headless || opts.Prompt != "" || opts.PromptFile != "" || len(args) > 0) {
		return opts, nil, fmt.Errorf("version không được dùng chung với tham số khởi động khác")
	}
	if opts.Update && (opts.Headless || opts.Prompt != "" || opts.PromptFile != "" || len(args) > 0) {
		return opts, nil, fmt.Errorf("update không được dùng chung với tham số khởi động khác")
	}
	return opts, args, nil
}

func versionInfo() buildversion.Info {
	return buildversion.Resolve(buildversion.Info{
		Version: version,
		Commit:  commit,
		Date:    date,
	})
}

func runSelfUpdate(target string) error {
	info := versionInfo()
	result, err := buildversion.Update(context.Background(), buildversion.UpdateOptions{
		Repo:           buildversion.DefaultRepo,
		BinaryName:     "kietnovel",
		TargetVersion:  target,
		CurrentVersion: info.Version,
	})
	if err != nil {
		return err
	}
	if !result.Updated {
		fmt.Printf("kietnovel đã là phiên bản mới nhất %s\n", result.Version)
		return nil
	}
	fmt.Printf("kietnovel đã cập nhật lên %s\n", result.Version)
	fmt.Printf("Vị trí cài đặt: %s\n", result.Path)
	return nil
}

func loadPrompt(opts cliOptions) (string, error) {
	return loadPromptFrom(opts, os.Stdin)
}

func loadPromptFrom(opts cliOptions, stdin io.Reader) (string, error) {
	if opts.PromptFile == "" {
		return strings.TrimSpace(opts.Prompt), nil
	}

	if opts.PromptFile == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("đọc prompt thất bại: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return startup.LoadPromptFile(opts.PromptFile)
}
