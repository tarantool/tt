package tcm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/tail"
)

const (
	indentSpaces = "  "

	logHeaderTime  = "time"
	logHeaderLevel = "level"
	logHeaderMsg   = "msg"
	logHeaderKey   = "key"
	logHeaderValue = "val"
)

// keyLineRegex is used to match lines with JSON keys in the format: "key": value.
// 0: whole line.
// 1: first indent.
// 2: key without quotes.
// 3: colon and spaces.
// 4: rest is value.
var keyLineRegex = regexp.MustCompile(`^(\s*)"(.*?)"(\s*:\s*)(.*)$`)

const (
	matchWholeLine = iota
	matchIndent
	matchKey
	matchColon
	matchValue
	matchesCount
)

var (
	colorBold      = *color.New(color.Bold)
	colorItalic    = *color.New(color.Italic)
	colorFaint     = *color.New(color.Faint)
	colorUnderline = *color.New(color.Underline)
)

// Printer is an interface for printing formatted log messages.
type Printer interface {
	Print(ctx context.Context, in <-chan string) error
}

// logPrinter default implementation for the LogPrinter interface.
type logPrinter struct {
	noFormat bool
	noColor  bool
	out      io.Writer
	color    tail.ColorPicker
	sync     func() error
}

// format formats a log string, applying colors and indentation if necessary.
// If noFormat is true or the string is not a valid JSON object, it returns
// the string as is without any formatting.
func (l *logPrinter) format(str string) string {
	str = strings.TrimSpace(str)
	if l.noFormat || str == "" || str[0] != '{' || str[len(str)-1] != '}' {
		return str
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(str), &record); err != nil {
		return str
	}

	color := l.color()

	var resultLines []string
	resultLines = append(resultLines, color.Sprint("{"))
	headerLines := printRecordHeader(record, color)
	resultLines = append(resultLines, headerLines...)

	json, err := json.MarshalIndent(record, "", indentSpaces)
	if err != nil {
		return str
	}

	if len(json) > 2 { // If the JSON contains more than empty `{}`.
		lines := strings.Split(string(json), "\n")
		lines = lines[1 : len(lines)-1]
		resultLines = append(resultLines, colorizeJsonLines(lines, color, colorFaint)...)
	}

	resultLines = append(resultLines, color.Sprint("}"))

	return strings.Join(resultLines, "\n")
}

// Print reads lines from the input channel and prints them to the output writer.
func (l *logPrinter) Print(ctx context.Context, in <-chan string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-in:
			if !ok {
				return nil
			}

			fmt.Fprintln(l.out, l.format(line))

			if l.sync != nil {
				l.sync()
			}
		}
	}
}

func colorizeJsonLines(lines []string, cKey, cVal color.Color) []string {
	resultLines := make([]string, 0, len(lines))

	for _, line := range lines {
		matches := keyLineRegex.FindStringSubmatch(line)
		if len(matches) == matchesCount {
			kc := cKey.Sprint(matches[matchKey])
			vc := cVal.Sprint(matches[matchValue])
			line = matches[matchIndent] + kc + matches[matchColon] + vc
		} else {
			line = cVal.Sprint(line)
		}

		resultLines = append(resultLines, line)
	}

	return resultLines
}

// formatStringValue if `val` is a string, it trims it and returns a formatted version.
// If string contains only spaces, it returns a quoted version.
// Returns true if string value was empty.
func formatStringValue(sv string) (string, bool) {
	if sv == "" {
		return `""`, true
	}

	ts := strings.TrimRightFunc(sv, unicode.IsSpace)
	if ts == "" {
		// Note: handle cases with value from only spaces.
		return fmt.Sprintf("%q", sv), false
	}

	return ts, false
}

// formatBaseHeaderEntry formats a base header entry (time, level, msg) with color.
// Returns the formatted line and a boolean indicating success.
func formatBaseHeaderEntry(key string, val any, color color.Color) (string, bool) {
	sv, ok := val.(string)
	if !ok {
		return "", false
	}

	sv, isEmpty := formatStringValue(sv)
	if isEmpty {
		return "", true
	}

	line := indentSpaces + color.Sprint(key) + ": "

	switch key {
	case logHeaderTime:
		line += colorBold.Sprint(sv)
	case logHeaderMsg:
		line += colorItalic.Sprint(sv)
	default:
		line += sv
	}

	return line, true
}

// formatKeyValueEntryPair formats a key/value entry pair with color.
func formatKeyValueEntryPair(key, val any, color color.Color) string {
	line := indentSpaces + color.Sprint(logHeaderKey+"/"+logHeaderValue) + ": " +
		colorUnderline.Sprint(key) + "="

	if sv, ok := val.(string); ok {
		val, _ = formatStringValue(sv)
	}

	line += colorItalic.Sprint(val)

	return line
}

// printRecordHeader extracting the time, level, message and key/val then print them at beginning.
// It modifies the record map by removing handled entries.
func printRecordHeader(record map[string]any, color color.Color) []string {
	var result []string
	for _, k := range []string{logHeaderTime, logHeaderLevel, logHeaderMsg} {
		if v, ok := record[k]; ok {
			if line, ok := formatBaseHeaderEntry(k, v, color); ok {
				if line != "" {
					result = append(result, line)
				}

				delete(record, k)
			}
		}
	}

	if k, ok := record[logHeaderKey]; ok {
		if v, ok := record[logHeaderValue]; ok {
			result = append(result, formatKeyValueEntryPair(k, v, color))

			delete(record, logHeaderKey)
			delete(record, logHeaderValue)
		}
	}

	return result
}

// NewLogPrinter creates a new log printer with optional formatting and coloring.
func NewLogPrinter(noFormat, noColor bool, out io.Writer) Printer {
	lf := logPrinter{
		noFormat: noFormat,
		noColor:  noColor,
		out:      out,
	}

	if lf.noColor || lf.noFormat {
		color.NoColor = true
		lf.noColor = true
	}

	if !lf.noFormat {
		lf.color = tail.DefaultColorPicker()
	}

	if s, ok := lf.out.(interface{ Sync() error }); ok {
		lf.sync = s.Sync
	} else if bw, ok := lf.out.(*bufio.Writer); ok {
		lf.sync = bw.Flush
	}

	return &lf
}
