// Package logtest captures slog records for tests.
//
// A [Recorder] is a slog.Handler that keeps every record it is given, at
// every level, with the attributes and groups of WithAttrs and WithGroup
// applied the way a rendering handler would apply them. Hand the logger from
// [New] to the code under test instead of touching slog.Default, so tests
// that log can run in parallel.
package logtest

import (
	"context"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// Record is one captured log record. Attrs is the attribute tree the record
// would be rendered with: LogValuers resolved, empty attributes and empty
// groups dropped, inline groups flattened, and the logger's own attributes
// and groups applied.
type Record struct {
	// Time is the record's time; zero when the caller left it unset.
	Time time.Time
	// Level is the record's level.
	Level slog.Level
	// Message is the log message.
	Message string
	// PC is the program counter of the logging call; zero when unknown.
	PC uintptr
	// Attrs are the record's attributes; see Record.
	Attrs []slog.Attr
}

// Source returns the file, line and function the record was logged from. It
// is the zero Source when PC is zero.
func (r Record) Source() slog.Source {
	if r.PC == 0 {
		return slog.Source{Function: "", File: "", Line: 0}
	}

	frame, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()

	return slog.Source{Function: frame.Function, File: frame.File, Line: frame.Line}
}

// Map returns the record as a map keyed the way slog's built-in handlers key
// their output: slog.TimeKey (absent for a zero time), slog.LevelKey,
// slog.MessageKey, and each attribute under its key, a group as a nested
// map[string]any and any other value as returned by slog.Value.Any.
func (r Record) Map() map[string]any {
	result := attrsMap(r.Attrs)
	if !r.Time.IsZero() {
		result[slog.TimeKey] = r.Time
	}

	result[slog.LevelKey] = r.Level
	result[slog.MessageKey] = r.Message

	return result
}

// attrsMap renders attrs as a map, groups as nested maps.
func attrsMap(attrs []slog.Attr) map[string]any {
	result := make(map[string]any, len(attrs))

	for _, attr := range attrs {
		if attr.Value.Kind() == slog.KindGroup {
			result[attr.Key] = attrsMap(attr.Value.Group())

			continue
		}

		result[attr.Key] = attr.Value.Any()
	}

	return result
}

// recorderState is the part of a Recorder that its WithAttrs and WithGroup
// derivatives share: every derived handler records into the same list.
type recorderState struct {
	mu      sync.Mutex
	records []Record
}

// scope is one WithGroup or WithAttrs step, in the order they were applied.
// A non-empty group opens a group; otherwise attrs are added at the current
// depth.
type scope struct {
	group string
	attrs []slog.Attr
}

// Recorder is a slog.Handler that captures records. It is safe for
// concurrent use; a Recorder and every handler derived from it through
// WithAttrs and WithGroup share one list of records.
type Recorder struct {
	state  *recorderState
	scopes []scope
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		state:  &recorderState{mu: sync.Mutex{}, records: nil},
		scopes: nil,
	}
}

// New returns a logger that records into a new Recorder, and the Recorder.
// When the test fails, the captured records are written to its log.
func New(tb testing.TB) (*slog.Logger, *Recorder) {
	tb.Helper()

	recorder := NewRecorder()

	tb.Cleanup(func() {
		if !tb.Failed() {
			return
		}

		for _, record := range recorder.Records() {
			tb.Logf("captured log: %s %q %v", record.Level, record.Message, record.Attrs)
		}
	})

	return slog.New(recorder), recorder
}

// Records returns a copy of the records captured so far, oldest first.
func (r *Recorder) Records() []Record {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()

	return slices.Clone(r.state.records)
}

// Messages returns the messages of the records captured so far, oldest
// first.
func (r *Recorder) Messages() []string {
	records := r.Records()
	messages := make([]string, 0, len(records))

	for _, record := range records {
		messages = append(messages, record.Message)
	}

	return messages
}

// Enabled reports true for every level: a Recorder keeps everything.
func (*Recorder) Enabled(context.Context, slog.Level) bool {
	return true
}

// Handle captures record.
func (r *Recorder) Handle(_ context.Context, record slog.Record) error {
	attrs := make([]slog.Attr, 0, record.NumAttrs())

	record.Attrs(func(attr slog.Attr) bool {
		attrs = appendResolved(attrs, attr)

		return true
	})

	for _, step := range slices.Backward(r.scopes) {
		switch {
		case step.group == "":
			attrs = append(resolveAll(step.attrs), attrs...)
		case len(attrs) > 0:
			attrs = []slog.Attr{{Key: step.group, Value: slog.GroupValue(attrs...)}}
		default:
			// A group with nothing in it is not rendered.
		}
	}

	r.state.mu.Lock()
	defer r.state.mu.Unlock()

	r.state.records = append(r.state.records, Record{
		Time:    record.Time,
		Level:   record.Level,
		Message: record.Message,
		PC:      record.PC,
		Attrs:   attrs,
	})

	return nil
}

// WithAttrs returns a Recorder that adds attrs to every record, sharing this
// one's records.
func (r *Recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return r
	}

	return r.with(scope{group: "", attrs: slices.Clone(attrs)})
}

// WithGroup returns a Recorder that nests later attributes under name,
// sharing this one's records.
func (r *Recorder) WithGroup(name string) slog.Handler {
	if name == "" {
		return r
	}

	return r.with(scope{group: name, attrs: nil})
}

// with returns a Recorder over the same state with step appended.
func (r *Recorder) with(step scope) *Recorder {
	return &Recorder{
		state:  r.state,
		scopes: append(slices.Clip(r.scopes), step),
	}
}

// resolveAll resolves and filters attrs; see appendResolved.
func resolveAll(attrs []slog.Attr) []slog.Attr {
	resolved := make([]slog.Attr, 0, len(attrs))

	for _, attr := range attrs {
		resolved = appendResolved(resolved, attr)
	}

	return resolved
}

// appendResolved appends attr to dst the way a handler renders it: the value
// resolved, an empty attribute dropped, a group resolved member by member and
// dropped when nothing is left, and a group with an empty key inlined.
func appendResolved(dst []slog.Attr, attr slog.Attr) []slog.Attr {
	attr.Value = attr.Value.Resolve()

	if attr.Equal(slog.Attr{Key: "", Value: slog.Value{}}) {
		return dst
	}

	if attr.Value.Kind() != slog.KindGroup {
		return append(dst, attr)
	}

	members := resolveAll(attr.Value.Group())

	switch {
	case len(members) == 0:
		return dst
	case attr.Key == "":
		return append(dst, members...)
	default:
		return append(dst, slog.Attr{Key: attr.Key, Value: slog.GroupValue(members...)})
	}
}
