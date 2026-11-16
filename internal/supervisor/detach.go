package supervisor

import (
	"context"
	"os/exec"
	"syscall"
)

// Detach starts path with args in a process group of its own and does not
// wait for it: the process keeps running after the caller exits and is not
// stopped by signals sent to the caller's group. Its standard streams are
// connected to the null device and it inherits the caller's environment.
// start starts the process; nil means StartCmd. Detach returns the pid.
//
// While the caller lives, a goroutine waits for the process, so one that
// exits before the caller does not stay behind as a zombie.
func Detach(path string, args []string, start StartFunc) (int, error) {
	pid, _, err := DetachWatched(path, args, start)

	return pid, err
}

// DetachWatched is Detach that also returns a channel closed once the
// process has exited, for a caller that waits a while for the process to
// get going while it lives.
func DetachWatched(path string, args []string, start StartFunc) (int, <-chan struct{}, error) {
	if start == nil {
		start = StartCmd
	}

	cmd := exec.CommandContext(context.Background(), path, args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err := start(cmd)
	if err != nil {
		return 0, nil, err
	}

	if cmd.Process == nil {
		return 0, nil, ErrNotStarted
	}

	exited := make(chan struct{})

	go func() {
		// Nobody reads the exit status of a detached process.
		_ = cmd.Wait()

		close(exited)
	}()

	return cmd.Process.Pid, exited, nil
}
