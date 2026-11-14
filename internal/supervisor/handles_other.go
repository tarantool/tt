//go:build !linux

package supervisor

import "syscall"

// handlesSignal tells whether the process pid survives sig. Only Linux shows
// the signal handling of a process to another one, through /proc: elsewhere
// every signal is taken as handled.
func handlesSignal(int, syscall.Signal) bool {
	return true
}
