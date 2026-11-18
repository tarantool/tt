package pack

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// The values of Options.DebugInfo: what the pack does with the debug
// information of the native files the builds of the package made.
const (
	// DebugInfoKeep packs them as the builds made them; the default.
	DebugInfoKeep = "keep"
	// DebugInfoStrip packs them without their debug information.
	DebugInfoStrip = "strip"
)

// debugInfoModes are the values of Options.DebugInfo, in the order the
// error of another one lists them.
var debugInfoModes = []string{DebugInfoKeep, DebugInfoStrip}

var (
	// errDebugInfo reports a DebugInfo of no mode of debugInfoModes.
	errDebugInfo = errors.New("unknown --debuginfo")
	// errNoStrip reports a pack that strips with no tool to strip with.
	errNoStrip = errors.New("--debuginfo strip needs strip")
)

// stripsDebugInfo reports whether the pack strips the debug information.
func (opts Options) stripsDebugInfo() bool {
	return opts.DebugInfo == DebugInfoStrip
}

// checkDebugInfo checks the DebugInfo of opts, and that a pack that strips
// has the tool to strip with (stripCommandOf), before anything is built.
func checkDebugInfo(opts Options) error {
	if opts.DebugInfo != "" && !slices.Contains(debugInfoModes, opts.DebugInfo) {
		return stateErrorf("%w %q: want %s", errDebugInfo, opts.DebugInfo,
			strings.Join(debugInfoModes, " or "))
	}

	if !opts.stripsDebugInfo() {
		return nil
	}

	_, err := stripCommandOf(opts.Build.Target, exec.LookPath)

	return err
}

// stripper returns the argv that writes the native file in as out without
// its debug information.
type stripper func(in, out string) []string

// stripDebug is the option of the strip of binutils, of objcopy and of
// llvm-strip that drops the debug information and keeps the symbols.
const stripDebug = "--strip-debug"

// stripCommand returns the stripper of the native files of goos: strip -S
// on darwin, strip --strip-debug elsewhere, else objcopy --strip-debug of
// binutils, each found in PATH with lookPath. The symbols are kept: a
// backtrace names the functions all the same.
func stripCommand(goos string, lookPath func(string) (string, error)) (stripper, error) {
	path, err := lookPath("strip")
	macOS := goos == "darwin"

	switch {
	case macOS && err != nil:
		return nil, stateErrorf("%w -S of Xcode in PATH: %w", errNoStrip, err)
	case macOS:
		return func(in, out string) []string { return []string{path, "-S", "-o", out, in} }, nil
	case err == nil:
		return func(in, out string) []string {
			return []string{path, stripDebug, "-o", out, in}
		}, nil
	}

	objcopy, objcopyErr := lookPath("objcopy")
	if objcopyErr == nil {
		return func(in, out string) []string {
			return []string{objcopy, stripDebug, in, out}
		}, nil
	}

	return nil, stateErrorf("%w --strip-debug of binutils, or objcopy, in PATH: %w",
		errNoStrip, err)
}

// crossStrip is the tool a pack of a build for another platform than the
// machine strips with: llvm-strip strips the ELF and Mach-O files of every
// platform, where the strip of binutils strips those of the machine alone.
const crossStrip = "llvm-strip"

// stripCommandOf returns the stripper of the native files a build for target
// made (build.Result.Target): that of the machine (stripCommand) for the
// machine, and llvm-strip --strip-debug, found in PATH with lookPath, for
// another platform.
func stripCommandOf(target native.Platform, lookPath func(string) (string, error)) (
	stripper, error,
) {
	if target == native.Host() || target == (native.Platform{OS: "", Arch: ""}) {
		return stripCommand(runtime.GOOS, lookPath)
	}

	path, err := lookPath(crossStrip)
	if err != nil {
		return nil, stateErrorf("%w: a pack for %s, another platform than the machine, "+
			"strips with %s, which strips the files of every platform, and it is not in "+
			"PATH: %w", errNoStrip, target, crossStrip, err)
	}

	return func(in, out string) []string {
		return []string{path, stripDebug, "-o", out, in}
	}, nil
}

// stripDebugInfo writes each of files, native files a build for target made,
// without its debug information, in place: the tool (stripCommandOf) writes
// a new file beside it, which replaces it, so that a file of the stage that
// shares its data with one of the project - a hard link - leaves that one as
// it is.
func stripDebugInfo(ctx context.Context, files []string, target native.Platform) error {
	if len(files) == 0 {
		return nil
	}

	command, err := stripCommandOf(target, exec.LookPath)
	if err != nil {
		return err
	}

	for _, file := range files {
		err = stripFile(ctx, command, file)
		if err != nil {
			return err
		}
	}

	return nil
}

// stripFile writes file without its debug information with command
// (stripCommand), through a temporary file of its directory, its mode kept.
func stripFile(ctx context.Context, command stripper, file string) error {
	info, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("stripping %s: %w", file, err)
	}

	tmp := file + ".strip"

	argv := command(file, tmp)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)

		return stateErrorf("stripping %s: %s: %w\n%s", file, strings.Join(argv, " "), err,
			strings.TrimSpace(string(output)))
	}

	err = os.Chmod(tmp, info.Mode().Perm())
	if err == nil {
		err = os.Rename(tmp, file)
	}

	if err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("stripping %s: %w", file, err)
	}

	return nil
}

// ownNativeFiles are the native files of the stage of a .tt archive that the
// builds of the package made: the modules and the programs of built, each at
// its path under .rocks/ of stageDir. The bundled runtime, the rocks of the
// dependencies and the files of [package].include are left as they are.
func ownNativeFiles(stageDir string, built *build.Result) ([]string, error) {
	var files []string

	for _, name := range slices.Sorted(maps.Keys(built.Built)) {
		made := built.Built[name]

		for _, path := range slices.Concat(made.Modules, made.Programs) {
			rel, err := filepath.Rel(built.Tree, path)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}

			staged := filepath.Join(stageDir, rocksDirName, rel)

			platforms, err := native.FilePlatforms(staged)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			if err != nil {
				return nil, fmt.Errorf("%s: %w", staged, err)
			}

			if platforms != nil && !slices.Contains(files, staged) {
				files = append(files, staged)
			}
		}
	}

	return files, nil
}

// strippedStage copies stage, the stage of the product the rock of the
// package is packed from, into a directory under temp, and strips the debug
// information of its native files there, all of them made by the builds of
// the package: the stage is that of the build, which the package in .rocks
// was installed from, and stays as it is. target is the platform the build
// was for (stripCommandOf). It returns the copy, with the cleanup that
// removes it.
func strippedStage(ctx context.Context, temp, stage string, target native.Platform) (
	string, func(), error,
) {
	dir, err := os.MkdirTemp(temp, "stripped-")
	if err != nil {
		return "", nil, fmt.Errorf("creating a directory under %s: %w", temp, err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }
	copied := filepath.Join(dir, "stage")

	err = copyTree(stage, copied)
	if err == nil {
		err = stripStage(ctx, copied, target)
	}

	if err != nil {
		cleanup()

		return "", nil, err
	}

	return copied, cleanup, nil
}

// stripStage strips the debug information of the native files of stage,
// which a build for target made.
func stripStage(ctx context.Context, stage string, target native.Platform) error {
	natives, err := native.Files(stage)
	if err != nil {
		return err
	}

	files := make([]string, 0, len(natives))
	for _, file := range natives {
		files = append(files, filepath.Join(stage, filepath.FromSlash(file.Name)))
	}

	return stripDebugInfo(ctx, files, target)
}
