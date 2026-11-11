package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// errNotStaticModule reports a native file of a module component whose build
// asks for static = true that needs a library a machine Tarantool runs on may
// not have. It carries exit code 2.
var errNotStaticModule = errors.New("not a static module")

// moduleFiles lists the files the build of a module component made for it,
// by absolute path: written, what it wrote into outputDir, relative to it,
// and laidOut, the files of its directory compAbsPath the layout takes, where
// a make build may leave the module it compiled.
func moduleFiles(
	outputDir string, written []string, compAbsPath string, laidOut []string,
) []string {
	files := make([]string, 0, len(written)+len(laidOut))

	for _, file := range written {
		files = append(files, filepath.Join(outputDir, filepath.FromSlash(file)))
	}

	for _, file := range laidOut {
		files = append(files, filepath.Join(compAbsPath, filepath.FromSlash(file)))
	}

	return files
}

// checkStaticModule checks that every native file of files, which the build
// of the module component name made, needs no library but those of the C
// library and the loader (native.Program.ModuleExtras): the module of a build
// with static = true is to load wherever Tarantool runs, from the binary rock
// of the package as from the tree it was built in. Every file that needs more
// is reported at once, as checkPrograms reports programs. A file that is not
// native - Lua, text - is not looked at, and neither is a symbolic link: the
// file it points to is one of files when the build made it.
//
// A file that needs a newer glibc than the Tarantool it is built for,
// opts.Tarantool, passes with a warning: it loads only where Tarantool runs
// on a glibc as new as that, and what fixes it is a build machine with an
// older glibc, not a change to the module.
func checkStaticModule(opts Options, name string, files []string) error {
	tarantoolGlibc := sync.OnceValue(func() string {
		return programGlibc(opts.Tarantool.Executable)
	})

	var (
		problems []error
		warnings []string
	)

	for _, path := range files {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		prog, err := native.InspectProgram(path)
		if errors.Is(err, native.ErrNotNative) {
			continue
		}

		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w: %w", path, errNotStaticModule, err))

			continue
		}

		extras := prog.ModuleExtras()
		if len(extras) > 0 {
			problems = append(problems,
				fmt.Errorf("%s: %w: %s", path, errNotStaticModule, describeExtras(extras)))
		}

		if prog.Glibc != "" {
			warning := newerGlibc(name, path, prog.Glibc, opts.Tarantool.Executable,
				tarantoolGlibc())
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
	}

	opts.emit(warnings)

	if len(problems) > 0 {
		return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name,
			errors.Join(problems...))
	}

	return nil
}

// describeExtras tells what a module needs that a machine Tarantool runs on
// may not have, and how a build does without it.
func describeExtras(extras []string) string {
	pronoun := "it"
	if len(extras) > 1 {
		pronoun = "them"
	}

	return fmt.Sprintf("it needs %s, which a machine Tarantool runs on may not have: "+
		"link %s in statically, or load %s at run time with dlopen",
		joinNames(extras), pronoun, pronoun)
}

// joinNames joins names for a message: "a", "a and b", "a, b and c".
func joinNames(names []string) string {
	last := len(names) - 1
	if last < 1 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:last], ", ") + " and " + names[last]
}

// programGlibc is the newest version of glibc the program at path needs
// (native.Program.Glibc); empty when there is no program there or it tells
// none.
func programGlibc(path string) string {
	if path == "" {
		return ""
	}

	prog, err := native.InspectProgram(path)
	if err != nil {
		return ""
	}

	return prog.Glibc
}

// newerGlibc is the warning about the file at path of the module component
// name, which needs glibc module, when that is newer than tarantool, the
// glibc the Tarantool at executable needs; empty when it is not, or either is
// not known.
func newerGlibc(name, path, module, executable, tarantool string) string {
	if module == "" || tarantool == "" || native.CompareGlibc(module, tarantool) <= 0 {
		return ""
	}

	return fmt.Sprintf("component %q: %s needs glibc %s, and %s, the Tarantool it is "+
		"built for, needs %s: the module does not load where Tarantool runs on a glibc "+
		"older than %s; build it with an older glibc for it to load there",
		name, path, module, executable, tarantool, module)
}
