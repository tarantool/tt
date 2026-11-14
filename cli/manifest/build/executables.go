package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// An executable component builds programs into the bin directory of the
// package's stage, its TT_OUTPUT_DIR, which the package's rock deploys to
// <tree>/bin/ as they are. That directory of the tree is shared: the rocks of
// the dependency closure install their console scripts there, luatest's among
// them. The build records which programs each component built, by the
// component, so that a build of one component knows the programs of the
// others.
//
// The record lives in _build/, the build's own state, rather than in the
// tree: it never travels into an archive and is never installed.
const (
	// buildDirName is the project directory of the build's own state.
	buildDirName = "_build"
	// executablesFileName is the record of the programs in bin/ the
	// executable components built, under buildDirName.
	executablesFileName = "executables.json"
	// executablesFilePerm is the mode the record is written with.
	executablesFilePerm os.FileMode = 0o644
)

var (
	// errNotProgram reports a file an executable component put into bin/ that
	// is not a program of the build platform. It carries exit code 2.
	errNotProgram = errors.New("not a program of the build platform")
	// errNotStatic reports a program of a build asking for static = true that
	// needs a loader or a shared library. It carries exit code 2.
	errNotStatic = errors.New("not a static program")
	// errNoProgram reports an executable component whose build put no
	// program into bin/. It carries exit code 2.
	errNoProgram = errors.New("no program built")
)

// executablesRecord is the on-disk record of the programs the executable
// components built: the names in bin/ each of them put there, as the last
// build of that component found them.
type executablesRecord struct {
	Components map[string][]string `json:"components"`
}

// readExecutables reads the record under stateDir, the directory of the
// build's own state (project.stateDir). A missing record is an empty one,
// and so is one that cannot be read: it is tt's own state, and the next
// build of each component writes its entry again.
func readExecutables(stateDir string, warn func(string)) map[string][]string {
	path := filepath.Join(stateDir, executablesFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) && warn != nil {
			warn(fmt.Sprintf("ignoring %s: %s", path, err))
		}

		return map[string][]string{}
	}

	var record executablesRecord

	err = json.Unmarshal(data, &record)
	if err != nil || record.Components == nil {
		if err != nil && warn != nil {
			warn(fmt.Sprintf("ignoring %s: %s", path, err))
		}

		return map[string][]string{}
	}

	return record.Components
}

// keepExecutable is the record of programs without the components the
// manifest no longer declares executable: a component that was removed or
// turned into a module owns nothing in bin/ any more.
func keepExecutable(man *manifest.Manifest, programs map[string][]string) map[string][]string {
	kept := map[string][]string{}

	for name, names := range programs {
		if man.Components[name].IsExecutable() {
			kept[name] = names
		}
	}

	return kept
}

// writeExecutables writes the record under stateDir, kept by keepExecutable,
// and returns what it wrote.
func writeExecutables(stateDir string, man *manifest.Manifest, programs map[string][]string) (
	map[string][]string, error,
) {
	kept := keepExecutable(man, programs)

	data, err := json.MarshalIndent(executablesRecord{Components: kept}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the record of programs: %w", err)
	}

	err = os.MkdirAll(stateDir, dirPerm)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", stateDir, err)
	}

	path := filepath.Join(stateDir, executablesFileName)

	err = os.WriteFile(path, append(data, '\n'), executablesFilePerm)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}

	return kept, nil
}

// fileStamp is what tells a file in bin/ changed while a backend ran.
type fileStamp struct {
	size    int64
	modTime time.Time
	mode    fs.FileMode
}

// stampDir records the files directly under dir. A missing directory has
// none; subdirectories are not programs tt instance could find.
func stampDir(dir string) (map[string]fileStamp, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]fileStamp{}, nil
		}

		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	stamps := make(map[string]fileStamp, len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filepath.Join(dir, entry.Name()), err)
		}

		stamps[entry.Name()] = fileStamp{info.Size(), info.ModTime(), info.Mode()}
	}

	return stamps, nil
}

// changedFiles lists the files of after that are new or differ from before.
func changedFiles(before, after map[string]fileStamp) []string {
	var changed []string

	for name, stamp := range after {
		old, ok := before[name]
		if !ok || !old.modTime.Equal(stamp.modTime) || old.size != stamp.size ||
			old.mode != stamp.mode {
			changed = append(changed, name)
		}
	}

	return changed
}

// buildExecutable runs the backend of the executable component of run into
// its output directory, the bin directory of the stage, and returns the
// programs it put there, checked.
//
// The programs a build names (sdkbuild.ProgramNamer) are its own. When it
// names them all - go and cargo build one program, named by the block - they
// are all it made. Otherwise it may write into TT_OUTPUT_DIR whatever else it
// makes, as a make or shell build does: those are the files of bin/ it
// created or changed while it ran. A make target that was up to date changes
// nothing, so the programs the component was recorded with stay its own
// while they are there.
func buildExecutable(ctx context.Context, run componentRun, recorded []string) ([]string, error) {
	dir := run.context.Dirs.Output

	before, err := stampDir(dir)
	if err != nil {
		return nil, err
	}

	err = runComponentBackend(ctx, run)
	if err != nil {
		return nil, err
	}

	var (
		programs   []string
		exhaustive bool
	)

	if namer, ok := run.build.(sdkbuild.ProgramNamer); ok {
		programs, exhaustive = namer.Programs()
		programs = slices.Clone(programs)
	}

	if !exhaustive {
		after, err := stampDir(dir)
		if err != nil {
			return nil, err
		}

		programs = append(programs, writtenFiles(before, after, recorded)...)
	}

	slices.Sort(programs)

	programs = slices.Compact(programs)
	if len(programs) == 0 {
		return nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w: it put no file "+
			"into %s; an executable component builds programs into TT_OUTPUT_DIR or lists "+
			"them in output", run.name, errNoProgram, dir)
	}

	target := native.Platform{OS: run.context.Target.OS, Arch: run.context.Target.Arch}

	err = checkPrograms(dir, programs, target, run.context.Static)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name, err)
	}

	return programs, nil
}

// checkPrograms checks that every file of names under dir is an executable
// program of target, and a static one when static is set. Every problem is
// reported at once, so that one pass fixes them.
//
// tt instance runs these files as the external processes of the
// application, on the machine the package is installed on: a script, a
// library or a program of another platform would fail only there, and a
// program that needs the glibc of the build machine only on a machine with
// another one.
func checkPrograms(dir string, names []string, target native.Platform, static bool) error {
	var problems []error

	for _, name := range names {
		err := checkProgram(filepath.Join(dir, name), target, static)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", filepath.Join(dir, name), err))
		}
	}

	return errors.Join(problems...)
}

// checkProgram is checkPrograms for one file.
func checkProgram(path string, target native.Platform, static bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %w", errNotProgram, err)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: it is not a regular file", errNotProgram)
	}

	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: it is not executable", errNotProgram)
	}

	prog, err := native.InspectProgram(path)
	if errors.Is(err, native.ErrNotNative) {
		return fmt.Errorf("%w: it is neither an ELF nor a Mach-O program", errNotProgram)
	}

	if err != nil {
		return fmt.Errorf("%w: %w", errNotProgram, err)
	}

	if !prog.Executable {
		return fmt.Errorf("%w: it is a library or an object file, not a program", errNotProgram)
	}

	if !slices.Contains(prog.Platforms, target) {
		return fmt.Errorf("%w: it runs on %s, but the build is for %s", errNotProgram,
			native.Describe(prog.Platforms), target)
	}

	if static && !prog.Static() {
		return fmt.Errorf("%w: %s", errNotStatic, describeLinkage(prog))
	}

	return nil
}

// describeLinkage tells what a dynamic program needs from the system.
func describeLinkage(prog native.Program) string {
	var needs []string

	if prog.Interpreter != "" {
		needs = append(needs, "the loader "+prog.Interpreter)
	}

	if len(prog.Needed) > 0 {
		needs = append(needs, "the libraries "+strings.Join(prog.Needed, ", "))
	}

	return "it needs " + strings.Join(needs, " and ")
}
