package coredump

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/tarantool/tt/sdk/output"
)

// The debuggers, by the names the debuggers of an extension list.
const (
	debuggerGDB  = "gdb"
	debuggerLLDB = "lldb"
)

// debugger is a debugger inspect opens the artefacts of a core in. inspect
// hands it the extensions that load in it, by its name, in their load
// order, and runs the command it builds (see runDebugger).
type debugger interface {
	// Name is the name of the debugger, as the debuggers of an extension
	// list it.
	Name() string
	// Command returns the command that opens the artefacts in dir in the
	// debugger, with the sources in sourceDir when it is not empty, and
	// loads the extensions at the paths exts, in their order. work is a
	// directory of inspect's own for the files the command needs; it
	// outlives the command.
	Command(ctx context.Context, work, dir, sourceDir string, exts []string) (*exec.Cmd, error)
}

// gdbDebugger is gdb, run through the gdb.sh tt embeds, never the one of
// the archive: an archive packed by an older tt carries the wrapper of that
// tt. The wrapper sources the extensions it is given once the core is
// loaded.
type gdbDebugger struct{}

// Name implements debugger.
func (gdbDebugger) Name() string {
	return debuggerGDB
}

// Command implements debugger: sh runs the wrapper, written into work, so
// that a temporary directory mounted noexec runs it too.
func (gdbDebugger) Command(ctx context.Context, work, dir, sourceDir string, exts []string,
) (*exec.Cmd, error) {
	script := filepath.Join(work, filepath.Base(inspectEmbedPath))

	err := copyEmbedded(coreScripts, inspectEmbedPath, script, scriptFileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to write the inspect script: %w", err)
	}

	args, err := inspectArgs(script, dir, sourceDir, exts)
	if err != nil {
		return nil, err
	}

	return exec.CommandContext(ctx, "sh", args...), nil
}

// inspectArgs returns the arguments of sh that run the gdb wrapper script
// on the artefacts in dir, with the sources in sourceDir when it is not
// empty and the extensions exts. The directories are made absolute: gdb
// runs in the working directory of tt.
func inspectArgs(script, dir, sourceDir string, exts []string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %q: %w", dir, err)
	}

	args := []string{script, "-d", abs}

	if sourceDir != "" {
		abs, err = filepath.Abs(sourceDir)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve %q: %w", sourceDir, err)
		}

		args = append(args, "-s", abs)
	}

	for _, ext := range exts {
		args = append(args, "-x", ext)
	}

	return args, nil
}

// runDebugger runs cmd, the command of a debugger, on streams until it
// exits: the debugger reads the standard input of tt.
//
// Ctrl-C at the prompt of the debugger interrupts the command it runs: the
// terminal sends SIGINT to tt as well, which must outlive the debugger
// rather than die of it and leave the debugger reading a terminal the shell
// has taken back. A signal caught rather than ignored is reset for the
// child.
func runDebugger(cmd *exec.Cmd, streams output.Streams) error {
	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err

	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)

	defer signal.Stop(interrupts)

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("inspect script execution failed: %w", err)
	}

	return nil
}
