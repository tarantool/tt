package instances

import (
	"strings"
	"syscall"
)

// signals maps the names of the signals that Linux and macOS share, without
// the SIG prefix, to their numbers on the running system. Names that only one
// of them has (SIGPWR, SIGINFO, ...) are left out, so that a description in
// an application runs on both, and so are numbers: SIGUSR1 is 10 on Linux and
// 30 on macOS.
var signals = map[string]syscall.Signal{
	"ABRT":   syscall.SIGABRT,
	"ALRM":   syscall.SIGALRM,
	"BUS":    syscall.SIGBUS,
	"CHLD":   syscall.SIGCHLD,
	"CONT":   syscall.SIGCONT,
	"FPE":    syscall.SIGFPE,
	"HUP":    syscall.SIGHUP,
	"ILL":    syscall.SIGILL,
	"INT":    syscall.SIGINT,
	"IO":     syscall.SIGIO,
	"KILL":   syscall.SIGKILL,
	"PIPE":   syscall.SIGPIPE,
	"PROF":   syscall.SIGPROF,
	"QUIT":   syscall.SIGQUIT,
	"SEGV":   syscall.SIGSEGV,
	"STOP":   syscall.SIGSTOP,
	"SYS":    syscall.SIGSYS,
	"TERM":   syscall.SIGTERM,
	"TRAP":   syscall.SIGTRAP,
	"TSTP":   syscall.SIGTSTP,
	"TTIN":   syscall.SIGTTIN,
	"TTOU":   syscall.SIGTTOU,
	"URG":    syscall.SIGURG,
	"USR1":   syscall.SIGUSR1,
	"USR2":   syscall.SIGUSR2,
	"VTALRM": syscall.SIGVTALRM,
	"WINCH":  syscall.SIGWINCH,
	"XCPU":   syscall.SIGXCPU,
	"XFSZ":   syscall.SIGXFSZ,
}

// parseSignal returns the signal a name stands for: SIGTERM and TERM both
// stand for SIGTERM. The name is case-sensitive, as in systemd's KillSignal=.
func parseSignal(name string) (syscall.Signal, bool) {
	sig, ok := signals[strings.TrimPrefix(name, "SIG")]

	return sig, ok
}
