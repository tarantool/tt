package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const (
	// indent starts every record line.
	indent = "   "
	// continuation indents the second and later lines of a multi-line
	// message to the column where the message starts: the indent, the glyph
	// and a space.
	continuation = "     "
	// sgrReset ends a coloured span.
	sgrReset = "\x1b[0m"
)

// style is how the text format marks a level.
type style struct {
	// glyph stands in for the level name.
	glyph string
	// glyphSGR colours the glyph.
	glyphSGR string
	// textSGR colours attribute keys, and the message too when
	// wholeMessage is set.
	textSGR string
	// wholeMessage colours the message and not only the glyph, so that
	// warnings and errors stand out.
	wholeMessage bool
}

// styleOf returns the style of level. A level between two named ones takes
// the style of the lower one.
func styleOf(level slog.Level) style {
	switch {
	case level >= slog.LevelError:
		return style{glyph: "⨯", glyphSGR: "1;91", textSGR: "1;91", wholeMessage: true}
	case level >= slog.LevelWarn:
		return style{glyph: "⚠", glyphSGR: "1;33", textSGR: "33", wholeMessage: true}
	case level >= slog.LevelInfo:
		return style{glyph: "•", glyphSGR: "1;34", textSGR: "34", wholeMessage: false}
	default:
		return style{glyph: "·", glyphSGR: "1;37", textSGR: "37", wholeMessage: false}
	}
}

// field is an attribute ready to render: its key carries the names of the
// groups it is in, joined with dots.
type field struct {
	key   string
	value slog.Value
}

// output is the writer a textHandler and every handler derived from it
// share, with the lock that keeps their lines whole.
type output struct {
	mu     sync.Mutex
	writer io.Writer
}

// textHandler renders a record as one line for a person:
//
//	"   • message key=value group.key=value"
//
// The glyph names the level (· debug, • info, ⚠ warn, ⨯ error). There is no
// time and no level word. Continuation lines of a multi-line message are
// indented to the message column.
type textHandler struct {
	out   *output
	level slog.Leveler
	color bool
	// prefix is the open groups, each followed by a dot.
	prefix string
	// fields are the attributes added with WithAttrs.
	fields []field
}

// newTextHandler returns a text handler writing records at level and above
// to writer, coloured when color is set.
func newTextHandler(writer io.Writer, level slog.Leveler, color bool) *textHandler {
	return &textHandler{
		out:    &output{mu: sync.Mutex{}, writer: writer},
		level:  level,
		color:  color,
		prefix: "",
		fields: nil,
	}
}

// Enabled reports whether level is at or above the handler's level.
func (h *textHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// Handle writes record as one line, or several for a multi-line message.
func (h *textHandler) Handle(_ context.Context, record slog.Record) error {
	levelStyle := styleOf(record.Level)

	var line strings.Builder

	line.WriteString(indent)
	h.paint(&line, levelStyle.glyphSGR, levelStyle.glyph)
	line.WriteString(" ")

	messageSGR := ""
	if levelStyle.wholeMessage {
		messageSGR = levelStyle.textSGR
	}

	for index, part := range strings.Split(record.Message, "\n") {
		if index > 0 {
			line.WriteString("\n")

			if part != "" {
				line.WriteString(continuation)
			}
		}

		h.paint(&line, messageSGR, part)
	}

	fields := slices.Clone(h.fields)

	record.Attrs(func(attr slog.Attr) bool {
		fields = appendFields(fields, h.prefix, attr)

		return true
	})

	for _, attr := range fields {
		line.WriteString(" ")
		h.paint(&line, levelStyle.textSGR, attr.key)
		line.WriteString("=")
		line.WriteString(formatValue(attr.value))
	}

	line.WriteString("\n")

	h.out.mu.Lock()
	defer h.out.mu.Unlock()

	_, err := io.WriteString(h.out.writer, line.String())
	if err != nil {
		return fmt.Errorf("write log record: %w", err)
	}

	return nil
}

// WithAttrs returns a handler that renders attrs, under the open groups,
// with every record.
func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	derived := h.clone()

	for _, attr := range attrs {
		derived.fields = appendFields(derived.fields, h.prefix, attr)
	}

	return derived
}

// WithGroup returns a handler that puts later attributes into group name.
func (h *textHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	derived := h.clone()

	derived.prefix = h.prefix + name + "."

	return derived
}

// clone returns a copy of h that can be extended without touching h.
func (h *textHandler) clone() *textHandler {
	return &textHandler{
		out:    h.out,
		level:  h.level,
		color:  h.color,
		prefix: h.prefix,
		fields: slices.Clip(h.fields),
	}
}

// paint writes text, wrapped in the SGR sequence sgr when colour is on.
func (h *textHandler) paint(line *strings.Builder, sgr, text string) {
	if !h.color || sgr == "" || text == "" {
		line.WriteString(text)

		return
	}

	line.WriteString("\x1b[" + sgr + "m")
	line.WriteString(text)
	line.WriteString(sgrReset)
}

// appendFields appends attr to fields as a handler renders it: the value
// resolved, an empty attribute dropped, and a group flattened into its
// members with the group name prefixed to their keys - or without one for a
// group with an empty key. A group with no members renders nothing.
func appendFields(fields []field, prefix string, attr slog.Attr) []field {
	attr.Value = attr.Value.Resolve()

	if attr.Equal(slog.Attr{Key: "", Value: slog.Value{}}) {
		return fields
	}

	if attr.Value.Kind() != slog.KindGroup {
		return append(fields, field{key: prefix + attr.Key, value: attr.Value})
	}

	groupPrefix := prefix
	if attr.Key != "" {
		groupPrefix = prefix + attr.Key + "."
	}

	for _, member := range attr.Value.Group() {
		fields = appendFields(fields, groupPrefix, member)
	}

	return fields
}

// formatValue renders a resolved value: an error by its message, any other
// non-scalar by its fmt "%+v" form. The result is quoted when it is empty or
// holds a space, a quote, an equals sign or an unprintable character, so
// that one attribute cannot pass for several.
func formatValue(value slog.Value) string {
	var text string

	switch value.Kind() {
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			text = err.Error()
		} else {
			text = fmt.Sprintf("%+v", value.Any())
		}
	default:
		text = value.String()
	}

	if needsQuoting(text) {
		return strconv.Quote(text)
	}

	return text
}

// needsQuoting reports whether text must be quoted to stay one attribute.
func needsQuoting(text string) bool {
	if text == "" {
		return true
	}

	return strings.ContainsFunc(text, func(char rune) bool {
		return unicode.IsSpace(char) || char == '"' || char == '=' || !unicode.IsPrint(char)
	})
}
