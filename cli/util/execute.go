package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/log"
)

var errHookShouldBeExecutable = errors.New("hook `")

// execOwnerPerm is a bitmask to check owner exec bit.
const execOwnerPerm uint32 = 0o100

// RunCommand runs specified command and returns an error.
// If showOutput is set to true, command output is shown.
// Else the output is kept aside, shown only if the command fails, and a
// spinner is shown on the log's terminal while the command runs.
func RunCommand(cmd *exec.Cmd, workingDir string, showOutput bool) error {
	var outputBuf *os.File

	stopSpinner := func() {}

	cmd.Dir = workingDir
	if showOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		var err error

		if outputBuf, err = os.CreateTemp("", "out"); err != nil {
			return fmt.Errorf("failed to create tmp file to store command output: %w", err)
		}

		cmd.Stdout = outputBuf
		cmd.Stderr = outputBuf

		defer func() {
			_ = outputBuf.Close()
		}()
		defer func() {
			_ = os.Remove(outputBuf.Name())
		}()

		stopSpinner = log.Spinner("running " + strings.Join(cmd.Args, " "))
	}

	err := cmd.Run()

	// The spinner leaves the line before the kept output is shown.
	stopSpinner()

	if err != nil {
		if outputBuf != nil {
			if err := PrintFromStart(outputBuf); err != nil {
				log.Warnf("Failed to show command output: %s", err)
			}
		}

		return fmt.Errorf(
			"failed to run \n%s\n\n%w", cmd.String(), err,
		)
	}

	return nil
}

// RunHook runs the specified hook.
// If showOutput is set to true, command output is shown.
func RunHook(hookPath string, showOutput bool) error {
	hookName := filepath.Base(hookPath)
	hookDir := filepath.Dir(hookPath)

	if isExec, err := IsExecOwner(hookPath); err != nil {
		return fmt.Errorf("failed go check hook file `%s`: %w", hookName, err)
	} else if !isExec {
		return fmt.Errorf("%w%s` should be executable", errHookShouldBeExecutable, hookName)
	}

	hookCmd := exec.CommandContext(context.Background(), hookPath)

	err := RunCommand(hookCmd, hookDir, showOutput)
	if err != nil {
		return fmt.Errorf("failed to run hook `%s`: %w", hookName, err)
	}

	return nil
}

// IsExecOwner checks if specified file has owner execute permissions.
func IsExecOwner(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	perm := fileInfo.Mode().Perm()

	return BitHas32(uint32(perm), execOwnerPerm), nil
}

func PrintFromStart(file *os.File) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek file begin: %w", err)
	}

	if _, err := io.Copy(os.Stdout, file); err != nil {
		log.Warnf("Failed to print file content: %s", err)
	}

	return nil
}

// ExecuteCommandGetOutput executes program with given args in verbose or quiet mode
// and sends stdinData to stdin pipe.
func ExecuteCommandGetOutput(program, workDir string, stdinData []byte,
	args ...string,
) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), program, args...)

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = &out

	if workDir == "" {
		var err error

		if workDir, err = os.Getwd(); err != nil {
			return out.Bytes(), err
		}
	}

	cmd.Dir = workDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return out.Bytes(), err
	}

	err = cmd.Start()
	if err != nil {
		return out.Bytes(), err
	}

	_, _ = stdin.Write(stdinData)
	_ = stdin.Close()

	err = cmd.Wait()

	return out.Bytes(), err
}
