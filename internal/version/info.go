package version

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

type Info struct {
	Version string
	Commit  string
	Date    string
}

// AppName là tên chương trình, dùng cho mọi chỗ người dùng nhìn thấy: thanh tiêu đề
// TUI, gợi ý lệnh nâng cấp, thông báo lỗi, metadata EPUB. Đặt ở đây để đổi tên
// một lần duy nhất, tránh tình trạng tên trong giao diện lệch với tên binary.
const AppName = "kietnovel"

func Resolve(raw Info) Info {
	return Info{
		Version: displayVersion(raw.Version),
		Commit:  displayCommit(raw.Commit),
		Date:    displayDate(raw.Date),
	}
}

func Print(w io.Writer, info Info) {
	info = Resolve(info)
	fmt.Fprintf(w, "%s %s\ncommit: %s\nbuilt: %s\n", AppName, info.Version, info.Commit, info.Date)
}

func displayVersion(v string) string {
	v = Normalize(v)
	if v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if mv := Normalize(info.Main.Version); mv != "dev" {
			return mv
		}
	}
	return "dev"
}

func Normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "(devel)" {
		return "dev"
	}
	if v != "dev" && !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

func displayCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if commit != "" && commit != "unknown" {
		return commit
	}
	if v := buildSetting("vcs.revision"); v != "" {
		return v
	}
	return "unknown"
}

func displayDate(date string) string {
	date = strings.TrimSpace(date)
	if date != "" && date != "unknown" {
		return date
	}
	if v := buildSetting("vcs.time"); v != "" {
		return v
	}
	return "unknown"
}

func buildSetting(key string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return strings.TrimSpace(setting.Value)
		}
	}
	return ""
}
