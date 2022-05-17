package checkpoint

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"

	"github.com/tarantool/tt/cli/cmdcontext"
)

// Opts contains flags for managing checkpoint files commands.
// Used for commands tt cat and tt play, which are checkpoint files commands.
type Opts struct {
	To         uint64
	From       uint64
	Space      []int
	Format     string
	Replica    []int
	ShowSystem bool
}

// Cat print the contents of .snap/.xlog files.
// Returns an error if such occur during reading files.
func Cat(cmdCtx *cmdcontext.CmdCtx) error {
	var errbuff bytes.Buffer
	cmd := exec.Command(cmdCtx.Cli.TarantoolExecutable, "-")
	cmd.Stderr = &errbuff

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdinPipe.Write([]byte(catFile))
	stdinPipe.Close()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Result of cat: %s", errbuff.String())
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	cmd.Wait()

	if len(errbuff.String()) > 0 {
		return fmt.Errorf("Result of cat: %s", errbuff.String())
	}

	return nil
}

// Play is playing the contents of .snap/.xlog files to another Tarantool instance.
// Returns an error if such occur during playing.
func Play(cmdCtx *cmdcontext.CmdCtx) error {
	var errbuff bytes.Buffer
	cmd := exec.Command(cmdCtx.Cli.TarantoolExecutable, "-")
	cmd.Stderr = &errbuff

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
		return fmt.Errorf("Result of play: %s", errbuff.String())
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	cmd.Wait()

	if len(errbuff.String()) > 0 {
		return fmt.Errorf("Result of play: %s", errbuff.String())
	}

	return nil
}
