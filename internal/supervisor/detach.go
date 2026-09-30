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
	if start == nil {
		start = StartCmd
	}

	cmd := exec.CommandContext(context.Background(), path, args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err := start(cmd)
	if err != nil {
		return 0, err
	}

	if cmd.Process == nil {
		return 0, ErrNotStarted
	}

	go func() {
		// Nobody reads the exit status of a detached process.
		_ = cmd.Wait()
	}()

	return cmd.Process.Pid, nil
}
