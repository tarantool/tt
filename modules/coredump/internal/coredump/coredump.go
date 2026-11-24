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
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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
	// Executable is the Tarantool that dumped it, as -e names it, or as the
	// process of the core of a pipe runs it: the core is refused when this
	// one did not dump it. Empty is the one pack finds for the core (see
	// findTarantool): the executable the core names, or else the first that
	// dumped it of the tarantool of PATH and of Tarantools.
	Executable string
	// Tarantools returns the tarantools tt knows of, which pack tries for a
	// core of no Executable after the executable the core names and the
	// tarantool of PATH, and calls it for only when neither dumped the core;
	// nil is none.
	Tarantools func() []Candidate
	// Dir is the directory the archive is created in; empty is the working
	// directory.
	Dir string
	// PID is the PID of the dumped process, which names the archive; 0 is
	// that of the process of the core, or none when it has none.
	PID uint
	// Time is the time of the dump in seconds since the Epoch; empty is
	// now.
	Time string
	// Logs are the logs of the instance of the core, whose tails, the last
	// LogTailBytes of each, go into logs/ of the archive; none for a core
	// of no instance.
	Logs []PackLog
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

// archiveReport is the file of the temporary directory of Pack the pack
// script writes the path of the archive into (-r).
const archiveReport = "archive"

// Pack packs a core dump into a tar.gz archive and returns its path. It logs
// to log the tarantool it found for a core of no Executable, and what of the
// core will not work in gdb, while another core may still be taken. The
// archive is named by the PID of opts, or else by that of the process of the
// core, which the pack script takes with the core of a file, -c, for the
// name alone. The tails of the Logs of opts go into logs/ of the archive,
// and Pack logs what of each. An archive of that name there already, as of
// the same core packed in the same minute, is kept, and its path is
// returned second: the archive is named with -2 before .tar.gz, or the next
// number that is free. The second is empty when the name was free. A ctx
// that ends, as a signal to tt ends it, ends the pack script with SIGTERM,
// which removes what it made, and Pack removes its temporary directory.
func Pack(ctx context.Context, streams output.Streams, log *slog.Logger, opts PackOptions) (
	string, string, error,
) {
	artefacts, err := probeArtefacts(streams.Err, opts, defaultDebugDirs)
	if err != nil {
		return "", "", err
	}

	logTarantoolChoice(log, artefacts.choice)
	warnPackVersion(log, artefacts)
	warnPackDebugInfo(log, artefacts, defaultDebugDirs)
	warnThreadStacks(log, artefacts.stacks)

	opts.Executable = artefacts.executable

	if opts.PID == 0 {
		opts.PID = artefacts.pid
	}

	scriptArgs, err := packArgs(opts)
	if err != nil {
		return "", "", err
	}

	tmpDir, err := os.MkdirTemp(os.TempDir(), "tt-coredump-*")
	if err != nil {
		return "", "", fmt.Errorf("cannot create a temporary directory for archiving: %w", err)
	}

	defer func() {
		_ = os.RemoveAll(tmpDir)
	}() // Clean up on function return.

	// The version goes first, before any file of the host could take its
	// name: the script packs a name once.
	versionArgs, err := stageVersion(tmpDir, artefacts)
	if err != nil {
		return "", "", err
	}

	scriptArgs = append(scriptArgs, versionArgs...)
	scriptArgs = append(scriptArgs, artefacts.scriptArgs()...)

	// Prepare gdb wrapper for packing.
	inspectPath := filepath.Join(tmpDir, filepath.Base(inspectEmbedPath))

	err = copyEmbedded(coreScripts, inspectEmbedPath, inspectPath, scriptFileMode)
	if err != nil {
		return "", "", fmt.Errorf("failed to put the inspecting script into the archive: %w", err)
	}

	scriptArgs = append(scriptArgs, "-g", inspectPath)

	// Prepare gdb extensions for packing.
	exts, err := writeExtensions(tmpDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to put GDB-extension into the archive: %w", err)
	}

	for _, ext := range exts {
		scriptArgs = append(scriptArgs, "-x", ext)
	}

	// The tails of the logs of the instance go into logs/ of the archive,
	// as files of their own in the temporary directory.
	logArgs, err := stageLogs(log, tmpDir, opts.Logs)
	if err != nil {
		return "", "", err
	}

	scriptArgs = append(scriptArgs, logArgs...)

	// The script names the archive and tells its path to a terminal alone:
	// it writes the path into the report for tt to tell it, and that of the
	// archive of the name it did not take.
	report := filepath.Join(tmpDir, archiveReport)

	err = runPackScript(ctx, streams, tmpDir, append(scriptArgs, "-r", report))
	if err != nil {
		return "", "", err
	}

	paths, err := os.ReadFile(report)
	if err != nil {
		return "", "", fmt.Errorf("cannot read the path of the archive the pack script made: %w",
			err)
	}

	archive, taken, _ := strings.Cut(strings.TrimSuffix(string(paths), "\n"), "\n")

	return archive, taken, nil
}

// runPackScript writes the pack script into dir and runs it with args. bash
// runs the file, and the standard input of the script is none: the script
// takes it for the core of a kernel.core_pattern pipe when it is given no
// core, and with the script fed to `bash -s` it read the rest of its own
// text for the core. A ctx that ends sends the script SIGTERM, on which it
// stops tar and removes what it made, and the script is waited for: a kill
// left the temporary directory of the script and the archive begun. bash and
// the programs of the script are looked up in a PATH of the system when
// that of tt is empty, as the kernel runs the pipe (see command).
func runPackScript(ctx context.Context, streams output.Streams, dir string, args []string,
) error {
	script := filepath.Join(dir, filepath.Base(packEmbedPath))

	err := copyEmbedded(coreScripts, packEmbedPath, script, scriptFileMode)
	if err != nil {
		return fmt.Errorf("failed to write the pack script: %w", err)
	}

	cmd := command(ctx, "bash", append([]string{script}, args...)...)

	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }

	err = cmd.Run()

	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("the pack script is stopped: %w", context.Cause(ctx))
	case err != nil:
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
