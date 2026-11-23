package coredump

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/output"
)

// Inspect opens the core dump of archiveOrDir, an archive tt coredump pack
// made or the directory it unpacks into, in gdb, with the sources in
// sourceDir when it is not empty. It logs to log where the gdb extensions
// come from and what may not work.
//
// gdb runs through the gdb.sh tt embeds (see gdbDebugger), which sources
// the gdb extensions tt selects explicitly once the core is loaded: those
// that match the Tarantool of the core (see resolver.resolve).
func Inspect(ctx context.Context, streams output.Streams, log *slog.Logger,
	archiveOrDir, sourceDir string,
) error {
	return inspect(ctx, streams, newResolver(log), archiveOrDir, sourceDir)
}

// inspect is Inspect with the extensions found by res, in gdb.
func inspect(ctx context.Context, streams output.Streams, res *resolver,
	archiveOrDir, sourceDir string,
) error {
	return inspectWith(ctx, streams, res, gdbDebugger{}, archiveOrDir, sourceDir)
}

// inspectWith is inspect with the debugger backend: it is handed the
// extensions that load in it, in their load order (see forDebugger).
func inspectWith(ctx context.Context, streams output.Streams, res *resolver, backend debugger,
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

	// The files of the debugger and the extensions tt loads live in a
	// directory of their own: the one of the artefacts may be the user's.
	work, err := os.MkdirTemp(os.TempDir(), "tt-coredump-*")
	if err != nil {
		return fmt.Errorf("cannot create a temporary directory for inspecting: %w", err)
	}

	defer func() {
		_ = os.RemoveAll(work)
	}()

	exts, err := selectExtensions(ctx, res, dir, sourceDir)
	if err != nil {
		return err
	}

	exts, skipped, err := forDebugger(exts, backend.Name())
	if err != nil {
		return err
	}

	warnSkipped(res.log, backend.Name(), skipped)

	paths := make([]string, 0, len(exts))

	for _, ext := range exts {
		path := filepath.Join(work, ext.Name)

		err = os.WriteFile(path, ext.Data, extensionFileMode)
		if err != nil {
			return fmt.Errorf("failed to write the GDB-extension: %w", err)
		}

		paths = append(paths, path)
	}

	cmd, err := backend.Command(ctx, work, dir, sourceDir, paths)
	if err != nil {
		return err
	}

	warnDebugInfo(res.log, dir)

	return runDebugger(cmd, streams)
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
// those of their manifest, in their load order.
func embeddedExtensions() ([]extension, error) {
	return embeddedSet.all()
}

// skippedExtension is an extension forDebugger leaves out although it
// loads in the debugger: it needs one its After names, which is not loaded
// there.
type skippedExtension struct {
	// Name is the name of the extension.
	Name string
	// Missing is the name of the extension it needs that is not loaded.
	Missing string
}

// forDebugger returns the extensions of exts that load in the debugger
// name, in their load order: each after those of them its After names, and
// otherwise in the order of exts. An extension needs those its After
// names, as lj-bt needs the extension of LuaJIT: one whose such is not
// loaded, since it does not load in the debugger or is none of exts, is
// left out too, and returned among the skipped.
func forDebugger(exts []extension, name string) ([]extension, []skippedExtension, error) {
	selected := make([]extension, 0, len(exts))

	for _, ext := range exts {
		if slices.Contains(ext.Debuggers, name) {
			selected = append(selected, ext)
		}
	}

	var skipped []skippedExtension

	// Leaving one out may leave out those that need it: the extensions are
	// looked through again until none is left out.
	for {
		loaded := make(map[string]bool, len(selected))

		for _, ext := range selected {
			loaded[ext.Name] = true
		}

		kept := make([]extension, 0, len(selected))

		for _, ext := range selected {
			missing := slices.IndexFunc(ext.After, func(after string) bool {
				return !loaded[after]
			})
			if missing >= 0 {
				skipped = append(skipped,
					skippedExtension{Name: ext.Name, Missing: ext.After[missing]})

				continue
			}

			kept = append(kept, ext)
		}

		if len(kept) == len(selected) {
			break
		}

		selected = kept
	}

	ordered, err := loadOrder(selected,
		func(ext extension) string { return ext.Name },
		func(ext extension) []string { return ext.After })
	if err != nil {
		return nil, nil, fmt.Errorf("cannot order the extensions for %s: %w", name, err)
	}

	return ordered, skipped, nil
}

// warnSkipped warns of the extensions forDebugger left out for the
// debugger name.
func warnSkipped(log *slog.Logger, name string, skipped []skippedExtension) {
	for _, skip := range skipped {
		log.Warn(fmt.Sprintf("the extension %s is not loaded in %s: it needs %s, which is not "+
			"loaded there", skip.Name, name, skip.Missing))
	}
}
