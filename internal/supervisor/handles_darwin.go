package supervisor

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// handlesSignal tells whether the process pid survives sig: it catches the
// signal or ignores it. A program that has only just been started does
// neither until it sets up its signal handling and dies of a signal whose
// default action ends it. The dispositions come from the kinfo_proc of the
// process, sysctl kern.proc.pid; macOS shows no blocked mask of another
// process there, and has no signalfd that would need one: a program that
// reads its signals from a kqueue ignores them. It reports true when it
// cannot tell, as for a process that is gone or a signal out of the range of
// the masks.
func handlesSignal(pid int, sig syscall.Signal) bool {
	if sig < 1 || sig > 32 {
		return true
	}

	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(info.Proc.P_pid) != pid {
		return true
	}

	bit := uint32(1) << (sig - 1)

	return (info.Proc.P_sigignore|info.Proc.P_sigcatch)&bit != 0
}
