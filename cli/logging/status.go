package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/term"
)

const (
	// clearLine returns the cursor to the start of the line and erases it.
	clearLine = "\r\x1b[K"
	// spinInterval is how often the spinner moves to its next frame.
	spinInterval = 100 * time.Millisecond
	// ellipsis ends a status message cut to the terminal width.
	ellipsis = "..."
)

// spinFrames are the frames of the spinner, drawn in turn in the glyph
// column of the status line.
var spinFrames = [...]string{"|", "/", "-", "\\"}

// ticker starts a source of animation ticks and returns it with the function
// that stops it.
type ticker func() (ticks <-chan time.Time, stop func())

// realTicker ticks every spinInterval.
func realTicker() (<-chan time.Time, func()) {
	t := time.NewTicker(spinInterval)

	return t.C, t.Stop
}

// statusLine is the transient line a text handler keeps below its records
// on a terminal: a spinner frame and a message. Every field but the
// configuration is guarded by the mutex of the output it belongs to.
type statusLine struct {
	// enabled is set when the output is a terminal able to redraw a line.
	enabled bool
	// width reports the terminal width in columns, 0 when unknown.
	width func() int
	// ticks starts the animation ticks of a spinner.
	ticks ticker

	// shown is set while a status line is on the terminal.
	shown bool
	msg   string
	frame int
}

// newStatusLine returns the status line of writer, measured with width. It
// is enabled only when terminal is set: a status line drawn anywhere else
// would leave its frames and carriage returns in a file or a pipe.
func newStatusLine(
	writer io.Writer, terminal bool, width func(io.Writer) int, ticks ticker,
) statusLine {
	return statusLine{
		enabled: terminal,
		width:   func() int { return width(writer) },
		ticks:   ticks,
		shown:   false,
		msg:     "",
		frame:   0,
	}
}

// fileWidth reports the width of the terminal writer is open on, or 0.
func fileWidth(writer io.Writer) int {
	file, ok := writer.(*os.File)
	if !ok {
		return 0
	}

	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil {
		return 0
	}

	return width
}

// render returns the bytes that draw the status line over the current
// terminal line, painted as an Info glyph would be when color is set. The
// line is cut to one column less than the terminal width, so that it never
// wraps: a clear erases only the physical line the cursor is on.
func (s *statusLine) render(color bool) string {
	glyph := spinFrames[s.frame%len(spinFrames)]
	if color {
		glyph = "\x1b[" + styleOf(slog.LevelInfo).glyphSGR + "m" + glyph + sgrReset
	}

	msg := s.msg
	if width := s.width(); width > 0 {
		msg = fit(msg, width-1-len(continuation))
	}

	return clearLine + indent + glyph + " " + msg
}

// fit cuts text to at most limit runes, marking a cut with an ellipsis.
func fit(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	if limit <= len(ellipsis) {
		return string(runes[:max(limit, 0)])
	}

	return string(runes[:limit-len(ellipsis)]) + ellipsis
}

// oneLine makes msg fit a single terminal line: every control character
// and line break becomes a space.
func oneLine(msg string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}

		return char
	}, msg)
}

// Status shows msg with a spinner on the status line until stop is called.
// Records written meanwhile clear the line and draw it again below them.
//
// Nothing is drawn when the output is not a terminal, and nothing either
// while another status line is shown: the first one keeps the line.
func (h *textHandler) Status(msg string) func() {
	out := h.out

	if !out.status.enabled {
		return func() {}
	}

	out.mu.Lock()

	if out.status.shown {
		out.mu.Unlock()

		return func() {}
	}

	out.status.shown = true
	out.status.msg = oneLine(msg)
	out.status.frame = 0
	out.write(out.status.render(h.color))

	out.mu.Unlock()

	ticks, stopTicks := out.status.ticks()
	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)

		for {
			select {
			case <-done:
				return
			case <-ticks:
				out.mu.Lock()
				out.status.frame++
				out.write(out.status.render(h.color))
				out.mu.Unlock()
			}
		}
	}()

	var once sync.Once

	return func() {
		once.Do(func() {
			close(done)
			<-finished
			stopTicks()

			out.mu.Lock()
			defer out.mu.Unlock()

			out.status.shown = false
			out.status.msg = ""
			out.write(clearLine)
		})
	}
}

// write writes text to the output, dropping the error: a status line that
// cannot be drawn has nowhere better to be reported. The caller holds the
// mutex.
func (o *output) write(text string) {
	_, _ = io.WriteString(o.writer, text)
}
