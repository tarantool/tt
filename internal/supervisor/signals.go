package supervisor

import (
	"os"
	"os/signal"
	"syscall"
)

// signalBuffer is how many signals wait for the event loop before new ones
// are lost.
const signalBuffer = 128

// subscribeOS relays every signal the process receives, except SIGURG and
// SIGCHLD, into the returned channel until the returned function is called.
//
// The relay runs on its own goroutine so that the runtime's preemption
// signals, which arrive at any time, never take a place in the buffer the
// event loop reads from while it is busy starting a child.
func subscribeOS() (<-chan os.Signal, func()) {
	raw := make(chan os.Signal, signalBuffer)

	signal.Notify(raw)

	// No signal reaches raw once Stop returns, so closing it is safe.
	return relay(raw, func() { signal.Stop(raw) })
}

// relay passes the signals from raw on to the returned channel, all but
// SIGURG and SIGCHLD, dropping those that find it full, until the returned
// function is called. That function calls detach, which has to stop the
// delivery into raw, closes raw and waits for the relay to finish.
func relay(raw chan os.Signal, detach func()) (<-chan os.Signal, func()) {
	relayed := make(chan os.Signal, signalBuffer)
	done := make(chan struct{})

	go func() {
		defer close(done)

		for sig := range raw {
			if sig == syscall.SIGURG || sig == syscall.SIGCHLD {
				continue
			}

			select {
			case relayed <- sig:
			default:
			}
		}
	}()

	return relayed, func() {
		detach()
		close(raw)
		<-done
	}
}
