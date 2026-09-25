package log

import (
	"log/slog"
)

// StatusHandler is implemented by a handler that can keep a transient status
// line - a spinner - on its output while a long operation runs. The core's
// text handler implements it when stderr is a terminal; it keeps the status
// line below the records it writes, clearing the line before a record and
// drawing it again after.
type StatusHandler interface {
	slog.Handler
	// Status shows msg on the status line until the returned stop is
	// called. Stop clears the line, waits until nothing more is drawn and
	// may be called more than once. A handler that cannot show a status
	// line at the moment returns a stop that does nothing.
	Status(msg string) (stop func())
}

// Spinner shows msg with an animation on the status line of the process
// logger until the returned stop is called. See [SpinnerOf].
func Spinner(msg string) (stop func()) {
	return SpinnerOf(slog.Default(), msg)
}

// SpinnerOf shows msg with an animation on the status line of logger's
// handler until the returned stop is called.
//
// The spinner is progress for a person watching a terminal: when the handler
// is not a [StatusHandler] - the output is not a terminal, the format is JSON,
// or the logger is not tt's - nothing is shown and stop does nothing. It
// never writes to stdout. Records logged while it spins stay whole.
func SpinnerOf(logger *slog.Logger, msg string) (stop func()) {
	handler, ok := logger.Handler().(StatusHandler)
	if !ok {
		return func() {}
	}

	return handler.Status(msg)
}

// Status shows msg on the status line of the inner handler, when it has one.
func (h *demoteHandler) Status(msg string) func() {
	return SpinnerOf(slog.New(h.inner), msg)
}
