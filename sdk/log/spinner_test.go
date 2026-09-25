package log_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/log/logtest"
)

// statusLog is what a statusRecorder and the handlers derived from it saw.
type statusLog struct {
	shown   []string
	stopped int
}

// statusRecorder is a StatusHandler that records the status lines asked of
// it and how many were stopped. Handlers derived from it share its log.
type statusRecorder struct {
	*logtest.Recorder

	log *statusLog
}

func newStatusRecorder() *statusRecorder {
	return &statusRecorder{Recorder: logtest.NewRecorder(), log: &statusLog{}}
}

func (s *statusRecorder) Status(msg string) func() {
	s.log.shown = append(s.log.shown, msg)

	return func() { s.log.stopped++ }
}

func (s *statusRecorder) Handle(ctx context.Context, record slog.Record) error {
	return s.Recorder.Handle(ctx, record) //nolint:wrapcheck // Transparent.
}

func (s *statusRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	recorder, _ := s.Recorder.WithAttrs(attrs).(*logtest.Recorder)

	return &statusRecorder{Recorder: recorder, log: s.log}
}

func TestSpinnerOfStatusHandler(t *testing.T) {
	t.Parallel()

	handler := newStatusRecorder()

	stop := log.SpinnerOf(slog.New(handler), "working")
	stop()

	assert.Equal(t, []string{"working"}, handler.log.shown)
	assert.Equal(t, 1, handler.log.stopped)
}

func TestSpinnerOfLibraryLogger(t *testing.T) {
	t.Parallel()

	handler := newStatusRecorder()

	log.SpinnerOf(log.LibraryOf(slog.New(handler), "lib"), "fetching")()

	assert.Equal(t, []string{"fetching"}, handler.log.shown)
	assert.Equal(t, 1, handler.log.stopped)
}

func TestSpinnerOfPlainHandler(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	stop := log.SpinnerOf(logger, "working")
	stop()
	stop()

	assert.Empty(t, recorder.Records())
}
