package cc

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/macho"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build/v3"
)

// This file bundles shared libraries the module links next to it: each of
// bundle is copied into the directory of the module, under the name the
// module asks the loader for, and the module finds it there by an rpath of
// its directory, $ORIGIN, @loader_path on darwin. The copies are modules
// of the build to the core: it installs them beside the module, into
// .rocks/lib/tarantool/<namespace>/, and packs them.

// checkBundle checks bundle, the libraries a build of the backend name
// bundles: each is the name of a library, as -l takes it, and is listed
// once. The c backend has none: Tarantool copies a module of
// box.schema.func into a temporary directory to load it, where the
// libraries beside the module are not.
func checkBundle(bundle []string, name string) error {
	if len(bundle) > 0 && name == CName {
		return build.FieldErrorf("bundle", "does nothing on backend %q: Tarantool copies a "+
			"module of box.schema.func into a temporary directory and loads it from there, "+
			"where the libraries beside the module are not; link them statically, or build "+
			"a module of %s", name, LuaCName)
	}

	for index, library := range bundle {
		field := fmt.Sprintf("bundle[%d]", index)

		switch {
		case library == "":
			return build.FieldErrorf("bundle", "must not contain an empty entry")
		case strings.ContainsAny(library, "/:"):
			return build.FieldErrorf(field, "%q is not the name of a library, as libraries "+
				"has it: foo for libfoo.so", library)
		case slices.Contains(bundle[:index], library):
			return build.FieldErrorf(field, "%q is listed twice", library)
		}
	}

	return nil
}

// bundledLibrary is a shared library a build bundles next to its module.
type bundledLibrary struct {
	// field is the field of bundle that names it.
	field string
	// name is its name, as bundle has it.
	name string
	// source is the shared library the link takes, its symbolic links
	// followed.
	source string
	// copyName is the name the module asks the loader for: the SONAME of
	// the library, the base name of its install name on darwin, or the name
	// of the file the link takes when it has none.
	copyName string
	// copy is the path of the copy, in the directory of the module.
	copy string
	// linkArg is what the link takes for -l<name>: the copy on darwin,
	// whose install name the module records; empty, -l<name>, elsewhere.
	linkArg string
}

// bundledLibraries are the libraries a build bundles, in the order of
// bundle.
type bundledLibraries []bundledLibrary

// lookup returns the library of b of the name name.
func (b bundledLibraries) lookup(name string) (bundledLibrary, bool) {
	for _, library := range b {
		if library.name == name {
			return library, true
		}
	}

	return bundledLibrary{
		field: "", name: "", source: "", copyName: "", copy: "", linkArg: "",
	}, false
}

// copyNames are the names of the copies of b.
func (b bundledLibraries) copyNames() []string {
	names := make([]string, 0, len(b))

	for _, library := range b {
		names = append(names, library.copyName)
	}

	return names
}

// sharedNameRe matches the file of a shared library, lib<name>.so,
// lib<name>.so.<version>, lib<name>.dylib and lib<name>.<version>.dylib,
// with its name.
var sharedNameRe = regexp.MustCompile(`^lib(.+?)(\.so(\.[0-9][0-9.]*)?|(\.[0-9][0-9.]*)?\.dylib)$`)

// linkedNames are the names of the libraries the build of config for goos
// links, the libraries a build may bundle, each with the file a file entry
// of libraries names, relative to cwd, or with none: the names of
// libraries, those of its shared library files, those of link and the -l of
// pkg-config.
func linkedNames(config Config, goos, cwd string, pkg pkgFlags,
	linked linkedLibraries,
) map[string]string {
	overlay := config.Platforms[goos]
	names := map[string]string{}

	for _, entry := range concat(config.Libraries, overlay.Libraries) {
		if !isLibraryFile(entry) {
			names[entry] = ""

			continue
		}

		match := sharedNameRe.FindStringSubmatch(filepath.Base(entry))
		if match != nil {
			names[match[1]] = inDir(cwd, entry)
		}
	}

	for _, name := range linked.names {
		names[name] = ""
	}

	for _, word := range pkg.libs {
		if name, ok := strings.CutPrefix(word, "-l"); ok && name != "" {
			names[name] = ""
		}
	}

	return names
}

// bundledLibrariesOf finds the shared library of each of bundle of the build
// of config in c, whose compiler that links is linker: the file of a file
// entry of libraries, or lib<name>.so (.dylib on darwin) in the directories
// of the link (searchDirs), or where the compiler finds it
// (-print-file-name). It copies each into the directory of the module
// before the link: the module is loaded once it is linked (testLoad), and
// finds the copy there by $ORIGIN; on darwin the link takes the copy, with
// its install name (bundleDarwin).
func bundledLibrariesOf(ctx context.Context, c *build.Context, config Config, pkg pkgFlags,
	linked linkedLibraries, linker []string,
) (bundledLibraries, error) {
	if len(config.Bundle) == 0 {
		return nil, nil
	}

	goos := c.Target.OS
	names := linkedNames(config, goos, c.Dirs.Cwd, pkg, linked)
	dirs := searchDirs(config, goos, c.Dirs.Cwd, pkg, linked)
	ext := ".so"

	if goos == darwinOS {
		ext = ".dylib"
	}

	bundled := make(bundledLibraries, 0, len(config.Bundle))

	for index, name := range config.Bundle {
		library := bundledLibrary{
			field: fmt.Sprintf("bundle[%d]", index), name: name, source: "", copyName: "",
			copy: "", linkArg: "",
		}

		file, links := names[name]
		if !links {
			return nil, build.FieldErrorf(library.field, "%q is no library the module links: "+
				"bundle names one of libraries, of link or of pkg_config", name)
		}

		if file == "" {
			file = findLibrary(ctx, c, linker, dirs, "lib"+name+ext)
		}

		if file == "" {
			return nil, build.FieldErrorf(library.field, "no shared library lib%s%s to bundle "+
				"in %s, nor where %s finds one", name, ext, orList(dirs), commandLine(linker))
		}

		err := library.find(file, goos)
		if err != nil {
			return nil, build.FieldErrorf(library.field, "%q: %v", name, err)
		}

		library.copy = filepath.Join(c.Dirs.Modules, library.copyName)

		if goos == darwinOS {
			err = bundleDarwin(ctx, c, library)
			library.linkArg = library.copy
		} else {
			err = build.CopyFile(library.copy, library.source)
		}

		if err != nil {
			return nil, fmt.Errorf("bundling %s: %w", library.source, err)
		}

		bundled = append(bundled, library)
	}

	return bundled, nil
}

// darwinOS is the GOOS of macOS.
const darwinOS = "darwin"

// findLibrary returns the first file of the name file in dirs, or the one
// the compiler of the words linker finds (printFileName); empty for none.
func findLibrary(ctx context.Context, c *build.Context, linker, dirs []string,
	file string,
) string {
	for _, dir := range dirs {
		candidate := filepath.Join(dir, file)
		if isRegularFile(candidate) {
			return candidate
		}
	}

	return printFileName(ctx, c, linker, file)
}

// find fills the source and the name of the copy of library from the shared
// library file, the one the link takes, of a build for goos: the file its
// symbolic links lead to, and its SONAME, or the base name of its install
// name on darwin, the base name of file when it has none.
func (library *bundledLibrary) find(file, goos string) error {
	source, err := filepath.EvalSymlinks(file)
	if err != nil {
		return fmt.Errorf("following %s: %w", file, err)
	}

	library.source = source
	library.copyName = filepath.Base(file)

	var installName string

	if goos == darwinOS {
		installName, err = machoDylibID(source)
	} else {
		installName, err = elfSoname(source)
	}

	if err != nil {
		return err
	}

	if installName != "" {
		library.copyName = path.Base(installName)
	}

	return nil
}

// elfSoname returns DT_SONAME of the shared library at path; empty when it
// has none.
func elfSoname(path string) (string, error) {
	file, err := elf.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	names, err := file.DynString(elf.DT_SONAME)
	if err != nil {
		return "", fmt.Errorf("reading the SONAME of %s: %w", path, err)
	}

	if len(names) == 0 {
		return "", nil
	}

	return names[0], nil
}

// loadCmdIDDylib is LC_ID_DYLIB, the load command of the install name of a
// dylib, which debug/macho leaves as bytes.
const loadCmdIDDylib macho.LoadCmd = 0xd

// dylibNameOffset is where the offset of the name is in a dylib_command:
// after cmd and cmdsize.
const dylibNameOffset = 8

// machoDylibID returns the install name of the dylib at path, LC_ID_DYLIB of
// its first slice; empty when it has none.
func machoDylibID(path string) (string, error) {
	fat, err := macho.OpenFat(path)
	if err == nil {
		defer func() { _ = fat.Close() }()

		if len(fat.Arches) == 0 {
			return "", nil
		}

		return dylibID(fat.Arches[0].File), nil
	}

	file, err := macho.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	return dylibID(file), nil
}

// dylibID returns the install name LC_ID_DYLIB of file holds; empty when it
// has none.
func dylibID(file *macho.File) string {
	for _, load := range file.Loads {
		raw := load.Raw()
		if len(raw) < dylibNameOffset+4 || macho.LoadCmd(file.ByteOrder.Uint32(raw)) !=
			loadCmdIDDylib {
			continue
		}

		offset := file.ByteOrder.Uint32(raw[dylibNameOffset:])
		if int(offset) >= len(raw) {
			return ""
		}

		name, _, _ := bytes.Cut(raw[offset:], []byte{0})

		return string(name)
	}

	return ""
}

// darwinBundleCommands are the commands that make the copy of a bundled
// dylib the one the module loads by its rpath: install_name_tool sets its
// install name to @rpath/<name>, which the link of the module records, and
// codesign signs it again, ad hoc, as the change of its load commands
// invalidates the signature a dylib of arm64 has to have.
func darwinBundleCommands(copyPath, copyName string) [][]string {
	return [][]string{
		{"install_name_tool", "-id", "@rpath/" + copyName, copyPath},
		{"codesign", "--force", "--sign", "-", copyPath},
	}
}

// bundleDarwin copies the dylib of library beside the module and makes the
// copy the one the module loads (darwinBundleCommands), before the link,
// which takes the copy.
func bundleDarwin(ctx context.Context, c *build.Context, library bundledLibrary) error {
	err := build.CopyFile(library.copy, library.source)
	if err != nil {
		return err
	}

	for _, argv := range darwinBundleCommands(library.copy, library.copyName) {
		err = runQuiet(ctx, c, argv)
		if err != nil {
			return err
		}
	}

	return nil
}

// runQuiet runs argv in the Context c, what it prints in the error of a
// run that fails.
func runQuiet(ctx context.Context, c *build.Context, argv []string) error {
	cmd := c.Command(ctx, argv[0], argv[1:]...)

	var output bytes.Buffer

	cmd.Stdout = &output
	cmd.Stderr = &output

	logger(c).DebugContext(ctx, "running "+commandLine(cmd.Args), "dir", cmd.Dir)

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", commandLine(cmd.Args), err,
			strings.TrimSpace(output.String()))
	}

	return nil
}

// finish completes the bundle of a build of c once its module out is
// linked, or found up to date: it checks that the module asks the loader
// for each library by the name of its copy - not when the link took the
// static library, or --as-needed dropped it - and warns of what a library
// needs that is not bundled, for a build without static = true: the core's
// static check reports it otherwise. The copies are there since before the
// link (bundledLibrariesOf).
func (b bundledLibraries) finish(c *build.Context, out string) error {
	if len(b) == 0 {
		return nil
	}

	needed, err := importedLibraries(out)
	if err != nil {
		return fmt.Errorf("bundling: %w", err)
	}

	for _, library := range b {
		want := library.copyName
		if c.Target.OS == darwinOS {
			want = "@rpath/" + library.copyName
		}

		if !slices.Contains(needed, want) {
			return build.FieldErrorf(library.field, "%q: the module does not need %s, the "+
				"library bundle copies: it links its static library, or -Wl,--as-needed "+
				"dropped it; the module needs %s", library.name, want,
				orNone(needed))
		}
	}

	if !c.Static {
		b.warnUnbundled(c)
	}

	return nil
}

// warnUnbundled warns of each library a library of b needs that is neither
// one of the C library nor bundled: the machine the module is installed on
// is to have it, as tt bundles no library a bundled one needs.
func (b bundledLibraries) warnUnbundled(c *build.Context) {
	bundled := b.copyNames()

	for _, library := range b {
		needed, err := importedLibraries(library.source)
		if err != nil {
			continue
		}

		var missing []string

		for _, need := range needed {
			name := strings.TrimPrefix(need, "@rpath/")
			if !systemLibrary(need, c.Target.OS) && !slices.Contains(bundled, name) {
				missing = append(missing, need)
			}
		}

		if len(missing) > 0 && c.Warn != nil {
			c.Warn(fmt.Sprintf("%s, which bundle copies beside the module, needs %s, which is "+
				"not bundled: the machine the module is installed on is to have it, or bundle "+
				"lists it too", library.copy, andList(missing)))
		}
	}
}

// orNone is names joined, or "nothing".
func orNone(names []string) string {
	if len(names) == 0 {
		return "nothing"
	}

	return strings.Join(names, ", ")
}

// importedLibraries returns the libraries the native file at path asks the
// loader for: DT_NEEDED of ELF, the dylibs of every slice of Mach-O.
func importedLibraries(path string) ([]string, error) {
	elfFile, err := elf.Open(path)
	if err == nil {
		defer func() { _ = elfFile.Close() }()

		needed, err := elfFile.ImportedLibraries()
		if err != nil {
			return nil, fmt.Errorf("reading the libraries of %s: %w", path, err)
		}

		return needed, nil
	}

	var elfFormat *elf.FormatError

	if !errors.As(err, &elfFormat) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	fat, err := macho.OpenFat(path)
	if err == nil {
		defer func() { _ = fat.Close() }()

		var needed []string

		for _, arch := range fat.Arches {
			libraries, err := arch.ImportedLibraries()
			if err != nil {
				return nil, fmt.Errorf("reading the libraries of %s: %w", path, err)
			}

			needed = append(needed, libraries...)
		}

		return needed, nil
	}

	file, err := macho.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	needed, err := file.ImportedLibraries()
	if err != nil {
		return nil, fmt.Errorf("reading the libraries of %s: %w", path, err)
	}

	return needed, nil
}

// elfSystemLibraries are the libraries of the C library and the GCC runtime
// a module of Linux may need wherever Tarantool runs, as the static check of
// the core has them; elfSystemLoaders match the names of the loaders.
var (
	elfSystemLibraries = []string{
		"libc.so.6", "libm.so.6", "libdl.so.2", "libpthread.so.0", "librt.so.1",
		"libgcc_s.so.1",
	}
	elfSystemLoaders = []string{"ld-linux*.so.*", "ld-musl-*.so.1", "libc.musl-*.so.1"}
	// machoSystemLibraries are those of darwin: libSystem and libc++.
	machoSystemLibraries = []string{"/usr/lib/libSystem.B.dylib", "/usr/lib/libc++.1.dylib"}
)

// systemLibrary reports whether a library of goos may need library wherever
// Tarantool runs.
func systemLibrary(library, goos string) bool {
	if goos == darwinOS {
		return slices.Contains(machoSystemLibraries, library)
	}

	if slices.Contains(elfSystemLibraries, library) {
		return true
	}

	return slices.ContainsFunc(elfSystemLoaders, func(pattern string) bool {
		matched, _ := path.Match(pattern, library)

		return matched
	})
}
