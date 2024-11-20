package checkpoint

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"

	"github.com/tarantool/tt/cli/cmdcontext"
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
}

// Cat print the contents of .snap/.xlog files.
// Returns an error if such occur during reading files.
func Cat(tntCli cmdcontext.TarantoolCli) error {
	cmd := exec.Command(tntCli.Executable, "-")
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
	var errbuff bytes.Buffer
	cmd := exec.Command(tntCli.Executable, "-")
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
		return fmt.Errorf("result of play: %s", errbuff.String())
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	cmd.Wait()

	if len(errbuff.String()) > 0 {
		return fmt.Errorf("result of play: %s", errbuff.String())
	}

	return nil
}
