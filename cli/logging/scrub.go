package logging

import (
	"context"
	"log/slog"
	"strings"

	sdklog "github.com/tarantool/tt/sdk/log"
)

// scrubHandler prepares every record before the inner handler renders it,
// whatever the format: it trims trailing line breaks off the message and
// masks secrets in the message, the record's attributes and the attributes
// added with WithAttrs.
type scrubHandler struct {
	inner    slog.Handler
	redactor *sdklog.Redactor
}

// newScrubHandler wraps inner so that every record it renders is scrubbed
// with redactor.
func newScrubHandler(inner slog.Handler, redactor *sdklog.Redactor) *scrubHandler {
	return &scrubHandler{inner: inner, redactor: redactor}
}

// Enabled reports whether the inner handler takes level.
func (h *scrubHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle passes a scrubbed copy of record to the inner handler.
//
// A trailing line break is a leftover of printf habits and of error texts
// read from other programs, never an empty line the caller meant to log.
func (h *scrubHandler) Handle(ctx context.Context, record slog.Record) error {
	record.Message = strings.TrimRight(record.Message, "\r\n")

	return h.inner.Handle(ctx, h.redactor.RedactRecord(record)) //nolint:wrapcheck // Transparent.
}

// Status shows msg, with its secrets masked, on the status line of the inner
// handler when it has one.
func (h *scrubHandler) Status(msg string) func() {
	return sdklog.SpinnerOf(slog.New(h.inner), h.redactor.Redact(msg))
}

// WithAttrs redacts attrs and adds them to the inner handler.
func (h *scrubHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attrs))

	for _, attr := range attrs {
		redacted = append(redacted, h.redactor.RedactAttr(attr))
	}

	return newScrubHandler(h.inner.WithAttrs(redacted), h.redactor)
}

// WithGroup opens a group on the inner handler.
func (h *scrubHandler) WithGroup(name string) slog.Handler {
	return newScrubHandler(h.inner.WithGroup(name), h.redactor)
}
