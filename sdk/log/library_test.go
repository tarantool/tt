package log_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/log/logtest"
)

func TestLibraryDemotesInfo(t *testing.T) {
	t.Parallel()

	base, recorder := logtest.New(t)
	library := log.LibraryOf(base, "luarocks")

	library.Debug("debug")
	library.Info("luaEngine stdout", "line", "x")
	library.Log(t.Context(), slog.LevelInfo+2, "info plus two")
	library.Warn("warn")
	library.Error("error")

	records := recorder.Records()
	require.Len(t, records, 5)

	want := []struct {
		level   slog.Level
		message string
	}{
		{slog.LevelDebug, "debug"},
		{slog.LevelDebug, "luaEngine stdout"},
		{slog.LevelDebug + 2, "info plus two"},
		{slog.LevelWarn, "warn"},
		{slog.LevelError, "error"},
	}

	for i, record := range records {
		assert.Equal(t, want[i].level, record.Level, want[i].message)
		assert.Equal(t, want[i].message, record.Message)
		assert.Equal(t, "luarocks", record.Map()[log.LibraryKey], want[i].message)
	}

	assert.Equal(t, "x", records[1].Map()["line"])
}

func TestLibraryKeepsGroupsAndAttrs(t *testing.T) {
	t.Parallel()

	base, recorder := logtest.New(t)

	log.LibraryOf(base, "etcd").With("a", 1).WithGroup("g").Info("m", "b", 2)

	records := recorder.Records()
	require.Len(t, records, 1)
	assert.Equal(t, slog.LevelDebug, records[0].Level)

	fields := records[0].Map()
	assert.Equal(t, "etcd", fields[log.LibraryKey])
	assert.Equal(t, int64(1), fields["a"])
	assert.Equal(t, map[string]any{"b": int64(2)}, fields["g"])
}

func TestLibraryInfoHiddenAtInfoLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	base := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		AddSource:   false,
		Level:       slog.LevelInfo,
		ReplaceAttr: nil,
	}))
	library := log.LibraryOf(base, "luarocks")

	assert.False(t, library.Enabled(t.Context(), slog.LevelInfo))
	assert.True(t, library.Enabled(t.Context(), slog.LevelWarn))

	library.Info("chatter")
	library.Warn("problem")

	assert.NotContains(t, buf.String(), "chatter")
	assert.Contains(t, buf.String(), "problem")
	assert.Contains(t, buf.String(), "library=luarocks")
}

func TestLibraryUsesDefault(t *testing.T) {
	logger, recorder := logtest.New(t)
	useDefault(t, logger)

	log.Library("luarocks").Info("x")

	records := recorder.Records()
	require.Len(t, records, 1)
	assert.Equal(t, slog.LevelDebug, records[0].Level)
	assert.Equal(t, "luarocks", records[0].Map()[log.LibraryKey])
}
