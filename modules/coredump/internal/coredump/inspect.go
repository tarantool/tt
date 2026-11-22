package coredump

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/output"
)

// Inspect opens the core dump of archiveOrDir, an archive tt coredump pack
// made or the directory it unpacks into, in gdb, with the sources in
// sourceDir when it is not empty. It logs to log where the gdb extensions
// come from and what may not work.
//
// gdb runs through the gdb.sh tt embeds, never the one of the archive: an
// archive packed by an older tt carries the wrapper of that tt. The wrapper
// sources the gdb extensions tt selects explicitly once the core is loaded:
// those that match the Tarantool of the core (see resolver.resolve).
func Inspect(ctx context.Context, streams output.Streams, log *slog.Logger,
	archiveOrDir, sourceDir string,
) error {
	return inspect(ctx, streams, newResolver(log), archiveOrDir, sourceDir)
}

// inspect is Inspect with the extensions found by res.
func inspect(ctx context.Context, streams output.Streams, res *resolver,
	archiveOrDir, sourceDir string,
) error {
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

	exts, err := selectExtensions(ctx, res, dir, sourceDir)
	if err != nil {
		return err
	}

	paths := make([]string, 0, len(exts))

	for _, ext := range exts {
		path := filepath.Join(work, ext.Name)

		err = os.WriteFile(path, ext.Data, extensionFileMode)
		if err != nil {
			return fmt.Errorf("failed to write the GDB-extension: %w", err)
		}

		paths = append(paths, path)
	}

	args, err := inspectArgs(script, dir, sourceDir, paths)
	if err != nil {
		return err
	}

	warnDebugInfo(res.log, dir)

	// The wrapper is run by sh, so that a temporary directory mounted
	// noexec runs it too; gdb reads the standard input of tt.
	cmd := exec.CommandContext(ctx, "sh", args...)

	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err

	// Ctrl-C at the prompt of gdb interrupts the command gdb runs: the
	// terminal sends SIGINT to tt as well, which must outlive gdb rather
	// than die of it and leave gdb reading a terminal the shell has taken
	// back. A signal caught rather than ignored is reset for the child.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)

	defer signal.Stop(interrupts)

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("inspect script execution failed: %w", err)
	}

	return nil
}

// warnDebugInfo warns, before gdb starts, when gdb will find no debugging
// information for the tarantool of the artefacts in dir: the extensions
// look the types of LuaJIT and Tarantool up and fail without them.
func warnDebugInfo(log *slog.Logger, dir string) {
	err := checkDebugInfo(dir)

	switch {
	case errors.Is(err, errNoDebugInfo):
		log.Warn(fmt.Sprintf("the tarantool of the core has %s: the lj-* commands of LuaJIT "+
			"and the pretty-printers of Tarantool will not work, plain backtraces will; a core "+
			"packed where the debug file of its tarantool is installed has it", err))
	case err != nil:
		log.Debug("cannot tell whether the tarantool has debugging information: " + err.Error())
	}
}

// selectExtensions returns the gdb extensions for the core of the
// artefacts in dir (resolver.resolve). Artefacts without a version are
// none of an archive of pack, and gdb.sh refuses them: the copies of tt are
// returned for them.
func selectExtensions(ctx context.Context, res *resolver, dir, sourceDir string,
) ([]extension, error) {
	version, err := readVersion(filepath.Join(dir, "version"))
	if errors.Is(err, fs.ErrNotExist) {
		return embeddedExtensions()
	}

	if err != nil {
		res.log.Warn("cannot read the version of the core: " + err.Error())
	}

	return res.resolve(ctx, sourceDir, version)
}

// embeddedExtensions returns the copies of the gdb extensions tt embeds,
// in the order of their names.
func embeddedExtensions() ([]extension, error) {
	entries, err := extensions.ReadDir(extensionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find embedded GDB-extensions: %w", err)
	}

	exts := make([]extension, 0, len(entries))

	for _, entry := range entries {
		data, err := extensions.ReadFile(extensionsDir + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read embedded GDB-extension: %w", err)
		}

		exts = append(exts, extension{Name: entry.Name(), Data: data})
	}

	return exts, nil
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
