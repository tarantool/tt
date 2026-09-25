package log

import (
	"context"
	"log/slog"
)

// LibraryKey is the attribute key that names the library a record came from.
const LibraryKey = "library"

// Library returns a logger for a third-party library, built on the process
// logger. See [LibraryOf].
//
// It binds the handler slog.Default has at the time of the call, so call it
// where the library is set up, not in a package-level variable initialised
// before tt has configured logging.
func Library(name string) *slog.Logger {
	return LibraryOf(slog.Default(), name)
}

// LibraryOf returns a logger for a third-party library, built on base.
//
// A library's Info is chatter from tt's point of view - go-luarocks logs
// every line luarocks prints at Info - so records from Info up to, but not
// including, Warn are demoted by the distance between Info and Debug. Warn
// and Error pass unchanged: those are for the user.
//
// Every record carries a top-level library=<name> attribute. It is a plain
// attribute rather than a group so that the library's own attributes keep
// their keys and the text format stays one flat line.
func LibraryOf(base *slog.Logger, name string) *slog.Logger {
	return slog.New(&demoteHandler{inner: base.Handler()}).With(LibraryKey, name)
}

// demoteHandler lowers records in [Info, Warn) by one step of Info-Debug.
type demoteHandler struct {
	inner slog.Handler
}

// demote maps a library level to the level tt logs it at.
func demote(level slog.Level) slog.Level {
	if level >= slog.LevelInfo && level < slog.LevelWarn {
		return level - (slog.LevelInfo - slog.LevelDebug)
	}

	return level
}

// Enabled reports whether the inner handler takes the demoted level.
func (h *demoteHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, demote(level))
}

// Handle passes the record on at its demoted level.
func (h *demoteHandler) Handle(ctx context.Context, record slog.Record) error {
	record.Level = demote(record.Level)

	return h.inner.Handle(ctx, record) //nolint:wrapcheck // A handler is transparent.
}

// WithAttrs returns a demoting handler over the inner one with attrs added.
func (h *demoteHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &demoteHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup returns a demoting handler over the inner one with a group
// opened.
func (h *demoteHandler) WithGroup(name string) slog.Handler {
	return &demoteHandler{inner: h.inner.WithGroup(name)}
}
