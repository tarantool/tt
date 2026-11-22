package cc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build/v3"
)

// This file puts the libraries of the link together: the entries of
// libraries, a name or a file each, and what pkg-config gives for the
// packages of pkg_config, each library of a build with static = true taken
// from its static library, lib<name>.a, where there is one.

// staticExempt are the libraries a build with static = true links as they
// are, -l<name>, rather than from their static libraries: those of the C
// library and the GCC runtime, which a module may need wherever Tarantool
// runs (the static check of the core allows them), and whose static
// libraries are no part of a shared object - libc.a is not built with
// -fPIC.
var staticExempt = []string{"c", "m", "dl", "pthread", "rt", "gcc_s"}

// darwinStaticExempt is the library of the C library of darwin, libSystem,
// which every module of darwin needs and no static library has.
const darwinStaticExempt = "System"

// exemptFromStatic reports whether the library name of a build for goos is
// linked as it is under static = true (staticExempt).
func exemptFromStatic(name, goos string) bool {
	return slices.Contains(staticExempt, name) || goos == "darwin" && name == darwinStaticExempt
}

// isLibraryFile reports whether entry, of libraries, is a file rather than
// the name of a library: one with a directory, or that ends in .a. A name of
// GNU ld's :<file> is a name.
func isLibraryFile(entry string) bool {
	if strings.HasPrefix(entry, ":") {
		return false
	}

	return strings.Contains(entry, "/") || strings.HasSuffix(entry, ".a")
}

// staticFind returns the static library lib<name>.a of the library name, as
// a build with static = true links it: the first in dirs, the directories
// of the link, or the one the compiler that links finds itself; empty when
// there is none.
type staticFind func(dirs []string, name string) string

// libraryArgs are the libraries of the link, as the link has them.
type libraryArgs struct {
	// args are the arguments of the link after the objects: -L of each of
	// library_dirs, then each of libraries in its place, -l<name>, the file
	// of a file entry or the static library of a name, then what
	// pkg-config gave, each -l<name> in its place as well.
	args []string
	// files are the files of the libraries of the link key (linkKey): each
	// of the files of libraries, the static libraries the args take, the
	// shared libraries bundle copies, and what each of libraries may be in
	// library_dirs (libraryFiles).
	files []string
	// rpath are the entries of rpath the link has besides those of the
	// block: $ORIGIN, for a module that bundles libraries and whose rpath
	// has none.
	rpath []string
}

// searchDirs are the directories the libraries of the link of the build of
// config for goos in cwd are looked for in, absolute: library_dirs, then
// those of the components of link (linked), then the -L of pkg-config
// (pkg).
func searchDirs(config Config, goos, cwd string, pkg pkgFlags, linked linkedLibraries) []string {
	overlay := config.Platforms[goos]
	libraryDirs := concat(config.LibraryDirs, overlay.LibraryDirs)
	dirs := make([]string, 0, len(libraryDirs)+len(linked.dirs))

	for _, dir := range libraryDirs {
		dirs = append(dirs, inDir(cwd, dir))
	}

	dirs = append(dirs, linked.dirs...)

	for _, word := range pkg.libs {
		if dir, ok := strings.CutPrefix(word, "-L"); ok && dir != "" {
			dirs = append(dirs, inDir(cwd, dir))
		}
	}

	return dirs
}

// hasOrigin reports whether entries, of rpath, have the directory of the
// module itself.
func hasOrigin(entries []string) bool {
	return slices.ContainsFunc(entries, func(entry string) bool {
		return slices.Contains(originTokens, strings.TrimSuffix(entry, "/"))
	})
}

// planLibraries returns the libraries of the link of the build of config
// for goos in cwd, pkg the flags pkg-config gave and linked the libraries
// of the components of link, each library of a build with static = true
// that staticExempt does not name taken from its static library as find
// finds it, in library_dirs, then the directories of link, then the -L
// directories of pkg-config, then the directories of the compiler that
// links. A library whose static library is not found is linked as it is,
// -l<name>, and the static check of the core names the shared library the
// module needs. Of a build without static nothing changes, but the files of
// libraries, which go to the link as they are, after the objects in their
// place among the -l. The libraries of link come after those of libraries
// - but those libraries names, which keep their place - and before those of
// pkg-config. A library of bundle is linked as it is, shared, static or not,
// or by its copy on darwin (bundledLibrary.linkArg), and the module gets an
// rpath of its directory, $ORIGIN, unless rpath has one.
func planLibraries(config Config, goos, cwd string, pkg pkgFlags, linked linkedLibraries,
	bundled bundledLibraries, static bool, find staticFind,
) libraryArgs {
	overlay := config.Platforms[goos]
	libs := libraryArgs{
		args: nil, files: concat(libraryFiles(config, goos, cwd), linked.files), rpath: nil,
	}

	for _, dir := range concat(config.LibraryDirs, overlay.LibraryDirs) {
		libs.args = append(libs.args, "-L"+dir)
	}

	for _, dir := range linked.dirs {
		libs.args = append(libs.args, "-L"+dir)
	}

	dirs := searchDirs(config, goos, cwd, pkg, linked)

	if len(bundled) > 0 && !hasOrigin(concat(config.Rpath, overlay.Rpath)) {
		libs.rpath = []string{originToken}
	}

	for _, library := range bundled {
		libs.files = append(libs.files, library.source)
	}

	resolve := func(name string) string {
		if library, ok := bundled.lookup(name); ok {
			if library.linkArg != "" {
				return library.linkArg
			}

			return "-l" + name
		}

		if !static || exemptFromStatic(name, goos) || strings.HasPrefix(name, ":") {
			return "-l" + name
		}

		archive := find(dirs, name)
		if archive == "" {
			return "-l" + name
		}

		libs.files = append(libs.files, archive)

		return archive
	}

	libraries := concat(config.Libraries, overlay.Libraries)

	for _, entry := range libraries {
		if isLibraryFile(entry) {
			libs.args = append(libs.args, entry)
			libs.files = append(libs.files, inDir(cwd, entry))

			continue
		}

		libs.args = append(libs.args, resolve(entry))
	}

	for _, name := range linked.names {
		if !slices.Contains(libraries, name) {
			libs.args = append(libs.args, resolve(name))
		}
	}

	for _, word := range pkg.libs {
		if name, ok := strings.CutPrefix(word, "-l"); ok && name != "" {
			word = resolve(name)
		}

		libs.args = append(libs.args, word)
	}

	return libs
}

// inDir is path, relative to dir or absolute, as an absolute path.
func inDir(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(dir, path)
}

// staticFinder finds the static libraries of a build (staticFind) with the
// compiler that links, linker, run in the Context c.
func staticFinder(ctx context.Context, c *build.Context, linker []string) staticFind {
	return func(dirs []string, name string) string {
		file := "lib" + name + ".a"

		for _, dir := range dirs {
			path := filepath.Join(dir, file)
			if isRegularFile(path) {
				return path
			}
		}

		return printFileName(ctx, c, linker, file)
	}
}

// printFileName returns the path of the library file that the compiler of
// the words linker links, as -print-file-name=<file> of gcc, clang and zig
// cc prints it: an absolute path of a file that is there, or the name alone
// when it finds none, which is empty here.
func printFileName(ctx context.Context, c *build.Context, linker []string, file string) string {
	argv := concat(linker, []string{"-print-file-name=" + file})
	cmd := c.Command(ctx, argv[0], argv[1:]...)

	var stdout bytes.Buffer

	cmd.Stdout = &stdout

	logger(c).DebugContext(ctx, "running "+commandLine(cmd.Args), "dir", cmd.Dir)

	err := cmd.Run()
	if err != nil {
		return ""
	}

	path := strings.TrimSpace(stdout.String())
	if !filepath.IsAbs(path) || !isRegularFile(path) {
		return ""
	}

	return filepath.Clean(path)
}

// isRegularFile reports whether path is a regular file, or a link to one.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// pieHints match what the linkers print of an object that is not
// position-independent in a shared object: GNU ld ("relocation R_X86_64_32
// against ... can not be used when making a shared object; recompile with
// -fPIC"), gold and lld ("relocation R_X86_64_32 cannot be used against
// local symbol; recompile with -fPIC").
var pieHints = []string{"recompile with -fPIC", "can not be used when making a shared object",
	"cannot be used when making a shared object"}

// archiveRe matches the static library of an object a linker names, as
// /path/libfoo.a(foo.o) - lld names it after ">>> defined in".
var archiveRe = regexp.MustCompile(`([^\s'"(]+\.a)\(`)

// pieHint is the hint of a failed link whose linker printed the output
// printed, when it says an object of the link is not position-independent:
// the static library it names was not built with -fPIC. Empty for another
// failure.
func pieHint(printed string) string {
	if !slices.ContainsFunc(pieHints, func(hint string) bool {
		return strings.Contains(printed, hint)
	}) {
		return ""
	}

	const fix = "a module is a shared object, which takes position-independent code only: " +
		"build the library with -fPIC (CFLAGS=-fPIC, or " +
		"-DCMAKE_POSITION_INDEPENDENT_CODE=ON of cmake), or link its shared library"

	var archives []string

	for _, match := range archiveRe.FindAllStringSubmatch(printed, -1) {
		if !slices.Contains(archives, match[1]) {
			archives = append(archives, match[1])
		}
	}

	switch len(archives) {
	case 0:
		return "hint: an object of the link was not built with -fPIC: " + fix
	case 1:
		return "hint: the static library " + archives[0] + " was not built with -fPIC: " + fix
	default:
		return "hint: the static libraries " + andList(archives) +
			" were not built with -fPIC: " + fix
	}
}
