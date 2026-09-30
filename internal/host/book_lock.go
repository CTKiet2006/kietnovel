package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
	"github.com/gofrs/flock"
)

const bookLockFile = ".kietnovel.lock"

// ErrBookInUse nghĩa là cùng thư mục truyện đã bị một tiến trình khác chiếm.
// Nêu đúng tên chương trình từ AppName, vì đây là thông báo người dùng đọc được
// và phải khớp với tên binary họ sẽ chạy.
var ErrBookInUse = errors.New("Thư mục truyện đã bị một instance " + buildversion.AppName + " khác chiếm")

// bookLease giữ quyền độc quyền liên tiến trình của thư mục truyện trong suốt vòng đời Host.
// File khoá vẫn nằm trong thư mục; trạng thái chiếm thực sự do hệ điều hành quản lý, tiến trình thoát bất thường cũng tự giải phóng.
type bookLease struct {
	lock *flock.Flock
}

func acquireBookLease(dir string) (*bookLease, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("Không phân giải được thư mục truyện: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("Không tạo được thư mục truyện: %w", err)
	}
	fileLock := flock.New(filepath.Join(absDir, bookLockFile), flock.SetPermissions(0o600))
	locked, err := fileLock.TryLock()
	if err != nil {
		return nil, closeBookLockAfterFailure(fileLock, fmt.Errorf("Không chiếm được thư mục truyện %q: %w", absDir, err))
	}
	if !locked {
		return nil, closeBookLockAfterFailure(fileLock, fmt.Errorf(
			"%w: %s; hãy đóng terminal khác đang thao tác thư mục này, hoặc dùng một thư mục truyện khác",
			ErrBookInUse,
			absDir,
		))
	}
	return &bookLease{lock: fileLock}, nil
}

func closeBookLockAfterFailure(fileLock *flock.Flock, cause error) error {
	if err := fileLock.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("Không đóng được khoá thư mục truyện: %w", err))
	}
	return cause
}

func (l *bookLease) Close() error {
	if l == nil || l.lock == nil {
		return nil
	}
	err := l.lock.Close()
	l.lock = nil
	return err
}
