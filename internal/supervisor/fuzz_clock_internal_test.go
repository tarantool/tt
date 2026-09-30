package supervisor

import (
	"slices"
	"sync"
	"time"
)

// fakeTimer is a timer or ticker of the fake clock.
type fakeTimer struct {
	at, period time.Duration
	ch         chan time.Time
	stopped    bool
	// delivered counts the ticks that went into ch.
	delivered int
}

// fakeClock is a clock that moves only when the script advances it.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTimer
	// tickers are all the tickers ever made, stopped ones too.
	tickers []*fakeTimer
}

func (clk *fakeClock) after(d time.Duration) (<-chan time.Time, func()) {
	return clk.add(d, 0)
}

func (clk *fakeClock) every(d time.Duration) (<-chan time.Time, func()) {
	return clk.add(d, d)
}

func (clk *fakeClock) add(delay, period time.Duration) (<-chan time.Time, func()) {
	clk.mu.Lock()
	defer clk.mu.Unlock()

	timer := &fakeTimer{at: clk.now + delay, period: period, ch: make(chan time.Time, 1)}

	switch {
	case delay <= 0 && period == 0:
		// An expired timer is ready at once, as a real one is.
		timer.ch <- time.Time{}

		timer.stopped = true
	case period > 0:
		clk.timers = append(clk.timers, timer)
		clk.tickers = append(clk.tickers, timer)
	default:
		clk.timers = append(clk.timers, timer)
	}

	return timer.ch, func() {
		clk.mu.Lock()
		defer clk.mu.Unlock()

		timer.stopped = true
	}
}

// fakeTicker is what the model sees of a ticker of the fake clock.
type fakeTicker struct {
	// pending tells that a tick waits in the channel for its reader.
	pending bool
}

// activeTickers are the tickers made by every and not stopped.
func (clk *fakeClock) activeTickers() []fakeTicker {
	clk.mu.Lock()
	defer clk.mu.Unlock()

	var tickers []fakeTicker

	for _, timer := range clk.timers {
		if timer.period > 0 && !timer.stopped {
			tickers = append(tickers, fakeTicker{pending: len(timer.ch) > 0})
		}
	}

	return tickers
}

// consumedTicks counts the ticks that the readers of all the tickers ever made
// have taken from their channels.
func (clk *fakeClock) consumedTicks() int {
	clk.mu.Lock()
	defer clk.mu.Unlock()

	consumed := 0

	for _, ticker := range clk.tickers {
		consumed += ticker.delivered - len(ticker.ch)
	}

	return consumed
}

// advance moves the time and fires what is due, in order. A tick that finds
// its channel full is dropped, as with a real ticker.
func (clk *fakeClock) advance(d time.Duration) {
	clk.mu.Lock()
	defer clk.mu.Unlock()

	target := clk.now + d

	for {
		var next *fakeTimer

		for _, timer := range clk.timers {
			if !timer.stopped && timer.at <= target && (next == nil || timer.at < next.at) {
				next = timer
			}
		}

		if next == nil {
			break
		}

		clk.now = next.at

		select {
		case next.ch <- time.Time{}:
			next.delivered++
		default:
		}

		if next.period > 0 {
			next.at += next.period
		} else {
			next.stopped = true
		}
	}

	clk.now = target
	clk.timers = slices.DeleteFunc(clk.timers, func(timer *fakeTimer) bool {
		return timer.stopped
	})
}
