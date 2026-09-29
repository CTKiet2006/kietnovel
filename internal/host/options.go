package host

import "log/slog"

type newOptions struct {
	logFile       string
	logAlsoStderr bool
	logAttrs      []slog.Attr
}

// NewOption configures the Host construction process; the runtime resources are still owned by Host.
type NewOption func(*newOptions)

// WithFileLog gives Host a runtime log session. The log is opened only after the novel-directory lease
// is taken, and closed once Host has closed all logs. If opening fails the current process logger keeps
// being used, and the caller must handle that error explicitly through FileLogError.
func WithFileLog(filename string, alsoStderr bool, attrs ...slog.Attr) NewOption {
	return func(opts *newOptions) {
		opts.logFile = filename
		opts.logAlsoStderr = alsoStderr
		opts.logAttrs = append([]slog.Attr(nil), attrs...)
	}
}
