package checkpoint

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/tarantool/tt/cli/cmdcontext"
)

var (
	errResultOfPlay = errors.New("result of play: ")
)

// Opts contains flags for managing checkpoint files commands.
// Used for commands tt cat and tt play, which are checkpoint files commands.
type Opts struct {
	To         uint64
	Timestamp  string
	From       uint64
	Space      []int
	Format     string
	Replica    []int
	ShowSystem bool
	Recursive  bool
}

// Cat print the contents of .snap/.xlog files.
// Returns an error if such occur during reading files.
func Cat(tntCli cmdcontext.TarantoolCli) error {
	cmd := exec.CommandContext(context.Background(), tntCli.Executable, "-")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdinPipe.Write([]byte(catFile))
	stdinPipe.Close()

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("result of cat: %w", err)
	}

	return nil
}

// Play is playing the contents of .snap/.xlog files to another Tarantool instance.
// Returns an error if such occur during playing.
func Play(tntCli cmdcontext.TarantoolCli) error {
	var errBuff bytes.Buffer
	cmd := exec.CommandContext(context.Background(), tntCli.Executable, "-")
	cmd.Stderr = &errBuff

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdinPipe.Write([]byte(playFile))
	stdinPipe.Close()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w%s", errResultOfPlay, errBuff.String())
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		fmt.Fprintln(os.Stdout, scanner.Text())
	}
	cmd.Wait()

	if len(errBuff.String()) > 0 {
		return fmt.Errorf("%w%s", errResultOfPlay, errBuff.String())
	}

	return nil
}
