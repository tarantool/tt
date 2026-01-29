package running

import (
	"bytes"
	"io"
	"regexp"

	"github.com/fatih/color"
)

var (
	errorColor     = color.New(color.FgRed, color.Bold)
	logLevelColors = map[string]*color.Color{
		"F": errorColor,
		"!": errorColor,
		"E": errorColor,
		"W": color.New(color.FgYellow),
	}

	logLevelRgx *regexp.Regexp
)

func init() {
	logLevelRgx = regexp.MustCompile(" ([F!EW])> ")
}

type colorizedWriter func(msg []byte) (int, error)

// Write is an io.Writer implementation.
func (f colorizedWriter) Write(msg []byte) (int, error) {
	return f(msg)
}

// NewColorizedPrefixWriter creates a writer which colors the prefix or the whole
// line depending on its log level.
func NewColorizedPrefixWriter(writer io.Writer, color color.Color, prefix string) io.Writer {
	buf := bytes.Buffer{}
	buf.Grow(1024)
	return colorizedWriter(func(msg []byte) (int, error) {
		for _, line := range bytes.Split(msg, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			buf.Reset()

			// spell-checker:ignore submatch
			if submatch := logLevelRgx.FindSubmatch(line); submatch != nil {
				color.Fprint(&buf, prefix)
				logLevelColors[string(submatch[1])].Fprintln(&buf, string(line))
				writer.Write(buf.Bytes())
				continue
			}

			color.Fprint(&buf, prefix)
			buf.Write(line)
			buf.WriteByte('\n')
			writer.Write(buf.Bytes())
		}
		return len(msg), nil
	})
}
