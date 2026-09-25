package log_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/log/logtest"
)

// useDefault makes logger the process logger for the duration of the test.
// Tests calling it change process state and must not run in parallel.
func useDefault(t *testing.T, logger *slog.Logger) {
	t.Helper()

	previous := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })
}

// funcSource returns where fn is defined. For a one-line function literal
// that is the line of the call inside it.
func funcSource(t *testing.T, fn func()) slog.Source {
	t.Helper()

	info := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	require.NotNil(t, info)

	file, line := info.FileLine(info.Entry())

	return slog.Source{Function: info.Name(), File: file, Line: line}
}

func TestFacadeLevelsAndMessages(t *testing.T) {
	logger, recorder := logtest.New(t)
	useDefault(t, logger)

	log.Debugf("debug %d", 1)
	log.Infof("info %s", "two")
	log.Warnf("warn %v", 3.5)
	log.Errorf("error %q", "four")
	log.Infof("100%%")
	log.Info("50% plain", "key", "value")
	log.Debug("d")
	log.Warn("w")
	log.Error("e", slog.Int("n", 7))

	records := recorder.Records()
	require.Len(t, records, 9)

	levels := make([]slog.Level, 0, len(records))
	for _, record := range records {
		levels = append(levels, record.Level)
	}

	assert.Equal(t, []slog.Level{
		slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError,
		slog.LevelInfo, slog.LevelInfo, slog.LevelDebug, slog.LevelWarn, slog.LevelError,
	}, levels)
	assert.Equal(t, []string{
		"debug 1", "info two", "warn 3.5", `error "four"`,
		"100%", "50% plain", "d", "w", "e",
	}, recorder.Messages())
	assert.Equal(t, "value", records[5].Map()["key"])
	assert.Equal(t, int64(7), records[8].Map()["n"])
}

func TestFacadeRecordsCallerSource(t *testing.T) {
	logger, recorder := logtest.New(t)
	useDefault(t, logger)

	calls := []struct {
		name string
		log  func()
	}{
		{"Debugf", func() { log.Debugf("x") }},
		{"Infof", func() { log.Infof("x") }},
		{"Warnf", func() { log.Warnf("x") }},
		{"Errorf", func() { log.Errorf("x") }},
		{"Debug", func() { log.Debug("x") }},
		{"Info", func() { log.Info("x") }},
		{"Warn", func() { log.Warn("x") }},
		{"Error", func() { log.Error("x") }},
	}

	for index, call := range calls {
		call.log()

		records := recorder.Records()
		require.Len(t, records, index+1, call.name)

		want := funcSource(t, call.log)
		got := records[index].Source()

		assert.Equal(t, want, got, call.name)
	}
}

func TestFacadeSourceThroughAddSource(t *testing.T) {
	var buf bytes.Buffer

	useDefault(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		AddSource:   true,
		Level:       slog.LevelDebug,
		ReplaceAttr: nil,
	})))

	logHello := func() { log.Infof("hello") }
	logHello()

	want := funcSource(t, logHello)
	assert.Contains(t, buf.String(), fmt.Sprintf("%s:%d", filepath.Base(want.File), want.Line))
}

func TestFacadeSkipsDisabledLevels(t *testing.T) {
	var buf bytes.Buffer

	useDefault(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		AddSource:   false,
		Level:       slog.LevelWarn,
		ReplaceAttr: nil,
	})))

	log.Debugf("hidden")
	log.Info("hidden")
	log.Warnf("shown")

	assert.NotContains(t, buf.String(), "hidden")
	assert.Contains(t, buf.String(), "shown")
}

func TestLoggerIsDefault(t *testing.T) {
	logger, _ := logtest.New(t)
	useDefault(t, logger)

	assert.Same(t, logger, log.Logger())
}
