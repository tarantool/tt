package coredump

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/output"
)

// Inspect opens the core dump of archiveOrDir, an archive tt coredump pack
// made or the directory it unpacks into, in gdb, with the sources in
// sourceDir when it is not empty.
//
// gdb runs through the gdb.sh tt embeds, never the one of the archive: an
// archive packed by an older tt carries the wrapper of that tt. The wrapper
// sources the gdb extensions tt selects explicitly once the core is loaded.
func Inspect(streams output.Streams, archiveOrDir, sourceDir string) error {
	stat, err := os.Stat(archiveOrDir)
	if err != nil {
		return fmt.Errorf("failed to inspect: %w", err)
	}

	dir := archiveOrDir

	if !stat.IsDir() {
		// It seems archive was specified, so try to unpack into
		// temporary directory.
		tmpDir, err := os.MkdirTemp(os.TempDir(), "tt-coredump-*")
		if err != nil {
			return fmt.Errorf("cannot create a temporary directory for unpacking: %w", err)
		}

		defer func() {
			_ = os.RemoveAll(tmpDir)
		}() // Clean up on function return.

		err = extractTarGz(archiveOrDir, tmpDir)
		if err != nil {
			return fmt.Errorf("failed to unpack: %w", err)
		}

		// Directory name is archive basename w/o extensions.
		dir = filepath.Join(tmpDir, strings.Split(filepath.Base(archiveOrDir), ".")[0])
	}

	// The wrapper and the extensions tt loads live in a directory of their
	// own: the one of the artefacts may be the user's.
	work, err := os.MkdirTemp(os.TempDir(), "tt-coredump-*")
	if err != nil {
		return fmt.Errorf("cannot create a temporary directory for inspecting: %w", err)
	}

	defer func() {
		_ = os.RemoveAll(work)
	}()

	script := filepath.Join(work, filepath.Base(inspectEmbedPath))

	err = copyEmbedded(coreScripts, inspectEmbedPath, script, scriptFileMode)
	if err != nil {
		return fmt.Errorf("failed to write the inspect script: %w", err)
	}

	exts, err := writeExtensions(work)
	if err != nil {
		return fmt.Errorf("failed to write the GDB-extensions: %w", err)
	}

	args, err := inspectArgs(script, dir, sourceDir, exts)
	if err != nil {
		return err
	}

	// The wrapper is run by sh, so that a temporary directory mounted
	// noexec runs it too; gdb reads the standard input of tt.
	cmd := exec.CommandContext(context.Background(), "sh", args...)

	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("inspect script execution failed: %w", err)
	}

	return nil
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
