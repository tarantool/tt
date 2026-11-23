// Package coredump packs a core dump of Tarantool with what gdb needs to
// read it into a tar.gz archive, unpacks such an archive, and opens it in
// gdb. The packing is tarabrt.sh, the opening gdb.sh: tt runs the scripts it
// embeds, and the archive carries gdb.sh and the gdb extensions for a user
// without tt.
package coredump

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/tarantool/tt/sdk/output"
)

//go:embed scripts/*
var coreScripts embed.FS

// extensions are the sets of gdb extensions tt embeds, each with its
// manifest and vendor.json: the copies of the upstream extensions, which
// inspect loads for a core it finds none of and pack puts into the
// archive, in extensionsDir, and the universal extensions of
// tarantool-gdb-tools, which inspect loads on request instead of those of
// the Tarantool of the core, with the license files of the repository, in
// gdbToolsDir under it. The whole directory is embedded rather than the
// files of each set by a pattern: a file put into it that no manifest
// names fails a test instead of being left out of tt unseen.
//
//go:embed extensions
var extensions embed.FS

const (
	packEmbedPath     = "scripts/tarabrt.sh" // spell-checker:disable-line
	inspectEmbedPath  = "scripts/gdb.sh"
	scriptFileMode    = 0o755
	extensionFileMode = 0o644
	// extensionsDir is the directory of the gdb extensions in extensions.
	extensionsDir = "extensions"
	// gdbToolsDir is the directory of the extensions of tarantool-gdb-tools
	// in extensions.
	gdbToolsDir = extensionsDir + "/gdb-tools"
)

// writeExtensions writes the gdb extensions tt embeds, those of their
// manifest, into dir and returns their paths, in their load order.
func writeExtensions(dir string) ([]string, error) {
	entries, err := embeddedManifest()
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(entries))

	for _, entry := range entries {
		path := filepath.Join(dir, entry.File)

		err = copyEmbedded(extensions, extensionsDir+"/"+entry.File, path, extensionFileMode)
		if err != nil {
			return nil, err
		}

		paths = append(paths, path)
	}

	return paths, nil
}

// PackOptions are what Pack packs and where.
type PackOptions struct {
	// Core is the core dump. Empty or StdinCore with a PID is the core on
	// the standard input, which ReceiveCore stores to a file for Pack.
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
	artefacts, err := probeArtefacts(streams.Err, opts, defaultDebugDirs)
	if err != nil {
		return err
	}

	opts.Executable = artefacts.executable

	scriptArgs, err := packArgs(opts)
	if err != nil {
		return err
	}

	scriptArgs = append(scriptArgs, artefacts.scriptArgs()...)

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
	exts, err := writeExtensions(tmpDir)
	if err != nil {
		return fmt.Errorf("failed to put GDB-extension into the archive: %w", err)
	}

	for _, ext := range exts {
		scriptArgs = append(scriptArgs, "-x", ext)
	}

	return runPackScript(streams, tmpDir, scriptArgs)
}

// runPackScript writes the pack script into dir and runs it with args. bash
// runs the file, and the standard input of the script is none: the script
// takes it for the core of a kernel.core_pattern pipe when it is given no
// core, and with the script fed to `bash -s` it read the rest of its own
// text for the core.
func runPackScript(streams output.Streams, dir string, args []string) error {
	script := filepath.Join(dir, filepath.Base(packEmbedPath))

	err := copyEmbedded(coreScripts, packEmbedPath, script, scriptFileMode)
	if err != nil {
		return fmt.Errorf("failed to write the pack script: %w", err)
	}

	cmd := exec.CommandContext(context.Background(), "bash", append([]string{script}, args...)...)

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
