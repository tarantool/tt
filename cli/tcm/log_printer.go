package tcm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/tail"
)

const (
	indentSpaces = "  "

	logHeaderTime  = "time"
	logHeaderLevel = "level"
	logHeaderMsg   = "msg"
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
	colorBold   = color.New(color.Bold)
	colorItalic = color.New(color.Italic)
	colorFaint  = color.New(color.Faint)
)

// LogPrinter is an interface for printing formatted log messages.
type LogPrinter interface {
	Format(str string) string
	Print(ctx context.Context, in <-chan string) error
}

// logPrinter default implementation for the LogPrinter interface.
type logPrinter struct {
	noFormat bool
	noColor  bool
	out      io.Writer
	color    tail.ColorPicker
}

// Format formats a log string, applying colors and indentation if necessary.
// If noFormat is true or the string is not a valid JSON object, it returns
// the string as is without any formatting.
func (f *logPrinter) Format(str string) string {
	str = strings.TrimSpace(str)
	if f.noFormat || str == "" || str[0] != '{' || str[len(str)-1] != '}' {
		return str
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(str), &record); err != nil {
		return str
	}

	color := f.color()

	var resultLines []string
	resultLines = append(resultLines, color.Sprint("{"))
	record, headerLines := printRecordHeader(record, color)
	resultLines = append(resultLines, headerLines...)

	json, err := json.MarshalIndent(record, "", indentSpaces)
	if err != nil {
		return str
	}

	lines := strings.Split(string(json), "\n")
	lines = lines[1 : len(lines)-1]

	for _, line := range lines {
		matches := keyLineRegex.FindStringSubmatch(line)
		if len(matches) == matchesCount {
			ck := color.Sprint(matches[matchKey])
			cv := colorFaint.Sprint(matches[matchValue])
			resultLines = append(resultLines, matches[matchIndent]+ck+matches[matchColon]+cv)

		} else {
			cv := colorFaint.Sprint(line)
			resultLines = append(resultLines, cv)
		}
	}

	resultLines = append(resultLines, color.Sprint("}"))
	return strings.Join(resultLines, "\n")
}

// Print reads lines from the input channel and prints them to the output writer.
func (f *logPrinter) Print(ctx context.Context, in <-chan string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-in:
			if !ok {
				return nil
			}
			fmt.Fprintln(f.out, line)
		}
	}
}

// printRecordHeader extracting the time, level, and message; then print them at beginning.
func printRecordHeader(record map[string]any, color color.Color) (map[string]any, []string) {
	var result []string
	for _, k := range []string{logHeaderTime, logHeaderLevel, logHeaderMsg} {
		if v, ok := record[k]; ok {
			sv, ok := v.(string)
			if !ok {
				continue
			}

			line := indentSpaces + color.Sprint(k) + `: `
			switch k {
			case logHeaderTime:
				line += colorBold.Sprint(sv)
			case logHeaderMsg:
				line += colorItalic.Sprint(sv)
			default:
				line += sv
			}

			result = append(result, line)
		}
		delete(record, k)
	}

	return record, result
}

// NewLogFormatter creates a new log printer with optional formatting and coloring.
func NewLogFormatter(noFormat, noColor bool, out io.Writer) *logPrinter {
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
	return &lf
}
