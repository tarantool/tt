package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk/v3"
	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// What a build makes is typed by the directory of the package's stage it
// goes to (sdkbuild.Result): a module into lib/<namespace>/, its
// TT_OUTPUT_DIR; a data file into lua/<namespace>/, which the package's rock
// deploys to share/; a program into bin/, which the rock deploys to
// <tree>/bin/ as it is. One component may make all three. The bin directory
// of the tree is shared: the rocks of the dependency closure install their
// console scripts there, luatest's among them, and every component of the
// package its programs. The build records what each component made, by the
// component and the type, so that a build of one component knows what the
// others made, and tt instance tells which component makes a program.
//
// The record lives in _build/, the build's own state, rather than in the
// tree: it never travels into an archive and is never installed.
const (
	// buildDirName is the project directory of the build's own state.
	buildDirName = "_build"
	// resultsFileName is the record of what the components built, under
	// buildDirName.
	resultsFileName = "results.json"
	// resultsFilePerm is the mode the record is written with.
	resultsFilePerm os.FileMode = 0o644
)

var (
	// errNotProgram reports a file a build put into bin/ that is not a
	// program of the build platform. It carries exit code 2.
	errNotProgram = errors.New("not a program of the build platform")
	// errNotModule reports a native file a build made among the modules that
	// does not run on the platform it built for. It carries exit code 2.
	errNotModule = errors.New("not a module of the build platform")
	// errNotStatic reports a program of a build asking for static = true that
	// needs a loader or a shared library. It carries exit code 2.
	errNotStatic = errors.New("not a static program")
	// errNoProgram reports a component of kind = "executable" whose build
	// put no program into bin/. It carries exit code 2.
	errNoProgram = errors.New("no program built")
	// errNotOnlyPrograms reports a component of kind = "executable" whose
	// build made modules or data files. It carries exit code 2.
	errNotOnlyPrograms = errors.New(`kind = "executable", but the build made more than programs`)
	// errProgramOfModule reports a component of kind = "module" whose build
	// made programs. It carries exit code 2.
	errProgramOfModule = errors.New(`kind = "module", but the build made programs`)
	// errNotProduced reports a build that made a file of a type its backend
	// does not produce (sdkbuild.Backend.Produces). It carries exit code 2.
	errNotProduced = sdkbuild.ErrNotProduced
)

// madeFiles are what the build of a component made, by type: the modules it
// wrote into its output directory and the data files into its share
// directory, each relative to its directory, and the programs it put into
// bin/, by name. It is the entry of a component in the record.
type madeFiles struct {
	Modules  []string `json:"modules,omitempty"`
	Data     []string `json:"data,omitempty"`
	Programs []string `json:"programs,omitempty"`
}

// paths are the files of made, absolute in dirs.
func (made madeFiles) paths(dirs sdkbuild.Dirs) []string {
	return slices.Concat(joinDir(dirs.Modules, made.Modules), joinDir(dirs.Share, made.Data),
		joinDir(dirs.Programs, made.Programs))
}

// join is made and other.
func (made madeFiles) join(other madeFiles) madeFiles {
	return madeFiles{
		Modules:  slices.Concat(made.Modules, other.Modules),
		Data:     slices.Concat(made.Data, other.Data),
		Programs: slices.Concat(made.Programs, other.Programs),
	}
}

// of returns the files of made of the type result, relative to the
// directory of the type.
func (made madeFiles) of(result sdkbuild.Result) []string {
	switch result {
	case sdkbuild.Module:
		return made.Modules
	case sdkbuild.Data:
		return made.Data
	case sdkbuild.Program:
		return made.Programs
	default:
		return nil
	}
}

// only returns made without the files of the types results do not name.
func (made madeFiles) only(results []sdkbuild.Result) madeFiles {
	keep := func(result sdkbuild.Result) []string {
		if slices.Contains(results, result) {
			return made.of(result)
		}

		return nil
	}

	return madeFiles{
		Modules:  keep(sdkbuild.Module),
		Data:     keep(sdkbuild.Data),
		Programs: keep(sdkbuild.Program),
	}
}

// madeIn is what of paths, absolute files of the stage, is in the modules,
// the share and the programs directories of dirs.
func madeIn(dirs sdkbuild.Dirs, paths []string) madeFiles {
	return madeFiles{
		Modules: inDir(dirs.Modules, paths), Data: inDir(dirs.Share, paths),
		Programs: inDir(dirs.Programs, paths),
	}
}

// resultsRecord is the on-disk record of what the components built, as the
// last build of each component found it.
type resultsRecord struct {
	Components map[string]madeFiles `json:"components"`
}

// readResults reads the record under stateDir, the directory of the build's
// own state (project.stateDir). A missing record is an empty one, and so is
// one that cannot be read: it is tt's own state, and the next build of each
// component writes its entry again.
func readResults(stateDir string, warn func(string)) map[string]madeFiles {
	path := filepath.Join(stateDir, resultsFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) && warn != nil {
			warn(fmt.Sprintf("ignoring %s: %s", path, err))
		}

		return map[string]madeFiles{}
	}

	var record resultsRecord

	err = json.Unmarshal(data, &record)
	if err != nil || record.Components == nil {
		if err != nil && warn != nil {
			warn(fmt.Sprintf("ignoring %s: %s", path, err))
		}

		return map[string]madeFiles{}
	}

	return record.Components
}

// RecordedPrograms returns the programs the last builds of the components of
// the project at projectDir put into .rocks/bin/, by component, as the
// record of its build has them; nil when there is none. tt instance tells by
// it which component makes a program it does not find.
func RecordedPrograms(projectDir string) map[string][]string {
	var programs map[string][]string

	for name, made := range readResults(filepath.Join(projectDir, buildDirName), nil) {
		if len(made.Programs) == 0 {
			continue
		}

		if programs == nil {
			programs = map[string][]string{}
		}

		programs[name] = made.Programs
	}

	return programs
}

// keepDeclared is the record without the components the manifest no longer
// builds: a component that was removed, or lost its build block, owns
// nothing any more.
func keepDeclared(man *manifest.Manifest, results map[string]madeFiles) map[string]madeFiles {
	kept := map[string]madeFiles{}

	for name, made := range results {
		if component, ok := man.Components[name]; ok && component.Build != nil {
			kept[name] = made
		}
	}

	return kept
}

// writeResults writes the record under stateDir, kept by keepDeclared, and
// returns what it wrote.
func writeResults(stateDir string, man *manifest.Manifest, results map[string]madeFiles) (
	map[string]madeFiles, error,
) {
	kept := keepDeclared(man, results)

	data, err := json.MarshalIndent(resultsRecord{Components: kept}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the record of the build: %w", err)
	}

	err = os.MkdirAll(stateDir, dirPerm)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", stateDir, err)
	}

	path := filepath.Join(stateDir, resultsFileName)

	err = os.WriteFile(path, append(data, '\n'), resultsFilePerm)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}

	return kept, nil
}

// programsOf returns the programs of results, by component, of the
// components that made any.
func programsOf(results map[string]madeFiles) map[string][]string {
	programs := map[string][]string{}

	for name, made := range results {
		if len(made.Programs) > 0 {
			programs[name] = made.Programs
		}
	}

	return programs
}

// fileStamp is what tells a file changed while a backend ran.
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

// madePrograms returns the programs the build of run put into the directory
// of the programs, which was stamped before it ran: those the build names
// (sdkbuild.ProgramNamer) and, unless it names them all - go and cargo
// build the programs their blocks name - the files there it created or
// changed, and those of recorded, which an earlier build made, that are
// still there: a make target that was up to date changes nothing.
func madePrograms(run componentRun, before map[string]fileStamp, recorded []string) (
	[]string, error,
) {
	var (
		programs   []string
		exhaustive bool
	)

	if namer, ok := run.build.(sdkbuild.ProgramNamer); ok {
		programs, exhaustive = namer.Programs()
		programs = slices.Clone(programs)
	}

	if !exhaustive {
		after, err := stampDir(run.context.Dirs.Programs)
		if err != nil {
			return nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name, err)
		}

		programs = append(programs, writtenFiles(before, after, recorded)...)
	}

	slices.Sort(programs)

	return slices.Compact(programs), nil
}

// checkMade checks what the build of run made: against the deprecated kind
// of its component - kind = "executable" makes programs and nothing else,
// kind = "module" makes no program - then against what its backend produces
// (checkProduced), that each native module it made runs on the build
// platform (checkModules), and that each program it made is a program of
// the build platform, a static one when its block asks for one
// (checkPrograms).
func checkMade(run componentRun, stage string, made madeFiles) error {
	dirs := run.context.Dirs

	switch run.component.Kind {
	case manifest.KindExecutable:
		other := slices.Concat(joinDir(dirs.Modules, made.Modules), joinDir(dirs.Share, made.Data))
		if len(other) > 0 {
			return sdk.Errorf(sdk.ExitSystem, "building component %q: %w: %s", run.name,
				errNotOnlyPrograms, strings.Join(inDir(stage, other), ", "))
		}

		if len(made.Programs) == 0 {
			return sdk.Errorf(sdk.ExitSystem, "building component %q: %w: it put no file "+
				"into %s; a component of kind = %q builds programs into TT_OUTPUT_DIR or "+
				"lists them in output", run.name, errNoProgram, dirs.Programs,
				manifest.KindExecutable)
		}
	case manifest.KindModule:
		if len(made.Programs) > 0 {
			return sdk.Errorf(sdk.ExitSystem, "building component %q: %w: %s", run.name,
				errProgramOfModule,
				strings.Join(inDir(stage, joinDir(dirs.Programs, made.Programs)), ", "))
		}
	}

	err := checkProduced(run, stage, made)
	if err != nil {
		return err
	}

	target := native.Platform{OS: run.context.Target.OS, Arch: run.context.Target.Arch}

	err = checkModules(dirs.Modules, made.Modules, target)
	if err == nil {
		err = checkPrograms(dirs.Programs, made.Programs, target, run.context.Static)
	}

	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name, err)
	}

	return nil
}

// checkModules checks that every native file of names under dir, the
// modules a build made, runs on target, the platform of the build, as
// checkPrograms checks the programs: a module of another platform fails
// only where Tarantool loads it, and the build of a make or shell component
// that compiled for the machine, whatever TT_PLATFORM_OS and
// TT_PLATFORM_ARCH said, makes one in a build for another platform. A file
// that is not native - Lua, a .wasm - is not looked at, nor one that cannot
// be read. Every problem is reported at once.
func checkModules(dir string, names []string, target native.Platform) error {
	var problems []error

	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))

		platforms, err := native.FilePlatforms(path)
		if err != nil || platforms == nil || slices.Contains(platforms, target) {
			continue
		}

		problems = append(problems, fmt.Errorf("%s: %w: it runs on %s, but the build is for %s",
			path, errNotModule, native.Describe(platforms), target))
	}

	return errors.Join(problems...)
}

// checkProduced checks that what the build of run made is of the types its
// backend produces (sdkbuild.Backend.Produces): a file it left in the
// directory of another type fails the build, as the kind of a component
// does, rather than be installed as what the backend says it never makes.
// made holds no file an earlier build recorded of such a type: those are
// not taken as made (runComponent), and are removed from the stage.
func checkProduced(run componentRun, stage string, made madeFiles) error {
	for _, result := range sdkbuild.Results() {
		files := made.of(result)
		if len(files) == 0 || slices.Contains(run.backend.Produces, result) {
			continue
		}

		return sdk.Errorf(sdk.ExitSystem, "building component %q: %w: backend %q makes no %s: %s",
			run.name, errNotProduced, run.backend.Name, buildreg.Plural(result),
			strings.Join(inDir(stage, joinDir(run.context.Dirs.Of(result), files)), ", "))
	}

	return nil
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
