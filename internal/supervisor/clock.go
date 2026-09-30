package supervisor

import "time"

// clock makes the timers the engine waits on: the restart delay, the stop
// timeout and the check period. Tests replace it with one they advance.
type clock interface {
	// after returns a channel that receives once d has passed, and a function
	// that cancels it.
	after(d time.Duration) (<-chan time.Time, func())
	// every returns a channel that receives each time d has passed, and a
	// function that stops it.
	every(d time.Duration) (<-chan time.Time, func())
}

type realClock struct{}

func (realClock) after(d time.Duration) (<-chan time.Time, func()) {
	timer := time.NewTimer(d)

	return timer.C, func() { timer.Stop() }
}

func (realClock) every(d time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(d)

	return ticker.C, ticker.Stop
}
