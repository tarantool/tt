// Package coredump packs a core dump of Tarantool with what gdb needs to
// read it into a tar.gz archive, unpacks such an archive, and opens it in
// gdb. The packing is tarabrt.sh, the opening gdb.sh: tt runs the scripts it
// embeds, and the archive carries gdb.sh and the gdb extensions for a user
// without tt.
package coredump

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/output"
)

//go:embed scripts/*
var coreScripts embed.FS

//go:embed extensions
var extensions embed.FS

const (
	packEmbedPath       = "scripts/tarabrt.sh" // spell-checker:disable-line
	inspectEmbedPath    = "scripts/gdb.sh"
	scriptFileMode      = 0o755
	extensionFileMode   = 0o644
	commandArgsCapacity = 2
)

// PackOptions are what Pack packs and where.
type PackOptions struct {
	// Core is the core dump.
	Core string
	// Executable is the Tarantool that dumped it; empty is the tarantool of
	// PATH.
	Executable string
	// Dir is the directory the archive is created in; empty is the working
	// directory.
	Dir string
	// PID is the PID of the dumped process, 0 for none.
	PID uint
	// Time is the time of the dump in seconds since the Epoch; empty is
	// now.
	Time string
}

// packArgs returns the arguments of the pack script for opts, with the
// paths absolute: the script packs the files through symbolic links to
// them.
func packArgs(opts PackOptions) ([]string, error) {
	paths := []struct {
		flag, path string
	}{{"-c", opts.Core}, {"-e", opts.Executable}, {"-d", opts.Dir}}

	var args []string

	for _, path := range paths {
		if path.path == "" {
			continue
		}

		abs, err := filepath.Abs(path.path)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve %q: %w", path.path, err)
		}

		args = append(args, path.flag, abs)
	}

	if opts.PID != 0 {
		args = append(args, "-p", strconv.FormatUint(uint64(opts.PID), 10))
	}

	if opts.Time != "" {
		args = append(args, "-t", opts.Time)
	}

	return args, nil
}

// Pack packs a core dump into a tar.gz archive.
func Pack(streams output.Streams, opts PackOptions) error {
	scriptArgs, err := packArgs(opts)
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp(os.TempDir(), "tt-coredump-*")
	if err != nil {
		return fmt.Errorf("cannot create a temporary directory for archiving: %w", err)
	}

	defer func() {
		_ = os.RemoveAll(tmpDir)
	}() // Clean up on function return.

	// Prepare gdb wrapper for packing.
	inspectPath := filepath.Join(tmpDir, filepath.Base(inspectEmbedPath))

	err = copyEmbedded(coreScripts, inspectEmbedPath, inspectPath, scriptFileMode)
	if err != nil {
		return fmt.Errorf("failed to put the inspecting script into the archive: %w", err)
	}

	scriptArgs = append(scriptArgs, "-g", inspectPath)

	// Prepare gdb extensions for packing.
	const extDirName = "extensions"

	extEntries, err := extensions.ReadDir(extDirName)
	if err != nil {
		return fmt.Errorf("failed to find embedded GDB-extensions: %w", err)
	}

	for _, extEntry := range extEntries {
		extSrc := filepath.Join(extDirName, extEntry.Name())
		extDst := filepath.Join(tmpDir, extEntry.Name())

		err = copyEmbedded(extensions, extSrc, extDst, extensionFileMode)
		if err != nil {
			return fmt.Errorf("failed to put GDB-extension into the archive: %w", err)
		}

		scriptArgs = append(scriptArgs, "-x", extDst)
	}

	script, err := coreScripts.Open(packEmbedPath)
	if err != nil {
		return fmt.Errorf("failed to open pack script: %w", err)
	}

	cmdArgs := make([]string, 0, commandArgsCapacity+len(scriptArgs))

	cmdArgs = append(cmdArgs, "-s", "--")

	cmd := exec.CommandContext(context.Background(), "bash", append(cmdArgs, scriptArgs...)...)

	cmd.Stdin = script
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("pack script execution failed: %w", err)
	}

	return nil
}

// Unpack unpacks a tar.gz archive.
func Unpack(archivePath string) error {
	err := extractTarGz(archivePath, ".")
	if err != nil {
		return fmt.Errorf("failed to unpack: %w", err)
	}

	return nil
}

// Inspect allows user to inspect coredump.
func Inspect(streams output.Streams, archiveOrDir, sourceDir string) error {
	stat, err := os.Stat(archiveOrDir)
	if err != nil {
		return fmt.Errorf("failed to inspect: %w", err)
	}

	var dir string

	if stat.IsDir() {
		dir = archiveOrDir
	} else {
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
		dir = strings.Split(filepath.Base(archiveOrDir), ".")[0]
		// Compose full path to unpacked directory.
		dir = filepath.Join(tmpDir, dir)
	}

	// First, try to find gdb wrapper within the unpacked directory.
	scriptPath := filepath.Join(dir, filepath.Base(inspectEmbedPath))

	_, err = os.Stat(scriptPath)

	if errors.Is(err, fs.ErrNotExist) {
		// If the wrapper is missing in archive, then use the embedded one.
		err = copyEmbedded(coreScripts, inspectEmbedPath, scriptPath, scriptFileMode)
	}

	if err != nil {
		return fmt.Errorf("failed to find inspect script: %w", err)
	}

	scriptArgs := []string{}
	if len(sourceDir) > 0 {
		scriptArgs = append(scriptArgs, "-s", sourceDir)
	}

	// GDB-wrapper use standard input, so we need to launch it directly
	// rather than pass it over standard input to bash -s.
	cmd := exec.CommandContext(context.Background(), scriptPath, scriptArgs...)

	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("inspect script execution failed: %w", err)
	}

	return nil
}
