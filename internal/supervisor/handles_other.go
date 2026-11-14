//go:build !linux && !darwin

package supervisor

import "syscall"

// handlesSignal tells whether the process pid survives sig. Linux shows the
// signal handling of a process to another one through /proc and macOS
// through sysctl kern.proc.pid: elsewhere every signal is taken as handled.
func handlesSignal(int, syscall.Signal) bool {
	return true
}
