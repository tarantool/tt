package logging

import (
	"io"
	"log/slog"
	"time"
)

// NewHandlerOnTerminal builds the handler opts describe as NewHandler does,
// but measures the terminal as width columns and advances a spinner once
// per value received from ticks instead of on a timer.
func NewHandlerOnTerminal(
	opts Options, width int, ticks <-chan time.Time,
) (slog.Handler, error) {
	return newHandler(opts, terminalHooks{
		width: func(io.Writer) int { return width },
		ticks: func() (<-chan time.Time, func()) { return ticks, func() {} },
	})
}
