// Package log is tt's logging facade over log/slog.
//
// Diagnostics - progress, warnings, errors, debug detail - go to stderr
// through slog. A command's result never goes through this package; it goes
// to stdout through [github.com/tarantool/tt/sdk/output].
//
// The tt core owns the process logger: it installs the handler that renders
// records as text or JSON, filters by level and redacts secrets, and makes it
// slog.Default. Nothing here configures handlers. A module logs through the
// *slog.Logger tt hands it; the printf functions below serve tt's own code.
//
// The levels are slog's four: Debug, Info, Warn, Error. There is no Fatal: a
// failure is returned as an error and the core decides the exit code.
//
// The package also holds the secret-redaction logic ([Redactor], [Secret],
// [RedactURL]) so that the core's handler and the tests of any module apply
// the same rules.
package log

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"time"
)

// callerSkip is the number of frames between runtime.Callers and the caller
// of a facade function: runtime.Callers itself, emit, and the facade
// function.
const callerSkip = 3

// Logger returns the process logger, slog.Default.
func Logger() *slog.Logger {
	return slog.Default()
}

// Debugf logs a formatted message at Debug level on the process logger.
func Debugf(format string, args ...any) {
	emit(slog.LevelDebug, true, format, args)
}

// Infof logs a formatted message at Info level on the process logger.
func Infof(format string, args ...any) {
	emit(slog.LevelInfo, true, format, args)
}

// Warnf logs a formatted message at Warn level on the process logger.
func Warnf(format string, args ...any) {
	emit(slog.LevelWarn, true, format, args)
}

// Errorf logs a formatted message at Error level on the process logger.
func Errorf(format string, args ...any) {
	emit(slog.LevelError, true, format, args)
}

// Debug logs msg at Debug level on the process logger. The optional attrs
// are key-value pairs or slog.Attr values, as for slog.Logger.Debug.
func Debug(msg string, attrs ...any) {
	emit(slog.LevelDebug, false, msg, attrs)
}

// Info logs msg at Info level on the process logger. The optional attrs are
// key-value pairs or slog.Attr values, as for slog.Logger.Info.
func Info(msg string, attrs ...any) {
	emit(slog.LevelInfo, false, msg, attrs)
}

// Warn logs msg at Warn level on the process logger. The optional attrs are
// key-value pairs or slog.Attr values, as for slog.Logger.Warn.
func Warn(msg string, attrs ...any) {
	emit(slog.LevelWarn, false, msg, attrs)
}

// Error logs msg at Error level on the process logger. The optional attrs
// are key-value pairs or slog.Attr values, as for slog.Logger.Error.
func Error(msg string, attrs ...any) {
	emit(slog.LevelError, false, msg, attrs)
}

// emit builds a record whose source is the caller of the facade function
// rather than the facade itself, so a handler with AddSource points at the
// line that logged. When formatted is set, msg is a format string and args
// its operands - formatted even when args is empty, as fmt.Sprintf would, so
// "100%%" logs as "100%". Otherwise msg is taken as is and args are attrs.
//
// The handler's error is dropped, as slog.Logger drops it: a failure to write
// a diagnostic has nowhere better to be reported.
func emit(level slog.Level, formatted bool, msg string, args []any) {
	ctx := context.Background()
	logger := slog.Default()

	if !logger.Enabled(ctx, level) {
		return
	}

	var attrs []any

	if formatted {
		msg = fmt.Sprintf(msg, args...)
	} else {
		attrs = args
	}

	var pcs [1]uintptr

	runtime.Callers(callerSkip, pcs[:])

	record := slog.NewRecord(time.Now(), level, msg, pcs[0])
	record.Add(attrs...)

	_ = logger.Handler().Handle(ctx, record)
}
