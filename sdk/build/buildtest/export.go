package buildtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tarantool/tt/sdk/build"
)

// This file checks the export of a build ([build.Exporter]) against its Run:
// a backend whose builds are made without tt runs both on two copies of one
// project in its tests (CheckExport, CheckExportHook), so that a script that
// makes something else than Run fails them.

// errNoExporter reports a build of a backend that is no build.Exporter.
var errNoExporter = errors.New("the build is no build.Exporter")

// Export returns the export of bld, a build of the backend of the harness,
// as tt package pack asks for it ([build.Exporter]): bld is checked
// ([build.Backend.ValidateBuild]) first, and the [build.ExportContext] is
// that of the harness - its component, whether it is of a hook, the
// versions of Locked, and its project and working directory. A build that
// is no Exporter is an error.
func (h *Harness) Export(bld build.Build) (build.Export, error) {
	err := h.backend.ValidateBuild(bld)
	if err != nil {
		return build.Export{}, err
	}

	exporter, ok := bld.(build.Exporter)
	if !ok {
		return build.Export{}, fmt.Errorf("buildtest: %w: backend %q", errNoExporter,
			h.backend.Name)
	}

	return exporter.Export(&build.ExportContext{
		Component:     h.Context.Component,
		Hook:          h.Context.Hook,
		LockedVersion: h.Context.LockedVersion,
		Project:       h.Context.Dirs.Project,
		Cwd:           h.Context.Dirs.Cwd,
	})
}

// exportScriptName is the file of the script a harness runs an export with,
// in its temporary directory, out of the project.
const exportScriptName = "tt-export.sh"

// RunExport runs the script of export, of a build of the backend of the
// harness, as the script tt package pack writes runs it: with set -eu, by
// sh in the PATH of the build, in [build.Dirs.Cwd], with the environment of
// a command of the build ([build.Context.Command]) and the variables of an
// export besides (see [build.Export]): TT_SHARE_DIR, TT_PROGRAMS_DIR and
// TT_LINK_DIR of a component, TT_STATE_DIR, TT_LINK_ROOT, TT_MODULE_EXT and
// TT_MODULE_LDFLAGS of the target, and CC, CFLAGS and LIBFLAG as LuaRocks
// sets them, but those the environment of the test sets. A tool of the
// export that is not in the PATH of the build fails it, wrapping
// [build.ErrNoTool]. The files output lists are not copied, as Run copies
// none.
func (h *Harness) RunExport(ctx context.Context, export build.Export) error {
	facts := h.Context

	for _, tool := range export.Tools {
		_, err := facts.LookPath(tool)
		if err != nil {
			return fmt.Errorf("buildtest: %w: the export runs %s: %w", build.ErrNoTool, tool, err)
		}
	}

	script := filepath.Join(filepath.Dir(facts.Dirs.Project), exportScriptName)

	err := os.WriteFile(script, []byte("set -eu\n"+export.Script+"\n"), filePerm)
	if err != nil {
		return fmt.Errorf("buildtest: %w", err)
	}

	cmd := facts.Command(ctx, "sh", script)

	cmd.Env = append(cmd.Env, h.exportEnv(cmd.Env)...)

	return facts.Run(cmd)
}

// filePerm is the mode of the script of an export.
const filePerm = 0o600

// exportEnv returns the variables of an export ([build.Export]) the
// environment environ of a command of the build lacks, as key=value.
func (h *Harness) exportEnv(environ []string) []string {
	facts := h.Context
	dirs := facts.Dirs
	root := filepath.Dir(dirs.Project)

	vars := map[string]string{
		"TT_STATE_DIR":  dirs.State,
		"TT_LINK_ROOT":  filepath.Join(root, "link"),
		"TT_MODULE_EXT": facts.Target.ModuleExt(),
	}

	if dirs.Link != "" {
		vars["TT_LINK_ROOT"] = filepath.Dir(dirs.Link)
	}

	for key, dir := range map[string]string{
		"TT_SHARE_DIR": dirs.Share, "TT_PROGRAMS_DIR": dirs.Programs, "TT_LINK_DIR": dirs.Link,
	} {
		if dir != "" {
			vars[key] = dir
		}
	}

	moduleLDFlags, libFlag := "-Wl,-Bsymbolic-functions", "-shared"
	if facts.Target.OS == "darwin" {
		moduleLDFlags, libFlag = "", "-bundle -undefined dynamic_lookup -all_load"
	}

	vars["TT_MODULE_LDFLAGS"] = moduleLDFlags

	// The toolchain is that of LuaRocks, but what the test sets.
	for key, value := range map[string]string{
		"CC": "cc", "CFLAGS": "-O2 -fPIC", "LIBFLAG": libFlag,
	} {
		if _, set := build.LookupEnv(environ, key); !set {
			vars[key] = value
		}
	}

	env := make([]string, 0, len(vars))
	for _, key := range slices.Sorted(maps.Keys(vars)) {
		env = append(env, key+"="+vars[key])
	}

	return env
}

// CheckExport checks the export of the build backend makes of block for
// component against its Run: block is checked as Validate checks it, then
// on two harnesses of component (New), each given to setup first - which
// writes the files of the project, and tells the Tarantool of the build or
// the Locked versions, the same on both - the build runs as the core runs
// it on one, Run, and its script on the other (RunExport), and for a
// backend of [build.OutputCwd] the files output lists are copied after
// either, as the core copies them. Then what each made in the directories
// of the results - [build.Dirs.Modules], [build.Dirs.Share],
// [build.Dirs.Programs] and [build.Dirs.Link] - is compared, by path, by
// whether it is executable, and by its bytes but for a native file, which
// two compiles make of other bytes: a difference fails tb. A build that
// does not check, that is no [build.Exporter], or that fails to run or to
// export fails tb at once, and a tool of the export that is not in the PATH
// of the build skips it. It returns the export, for the test to check its
// Tools and Lost.
func CheckExport(
	tb testing.TB, backend build.Backend, component build.Component, block Block,
	setup func(h *Harness),
) build.Export {
	tb.Helper()

	bld, err := Validate(backend, component, block)
	if err != nil {
		tb.Fatalf("buildtest: %v", err)
	}

	run, script := New(tb, backend, component), New(tb, backend, component)

	return checkExport(tb, bld, block, setup, run, script, resultDirs)
}

// CheckExportHook checks the export of the build backend makes of block, the
// block of a lifecycle hook, against its Run, as CheckExport does, on two
// harnesses of a hook (NewHook): block is checked as ValidateHook checks it,
// and what is compared are the files of the project, what a hook makes.
func CheckExportHook(
	tb testing.TB, backend build.Backend, block Block, setup func(h *Harness),
) build.Export {
	tb.Helper()

	bld, err := ValidateHook(backend, block)
	if err != nil {
		tb.Fatalf("buildtest: %v", err)
	}

	run, script := NewHook(tb, backend), NewHook(tb, backend)

	return checkExport(tb, bld, block, setup, run, script, projectDir)
}

// checkExport is CheckExport and CheckExportHook on the harnesses run and
// script: the directories dirs returns of each are compared.
func checkExport(
	tb testing.TB, bld build.Build, block Block, setup func(h *Harness), run, script *Harness,
	dirs func(build.Dirs) map[string]string,
) build.Export {
	tb.Helper()

	if setup != nil {
		setup(run)
		setup(script)
	}

	export, err := script.Export(bld)
	if err != nil {
		tb.Fatalf("buildtest: export: %v", err)
	}

	for _, tool := range export.Tools {
		_, err := script.Context.LookPath(tool)
		if err != nil {
			tb.Skipf("buildtest: the export runs %s, which is not in PATH", tool)
		}
	}

	err = run.Run(tb.Context(), bld)
	if err == nil {
		err = run.copyOutputs(block)
	}

	if err != nil {
		tb.Fatalf("buildtest: run: %v\n%s", err, run.Printed())
	}

	err = script.RunExport(tb.Context(), export)
	if err == nil {
		err = script.copyOutputs(block)
	}

	if err != nil {
		tb.Fatalf("buildtest: the script of the export: %v\n%s\nscript:\n%s", err,
			script.Printed(), export.Script)
	}

	runDirs, scriptDirs := dirs(run.Context.Dirs), dirs(script.Context.Dirs)

	for _, name := range slices.Sorted(maps.Keys(runDirs)) {
		diff, err := compareTrees(runDirs[name], scriptDirs[name])
		if err != nil {
			tb.Fatalf("buildtest: %v", err)
		}

		if diff != "" {
			tb.Errorf("buildtest: the script of the export makes another %s than Run:\n%s"+
				"script:\n%s", name, diff, export.Script)
		}
	}

	return export
}

// resultDirs are the directories of the results of the build of a
// component, by the name of what they hold.
func resultDirs(dirs build.Dirs) map[string]string {
	return map[string]string{
		"modules": dirs.Modules, "data": dirs.Share, "programs": dirs.Programs, "link": dirs.Link,
	}
}

// projectDir is the directory of the project, what the build of a hook
// makes files in.
func projectDir(dirs build.Dirs) map[string]string {
	return map[string]string{"project": dirs.Project}
}

// copyOutputs copies the files the output of block lists, from the working
// directory of the build into the directories of their types, as the core
// copies them for a backend of build.OutputCwd; a backend of another
// OutputFrom, and a hook, have none copied.
func (h *Harness) copyOutputs(block Block) error {
	if h.backend.OutputFrom != build.OutputCwd || h.Context.Hook {
		return nil
	}

	dirs := h.Context.Dirs

	for _, file := range block.Common.Output {
		err := build.CopyFile(dirs.OutputPath(file.To, filepath.Base(file.Path)),
			filepath.Join(dirs.Cwd, file.Path))
		if err != nil {
			return fmt.Errorf("buildtest: copying the output %s: %w", file.Path, err)
		}
	}

	return nil
}

// treeFile is what is compared of a file of a tree (compareTrees).
type treeFile struct {
	executable, native bool
	data               []byte
}

// compareTrees returns the differences of the files under want and got, a
// line each, by path: a file of one alone, one executable in one alone, and
// one of other bytes but for a native one; empty when there are none.
func compareTrees(want, got string) (string, error) {
	wantFiles, err := readTree(want)
	if err != nil {
		return "", err
	}

	gotFiles, err := readTree(got)
	if err != nil {
		return "", err
	}

	paths := slices.Sorted(maps.Keys(wantFiles))
	for path := range gotFiles {
		if _, ok := wantFiles[path]; !ok {
			paths = append(paths, path)
		}
	}

	slices.Sort(paths)

	var diff strings.Builder

	for _, path := range paths {
		wantFile, inWant := wantFiles[path]
		gotFile, inGot := gotFiles[path]

		switch {
		case !inGot:
			fmt.Fprintf(&diff, "  %s: Run made it, the script did not\n", path)
		case !inWant:
			fmt.Fprintf(&diff, "  %s: the script made it, Run did not\n", path)
		case wantFile.executable != gotFile.executable:
			fmt.Fprintf(&diff, "  %s: executable %t by Run, %t by the script\n", path,
				wantFile.executable, gotFile.executable)
		case wantFile.native != gotFile.native:
			fmt.Fprintf(&diff, "  %s: native %t by Run, %t by the script\n", path,
				wantFile.native, gotFile.native)
		case !wantFile.native && !bytes.Equal(wantFile.data, gotFile.data):
			fmt.Fprintf(&diff, "  %s: %q by Run, %q by the script\n", path, wantFile.data,
				gotFile.data)
		}
	}

	return diff.String(), nil
}

// readTree reads the files under dir, by their slash-separated paths
// relative to it; none when dir is empty or not there.
func readTree(dir string) (map[string]treeFile, error) {
	files := map[string]treeFile{}

	if dir == "" {
		return files, nil
	}

	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}

	if err != nil {
		return nil, fmt.Errorf("buildtest: %w", err)
	}

	defer func() { _ = root.Close() }()

	tree := root.FS()

	err = fs.WalkDir(tree, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		data, err := fs.ReadFile(tree, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		files[path] = treeFile{
			executable: info.Mode().Perm()&0o111 != 0, native: isNative(data), data: data,
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("buildtest: reading %s: %w", dir, err)
	}

	return files, nil
}

// nativeMagics begin the files two compiles of the same sources make of
// other bytes: ELF, Mach-O of either byte order and width, a universal
// Mach-O, and a static library.
//
//nolint:gochecknoglobals // A constant table.
var nativeMagics = [][]byte{
	[]byte("\x7fELF"),
	{0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe},
	{0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe},
	{0xca, 0xfe, 0xba, 0xbe},
	[]byte("!<arch>\n"),
}

// isNative reports whether data is of a native file (nativeMagics).
func isNative(data []byte) bool {
	return slices.ContainsFunc(nativeMagics, func(magic []byte) bool {
		return bytes.HasPrefix(data, magic)
	})
}
