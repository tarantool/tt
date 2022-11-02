package util

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/apex/log"
	"github.com/briandowns/spinner"
)

type emptyStruct struct{}

// readyChan is a channel used to signal completion of command execution.
type readyChan chan emptyStruct

// execOwnerPerm is a bitmask to check owner exec bit.
const execOwnerPerm uint32 = 0100

var (
	spinnerPicture    = spinner.CharSets[9]
	spinnerUpdateTime = 100 * time.Millisecond

	ready = emptyStruct{}
)

// sendReady sends ready to channel.
func sendReady(readyChannel readyChan) {
	readyChannel <- ready
}

// startAndWaitCommand executes a command.
// and sends `ready` flag to the channel before return.
func startAndWaitCommand(cmd *exec.Cmd, readyChannel readyChan,
	workGroup *sync.WaitGroup, err *error) {
	defer workGroup.Done()
	defer sendReady(readyChannel)

	if *err = cmd.Start(); *err != nil {
		return
	}

	if *err = cmd.Wait(); *err != nil {
		return
	}
}

// StartCommandSpinner starts running spinner.
// until `ready` flag is received from the channel.
func StartCommandSpinner(readyChannel readyChan, wg *sync.WaitGroup, prefix string) {
	defer wg.Done()

	spinner := spinner.New(spinnerPicture, spinnerUpdateTime)
	if prefix != "" {
		spinner.Prefix = fmt.Sprintf("%s ", strings.TrimSpace(prefix))
	}

	spinner.Start()

	// Wait for the command to complete.
	<-readyChannel

	spinner.Stop()
}

// RunCommand runs specified command and returns an error.
// If showOutput is set to true, command output is shown.
// Else spinner is shown while command is running.
func RunCommand(cmd *exec.Cmd, workingDir string, showOutput bool) error {
	var err error
	var workGroup sync.WaitGroup
	readyChannel := make(readyChan, 1)

	var outputBuf *os.File

	cmd.Dir = workingDir
	if showOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		if outputBuf, err = ioutil.TempFile("", "out"); err != nil {
			log.Warnf("Failed to create tmp file to store command output: %s", err)
		}
		cmd.Stdout = outputBuf
		cmd.Stderr = outputBuf
		defer outputBuf.Close()
		defer os.Remove(outputBuf.Name())

		if isatty.IsTerminal(os.Stdout.Fd()) {
			workGroup.Add(1)
			go StartCommandSpinner(readyChannel, &workGroup, "")
		}
	}

	workGroup.Add(1)
	go startAndWaitCommand(cmd, readyChannel, &workGroup, &err)

	workGroup.Wait()

	if err != nil {
		if outputBuf != nil {
			if err := PrintFromStart(outputBuf); err != nil {
				log.Warnf("Failed to show command output: %s", err)
			}
		}

		return fmt.Errorf(
			"Failed to run \n%s\n\n%s", cmd.String(), err,
		)
	}

	return err
}

// RunHook runs the specified hook.
// If showOutput is set to true, command output is shown.
func RunHook(hookPath string, showOutput bool) error {
	hookName := filepath.Base(hookPath)
	hookDir := filepath.Dir(hookPath)

	if isExec, err := IsExecOwner(hookPath); err != nil {
		return fmt.Errorf("Failed go check hook file `%s`: %s", hookName, err)
	} else if !isExec {
		return fmt.Errorf("Hook `%s` should be executable", hookName)
	}

	hookCmd := exec.Command(hookPath)
	err := RunCommand(hookCmd, hookDir, showOutput)
	if err != nil {
		return fmt.Errorf("Failed to run hook `%s`: %s", hookName, err)
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
		return fmt.Errorf("Failed to seek file begin: %s", err)
	}
	if _, err := io.Copy(os.Stdout, file); err != nil {
		log.Warnf("Failed to print file content: %s", err)
	}

	return nil
}

// ExecuteCommandStdin executes program with given args in verbose or quiet mode
// and sends stdinData to stdin pipe.
func ExecuteCommandGetOutput(program string, workDir string, stdinData []byte,
	args ...string) ([]byte, error) {
	cmd := exec.Command(program, args...)

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

	stdin.Write(stdinData)
	stdin.Close()

	err = cmd.Wait()
	return out.Bytes(), err
}
