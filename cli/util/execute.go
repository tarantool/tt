package util

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/tarantool/tt/sdk/log"
)

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

		outputBuf, err = os.CreateTemp("", "out")
		if err != nil {
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
			printErr := PrintFromStart(outputBuf)
			if printErr != nil {
				log.Warnf("Failed to show command output: %s", printErr)
			}
		}

		return fmt.Errorf(
			"failed to run \n%s\n\n%w", cmd.String(), err,
		)
	}

	return nil
}

func PrintFromStart(file *os.File) error {
	_, err := file.Seek(0, io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek file begin: %w", err)
	}

	_, err = io.Copy(os.Stdout, file)
	if err != nil {
		log.Warnf("Failed to print file content: %s", err)
	}

	return nil
}
