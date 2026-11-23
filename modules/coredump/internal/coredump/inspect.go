package coredump

import (
	"context"
	"debug/elf"
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

// InspectOptions are what Inspect opens and the extensions it loads.
type InspectOptions struct {
	// Path is an archive tt coredump pack made, or the directory it
	// unpacks into.
	Path string
	// SourceDir is the directory of the sources of the Tarantool of the
	// core, which gdb maps the paths of the build to; empty is sources/ of
	// the artefacts. Unless GDBTools is set, its extensions are loaded.
	SourceDir string
	// GDBTools loads the universal extensions of tarantool-gdb-tools tt
	// embeds (see gdbToolsSet) instead of those of the Tarantool of the
	// core: one version of each for the versions of Tarantool their
	// manifest names, and lj-bt and gcore-pages, which no release has.
	GDBTools bool
}

// Inspect opens the core dump of opts.Path in gdb. It logs to log where the
// gdb extensions come from and what may not work.
//
// gdb runs through the gdb.sh tt embeds (see gdbDebugger), which sources
// the gdb extensions tt selects explicitly once the core is loaded: those
// that match the Tarantool of the core (see resolver.resolve), or the
// universal ones of tarantool-gdb-tools with opts.GDBTools.
func Inspect(ctx context.Context, streams output.Streams, log *slog.Logger,
	opts InspectOptions,
) error {
	return inspect(ctx, streams, newResolver(log), opts)
}

// inspect is Inspect with the extensions found by res, in gdb.
func inspect(ctx context.Context, streams output.Streams, res *resolver, opts InspectOptions,
) error {
	return inspectWith(ctx, streams, res, gdbDebugger{}, opts)
}

// inspectWith is inspect with the debugger backend: it is handed the
// extensions that load in it, in their load order (see forDebugger).
func inspectWith(ctx context.Context, streams output.Streams, res *resolver, backend debugger,
	opts InspectOptions,
) error {
	stat, err := os.Stat(opts.Path)
	if err != nil {
		return fmt.Errorf("failed to inspect: %w", err)
	}

	// The debugger is probed first: a host without it fails here, before
	// the archive is unpacked and the extensions are chosen, which may wait
	// on GitHub.
	probe, err := probeDebugger(ctx, res.log, backend, opts.GDBTools)
	if err != nil {
		return err
	}

	// dir is the directory of the artefacts, and unpacked, of an archive,
	// the one tt coredump unpack puts them in, relative to the working
	// directory.
	dir, unpacked := opts.Path, ""

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

		err = extractTarGz(opts.Path, tmpDir)
		if err != nil {
			return fmt.Errorf("failed to unpack: %w", err)
		}

		unpacked, err = unpackedDir(tmpDir)
		if err != nil {
			return fmt.Errorf("failed to unpack: %w", err)
		}

		dir = filepath.Join(tmpDir, unpacked)
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

	exts, err := chooseExtensions(ctx, res, dir, opts)
	if err != nil {
		return err
	}

	exts, skipped, err := forDebugger(exts, backend.Name())
	if err != nil {
		return err
	}

	warnSkipped(res.log, backend.Name(), skipped)

	// The extensions of the Tarantool of the core are of that version;
	// the universal ones are checked on a range of them, which the core
	// may be out of.
	if opts.GDBTools {
		logGDBTools(res.log, backend.Name(), exts)
		warnUnchecked(res.log, readCoreTarget(res.log, dir), exts)
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

	cmd, err := backend.Command(ctx, work, dir, opts.SourceDir, paths)
	if err != nil {
		return err
	}

	warnDebugInfo(res.log, dir, opts.Path, unpacked)

	stacks := readThreadStacks(filepath.Join(dir, "coredump"))
	warnThreadStacks(res.log, stacks)

	if backend.Name() == debuggerGDB {
		warnThreadLocalLua(res.log, readThreadDBFacts(dir, probe.Version, stacks), opts.GDBTools)
	}

	return runDebugger(cmd, streams)
}

// chooseExtensions returns the extensions for the core of the artefacts in
// dir: with opts.GDBTools the universal ones of tarantool-gdb-tools, the
// same for every core, for which neither the sources nor GitHub are looked
// at, else those of the Tarantool of the core (see selectExtensions).
func chooseExtensions(ctx context.Context, res *resolver, dir string, opts InspectOptions,
) ([]extension, error) {
	if opts.GDBTools {
		return gdbToolsSet.all()
	}

	return selectExtensions(ctx, res, dir, opts.SourceDir)
}

// logGDBTools logs which of the universal extensions of tarantool-gdb-tools
// the debugger name loads and the commit tt has them of, as the resolver
// logs where each extension of the Tarantool of a core comes from.
func logGDBTools(log *slog.Logger, name string, exts []extension) {
	if len(exts) == 0 {
		return
	}

	log.Info(fmt.Sprintf("%s extensions of tarantool-gdb-tools %s, which tt embeds: %s", name,
		GDBToolsCommit(), strings.Join(extensionNames(exts), ", ")))
}

// extensionNames returns the names of exts, in order.
func extensionNames(exts []extension) []string {
	names := make([]string, 0, len(exts))

	for _, ext := range exts {
		names = append(names, ext.Name)
	}

	return names
}

// warnDebugInfo warns, before gdb starts, when gdb will find no debugging
// information for the tarantool of the artefacts in dir, unpacked from the
// archive at path into unpacked, relative to the working directory as tt
// coredump unpack puts them, or the directory path itself, unpacked empty:
// the extensions look the types of LuaJIT and Tarantool up and fail without
// them. The hint says where the information is (see debugInfoHint).
func warnDebugInfo(log *slog.Logger, dir, path, unpacked string) {
	err := checkDebugInfo(dir)

	switch {
	case errors.Is(err, errNoDebugInfo):
		warnWithHint(log, fmt.Sprintf("the tarantool of the core has %s: the lj-* commands of "+
			"LuaJIT and the pretty-printers of Tarantool will not work, plain backtraces will",
			err), debugInfoHint(dir, path, unpacked))
	case err != nil:
		log.Debug("cannot tell whether the tarantool has debugging information: " + err.Error())
	}
}

// debugInfoHint returns what warnDebugInfo hints at of the artefacts in dir,
// of path, unpacked into unpacked. The tarantool of the static deb has the
// debugging information of a static build of Community (see
// staticDebRelease): gdb.sh finds it as the tarantool of the artefacts, or
// as the debug file of its build ID under usr/lib/debug of the artefacts,
// the one the warning names, and inspect is run again on them. symbol-file
// at the prompt of gdb would not do: the extensions are loaded by then, and
// fail without the types. An archive is unpacked first, where tt coredump
// unpack puts it, into the directory it holds, as pack named it, whatever
// the archive is named now (see unpackedDir); inspect unpacks it into a
// directory it removes. Of another tarantool the debug file a host the core
// is packed on may have is all there is.
func debugInfoHint(dir, path, unpacked string) string {
	generic := "pack the core again where the debug file of its tarantool is installed, " +
		"under " + hostDebugDir

	line, err := readVersion(filepath.Join(dir, "version"))
	if err != nil {
		return generic
	}

	file, err := elf.Open(filepath.Join(dir, "tarantool"))
	if err != nil {
		return generic
	}

	defer func() { _ = file.Close() }()

	build := buildID(file)

	release, ok := staticDebRelease(line, file.Type, build)
	if !ok {
		return generic
	}

	target, unpack := path, ""

	if unpacked != "" {
		target = unpacked
		unpack = "unpack the archive with `tt coredump unpack " + path + "`, "
	}

	return staticDebHint(release, build, fmt.Sprintf("%scopy it over %s, or to the debug file "+
		"named above under %s, and run `tt coredump inspect %s`", unpack,
		filepath.Join(target, "tarantool"), target, target))
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
