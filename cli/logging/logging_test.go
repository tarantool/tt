package logging_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/slogtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdklog "github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/logging"
)

// noEnv is a LookupEnv that finds nothing, so a test does not depend on the
// NO_COLOR and TERM of the machine it runs on.
func noEnv(string) (string, bool) {
	return "", false
}

// env returns a LookupEnv that serves vars.
func env(vars map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := vars[key]

		return value, ok
	}
}

// terminal is an IsTerminal that reports every writer as a terminal.
func terminal(io.Writer) bool {
	return true
}

// notTerminal is an IsTerminal that reports no writer as a terminal.
func notTerminal(io.Writer) bool {
	return false
}

// newLogger returns a logger over the handler opts describe, writing to a
// buffer it also returns. Unset fields get test values: text format, a fresh
// redactor, no terminal and an empty environment.
func newLogger(t *testing.T, opts logging.Options) (*slog.Logger, *bytes.Buffer) {
	t.Helper()

	var buf bytes.Buffer

	opts.Writer = &buf

	if opts.Format == "" {
		opts.Format = logging.FormatText
	}

	if opts.Redactor == nil {
		opts.Redactor = sdklog.NewRedactor()
	}

	if opts.IsTerminal == nil {
		opts.IsTerminal = notTerminal
	}

	if opts.LookupEnv == nil {
		opts.LookupEnv = noEnv
	}

	handler, err := logging.NewHandler(opts)
	require.NoError(t, err)

	return slog.New(handler), &buf
}

func TestText_Levels(t *testing.T) {
	logger, buf := newLogger(t, logging.Options{Level: slog.LevelDebug - 4})

	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")
	logger.Log(t.Context(), slog.LevelError+4, "fatal-ish message")
	logger.Log(t.Context(), slog.LevelDebug-4, "trace message")

	assert.Equal(t, ""+
		"   · debug message\n"+
		"   • info message\n"+
		"   ⚠ warn message\n"+
		"   ⨯ error message\n"+
		"   ⨯ fatal-ish message\n"+
		"   · trace message\n",
		buf.String())
}

func TestText_LevelFilter(t *testing.T) {
	logger, buf := newLogger(t, logging.Options{})

	logger.Debug("hidden")
	logger.Info("shown")

	assert.Equal(t, "   • shown\n", buf.String())
}

func TestText_Attrs(t *testing.T) {
	logger, buf := newLogger(t, logging.Options{})

	logger.LogAttrs(t.Context(), slog.LevelInfo, "started",
		slog.String("name", "app"),
		slog.Int("count", 3),
		slog.Bool("ok", true),
		slog.Any("err", errors.New("boom")),
		slog.String("spaced", "a b"),
		slog.String("empty", ""),
		slog.String("quoted", `say "hi"`),
		slog.String("eq", "a=b"),
		slog.String("newline", "one\ntwo"),
		slog.Attr{Key: "", Value: slog.Value{}},
	)

	assert.Equal(t, `   • started name=app count=3 ok=true err=boom spaced="a b" empty=""`+
		` quoted="say \"hi\"" eq="a=b" newline="one\ntwo"`+"\n",
		buf.String())
}

func TestText_Groups(t *testing.T) {
	logger, buf := newLogger(t, logging.Options{})

	logger.With("top", 1).WithGroup("g").With("a", "x").WithGroup("h").Info("msg",
		"b", "y",
		slog.Group("inner", "c", "z"),
		slog.Group("", "inline", "w"),
		slog.Group("empty"),
	)
	logger.WithGroup("unused").Info("no attrs")

	assert.Equal(t, ""+
		"   • msg top=1 g.a=x g.h.b=y g.h.inner.c=z g.h.inline=w\n"+
		"   • no attrs\n",
		buf.String())
}

func TestText_Multiline(t *testing.T) {
	logger, buf := newLogger(t, logging.Options{})

	logger.Warn("first\nsecond\n\nthird\n", "k", "v")
	logger.Info("trailing newline\n")

	assert.Equal(t, ""+
		"   ⚠ first\n"+
		"     second\n"+
		"\n"+
		"     third k=v\n"+
		"   • trailing newline\n",
		buf.String())
}

func TestText_Color(t *testing.T) {
	logger, buf := newLogger(t, logging.Options{Level: slog.LevelDebug, IsTerminal: terminal})

	logger.Debug("d", "k", "v")
	logger.Info("i", "k", "v")
	logger.Warn("w", "k", "v")
	logger.Error("e\nmore", "k", "v")

	assert.Equal(t, ""+
		"   \x1b[1;37m·\x1b[0m d \x1b[37mk\x1b[0m=v\n"+
		"   \x1b[1;34m•\x1b[0m i \x1b[34mk\x1b[0m=v\n"+
		"   \x1b[1;33m⚠\x1b[0m \x1b[33mw\x1b[0m \x1b[33mk\x1b[0m=v\n"+
		"   \x1b[1;91m⨯\x1b[0m \x1b[1;91me\x1b[0m\n"+
		"     \x1b[1;91mmore\x1b[0m \x1b[1;91mk\x1b[0m=v\n",
		buf.String())
}

func TestText_ColorOff(t *testing.T) {
	tests := map[string]logging.Options{
		"not a terminal": {IsTerminal: notTerminal, LookupEnv: noEnv},
		"NO_COLOR": {
			IsTerminal: terminal,
			LookupEnv:  env(map[string]string{"NO_COLOR": "1"}),
		},
		"dumb terminal": {
			IsTerminal: terminal,
			LookupEnv:  env(map[string]string{"TERM": "dumb"}),
		},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			logger, buf := newLogger(t, opts)

			logger.Error("e", "k", "v")

			assert.Equal(t, "   ⨯ e k=v\n", buf.String())
		})
	}

	t.Run("empty NO_COLOR", func(t *testing.T) {
		logger, buf := newLogger(t, logging.Options{
			IsTerminal: terminal,
			LookupEnv:  env(map[string]string{"NO_COLOR": ""}),
		})

		logger.Info("i")

		assert.Equal(t, "   \x1b[1;34m•\x1b[0m i\n", buf.String())
	})
}

func TestNewHandler_DefaultTerminalProbe(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "log"))
	require.NoError(t, err)

	t.Cleanup(func() { _ = file.Close() })

	handler, err := logging.NewHandler(logging.Options{
		Level:      slog.LevelInfo,
		Format:     logging.FormatText,
		Writer:     file,
		Redactor:   sdklog.NewRedactor(),
		IsTerminal: nil,
		LookupEnv:  noEnv,
	})
	require.NoError(t, err)

	slog.New(handler).Error("e")

	written, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	assert.Equal(t, "   ⨯ e\n", string(written))
}

func TestJSON(t *testing.T) {
	logger, buf := newLogger(t, logging.Options{Format: logging.FormatJSON, IsTerminal: terminal})

	logger.Debug("hidden")
	logger.WithGroup("g").Warn("careful", "k", "v", "n", 2)

	var record map[string]any

	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	assert.Equal(t, "WARN", record[slog.LevelKey])
	assert.Equal(t, "careful", record[slog.MessageKey])
	assert.Contains(t, record, slog.TimeKey)
	assert.Equal(t, map[string]any{"k": "v", "n": float64(2)}, record["g"])
	assert.Equal(t, 1, strings.Count(buf.String(), "\n"), "one object per line")
	assert.NotContains(t, buf.String(), "\x1b[", "JSON is never coloured")
}

func TestJSON_TrailingNewline(t *testing.T) {
	logger, buf := newLogger(t, logging.Options{Format: logging.FormatJSON})

	logger.Info("printf habit\r\n\n")

	var record map[string]any

	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	assert.Equal(t, "printf habit", record[slog.MessageKey])
}

// parseText reads the lines a text handler wrote back into the map slogtest
// checks. The text format has no time column, so the map always carries
// slog.TimeKey; the one case that wants it absent is skipped. The message is
// the first word after the glyph, which holds for every slogtest message.
func parseText(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	levels := map[string]string{"·": "DEBUG", "•": "INFO", "⚠": "WARN", "⨯": "ERROR"}

	scanner := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	require.True(t, scanner.Scan(), "no line written")

	line, ok := strings.CutPrefix(scanner.Text(), "   ")
	require.True(t, ok, "no indent in %q", scanner.Text())

	glyph, rest, ok := strings.Cut(line, " ")
	require.True(t, ok, "no glyph in %q", line)

	level, ok := levels[glyph]
	require.True(t, ok, "unknown glyph %q", glyph)

	words := strings.Fields(rest)
	require.NotEmpty(t, words)

	result := map[string]any{
		slog.TimeKey:    "not rendered",
		slog.LevelKey:   level,
		slog.MessageKey: words[0],
	}

	for _, word := range words[1:] {
		key, value, ok := strings.Cut(word, "=")
		require.True(t, ok, "no '=' in %q", word)

		path := strings.Split(key, ".")
		group := result

		for _, name := range path[:len(path)-1] {
			nested, ok := group[name].(map[string]any)
			if !ok {
				nested = map[string]any{}
				group[name] = nested
			}

			group = nested
		}

		group[path[len(path)-1]] = value
	}

	require.False(t, scanner.Scan(), "more than one line written")

	return result
}

func TestText_Slogtest(t *testing.T) {
	var buf *bytes.Buffer

	slogtest.Run(t,
		func(t *testing.T) slog.Handler {
			t.Helper()

			if strings.HasSuffix(t.Name(), "/zero-time") {
				t.Skip("the text format never renders a time")
			}

			var logger *slog.Logger

			logger, buf = newLogger(t, logging.Options{})

			return logger.Handler()
		},
		func(t *testing.T) map[string]any {
			t.Helper()

			return parseText(t, buf)
		},
	)
}

func TestJSON_Slogtest(t *testing.T) {
	var buf *bytes.Buffer

	slogtest.Run(t,
		func(t *testing.T) slog.Handler {
			t.Helper()

			var logger *slog.Logger

			logger, buf = newLogger(t, logging.Options{Format: logging.FormatJSON})

			return logger.Handler()
		},
		func(t *testing.T) map[string]any {
			t.Helper()

			var record map[string]any

			require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

			return record
		},
	)
}

// secretError hides a secret behind an error message.
type secretError struct {
	uri string
}

func (e secretError) Error() string {
	return "cannot dial " + e.uri
}

func TestRedaction(t *testing.T) {
	const (
		token    = "t0ken-registered"
		password = "S3cretPassw0rd"
	)

	for _, format := range []logging.Format{logging.FormatText, logging.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			redactor := sdklog.NewRedactor()
			require.True(t, redactor.Register(token))

			logger, buf := newLogger(t, logging.Options{Format: format, Redactor: redactor})

			logger.With("header", "Bearer "+token).WithGroup("g").Info(
				"connecting to admin:"+password+"@localhost:3301 with "+token,
				"uri", "http://user:"+password+"@example.com/x",
				"err", secretError{uri: "admin:" + password + "@127.0.0.1:1"},
				slog.Group("nested", "token", token),
			)

			out := buf.String()
			assert.NotContains(t, out, password)
			assert.NotContains(t, out, token)
			assert.Equal(t, 6, strings.Count(out, sdklog.Redacted), out)
		})
	}
}

func TestRedaction_ProcessRegistryByDefault(t *testing.T) {
	const secret = "pr0cess-wide-secret"

	require.True(t, sdklog.RegisterSecret(secret))

	var buf bytes.Buffer

	handler, err := logging.NewHandler(logging.Options{
		Level:      slog.LevelInfo,
		Format:     logging.FormatText,
		Writer:     &buf,
		Redactor:   nil,
		IsTerminal: notTerminal,
		LookupEnv:  noEnv,
	})
	require.NoError(t, err)

	slog.New(handler).Info("value " + secret)

	assert.Equal(t, "   • value "+sdklog.Redacted+"\n", buf.String())
}

func TestNewHandler_Errors(t *testing.T) {
	_, err := logging.NewHandler(logging.Options{
		Level:      slog.LevelInfo,
		Format:     "xml",
		Writer:     io.Discard,
		Redactor:   nil,
		IsTerminal: nil,
		LookupEnv:  nil,
	})
	require.ErrorIs(t, err, logging.ErrUnknownFormat)

	_, err = logging.NewHandler(logging.Options{
		Level:      slog.LevelInfo,
		Format:     logging.FormatText,
		Writer:     nil,
		Redactor:   nil,
		IsTerminal: nil,
		LookupEnv:  nil,
	})
	require.ErrorIs(t, err, logging.ErrNoWriter)
}

func TestFormat_Flag(t *testing.T) {
	format := logging.FormatText

	require.ErrorIs(t, format.Set("xml"), logging.ErrUnknownFormat)
	assert.Equal(t, "text", format.String(), "a refused value leaves the format as it was")

	require.NoError(t, format.Set("json"))
	assert.Equal(t, logging.FormatJSON, format)
	assert.Equal(t, "text|json", format.Type())
}

func TestSetup(t *testing.T) {
	previous := slog.Default()

	t.Cleanup(func() { slog.SetDefault(previous) })

	var buf bytes.Buffer

	require.NoError(t, logging.Setup(logging.Options{
		Level:      slog.LevelDebug,
		Format:     logging.FormatText,
		Writer:     &buf,
		Redactor:   sdklog.NewRedactor(),
		IsTerminal: notTerminal,
		LookupEnv:  noEnv,
	}))

	sdklog.Debugf("through the %s", "facade")
	sdklog.Warn("careful", "k", "v")

	assert.Equal(t, "   · through the facade\n   ⚠ careful k=v\n", buf.String())

	require.ErrorIs(t, logging.Setup(logging.Options{
		Level:      slog.LevelInfo,
		Format:     "",
		Writer:     &buf,
		Redactor:   nil,
		IsTerminal: nil,
		LookupEnv:  nil,
	}), logging.ErrUnknownFormat)
}
