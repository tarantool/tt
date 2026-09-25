package logtest_test

import (
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"testing/slogtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
)

func TestRecorderConformsToSlog(t *testing.T) {
	t.Parallel()

	recorder := logtest.NewRecorder()

	err := slogtest.TestHandler(recorder, func() []map[string]any {
		records := recorder.Records()
		maps := make([]map[string]any, 0, len(records))

		for _, record := range records {
			maps = append(maps, record.Map())
		}

		return maps
	})
	require.NoError(t, err)
}

func TestRecorderCapturesTree(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	logger.With("a", 1).WithGroup("g").With("b", 2).Debug("hello", "c", 3)
	logger.WithGroup("empty").Info("bare")

	records := recorder.Records()
	require.Len(t, records, 2)

	assert.Equal(t, slog.LevelDebug, records[0].Level)
	assert.Equal(t, map[string]any{
		slog.TimeKey:    records[0].Time,
		slog.LevelKey:   slog.LevelDebug,
		slog.MessageKey: "hello",
		"a":             int64(1),
		"g":             map[string]any{"b": int64(2), "c": int64(3)},
	}, records[0].Map())

	assert.Empty(t, records[1].Attrs)
	assert.Equal(t, []string{"hello", "bare"}, recorder.Messages())
}

func TestRecorderSource(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	_, file, line, ok := runtime.Caller(0)
	require.True(t, ok)
	logger.Info("here")

	records := recorder.Records()
	require.Len(t, records, 1)

	source := records[0].Source()
	assert.Equal(t, file, source.File)
	assert.Equal(t, line+2, source.Line)
}

func TestRecorderConcurrent(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	const writers = 8

	var group sync.WaitGroup

	for i := range writers {
		group.Go(func() {
			logger.With("writer", i).Info("line")
		})
	}

	group.Wait()

	assert.Len(t, recorder.Records(), writers)
}
