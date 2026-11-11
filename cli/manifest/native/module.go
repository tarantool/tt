package native

import (
	"debug/elf"
	"path"
	"slices"
	"strconv"
	"strings"
)

// A native module is not run but loaded into Tarantool, which has its own
// symbols for the module to take and the libraries it needs itself already
// loaded. The module needs no more than those to load on any machine
// Tarantool runs on.
//
// On Linux these are the C library and the loader, and the GCC runtime
// libgcc_s: every build of Tarantool, of the packages as of the static
// build, needs it, for the unwinding of its C++ code, so it is there
// wherever Tarantool starts. A C++ module that needs libstdc++ is not
// covered: Tarantool links it in statically, so the system may not have it.
var (
	// elfModuleLibraries are the libraries of the C library and the GCC
	// runtime a module may need by name: glibc's libc, and those glibc split
	// off from it, which it keeps as stubs since they went back into libc.
	elfModuleLibraries = []string{
		"libc.so.6", "libm.so.6", "libdl.so.2", "libpthread.so.0", "librt.so.1",
		"libgcc_s.so.1",
	}
	// elfModuleLoaders match the names of the loader, which glibc names
	// after the architecture (ld-linux-x86-64.so.2, ld-linux-aarch64.so.1),
	// and of musl, whose loader is its libc too, linked as libc.musl-<arch>
	// on Alpine.
	elfModuleLoaders = []string{"ld-linux*.so.*", "ld-musl-*.so.1", "libc.musl-*.so.1"}
)

// ModuleExtras lists the libraries of Needed a native module may not find on
// a machine Tarantool runs on: all but those of the C library and the
// loader. On Linux these are elfModuleLibraries and the libraries
// elfModuleLoaders match. On macOS the C library is libSystem, which holds
// the interface of the loader, libdyld, too; libc++, the C++ library of the
// system, is no more one of them than libstdc++ is on Linux. A universal
// Mach-O file needs the libraries of every slice (InspectProgram), and each
// has to be one of those.
//
// Unlike Static, ModuleExtras does not look at the loader a file names: the
// process that loads a module has one already.
func (p Program) ModuleExtras() []string {
	var extras []string

	for _, library := range p.Needed {
		if !p.moduleLibrary(library) {
			extras = append(extras, library)
		}
	}

	return extras
}

// moduleLibrary reports whether a module of every platform of p may need
// library.
func (p Program) moduleLibrary(library string) bool {
	if len(p.Platforms) == 0 {
		return false
	}

	for _, platform := range p.Platforms {
		if platform.OS == darwinOS {
			if library != libSystem {
				return false
			}

			continue
		}

		if !elfModuleLibrary(library) {
			return false
		}
	}

	return true
}

// elfModuleLibrary reports whether an ELF module may need library.
func elfModuleLibrary(library string) bool {
	if slices.Contains(elfModuleLibraries, library) {
		return true
	}

	for _, pattern := range elfModuleLoaders {
		matched, _ := path.Match(pattern, library)
		if matched {
			return true
		}
	}

	return false
}

// glibcVersionPrefix starts the names glibc gives the versions of its
// symbols, GLIBC_2.2.5 to GLIBC_2.43, besides GLIBC_PRIVATE.
const glibcVersionPrefix = "GLIBC_"

// glibcVersion is the newest version of glibc the version needs of file
// name, of libc and of every library glibc made. The loader refuses a file
// that needs a version its glibc does not have, so the file loads on that
// version of glibc and the newer ones only. A file with no table of version
// needs, as musl's are, needs none; one that has no section headers left -
// the table is found by them - is not told.
func glibcVersion(file *elf.File) string {
	needs, err := file.DynamicVersionNeeds()
	if err != nil {
		return ""
	}

	newest := ""

	for _, need := range needs {
		for _, dep := range need.Needs {
			version, ok := strings.CutPrefix(dep.Dep, glibcVersionPrefix)
			if !ok || glibcParts(version) == nil {
				continue
			}

			if newest == "" || CompareGlibc(version, newest) > 0 {
				newest = version
			}
		}
	}

	return newest
}

// CompareGlibc compares two versions of glibc, as Program.Glibc reports
// them: -1 when a is older than b, 0 when they are the same, +1 when a is
// newer. 2.17 is newer than 2.2.5. A version that is not dotted numbers is
// older than any that is.
func CompareGlibc(a, b string) int {
	return slices.Compare(glibcParts(a), glibcParts(b))
}

// glibcParts splits a version of glibc into its numbers; nil when it is not
// dotted numbers.
func glibcParts(version string) []int {
	fields := strings.Split(version, ".")
	parts := make([]int, 0, len(fields))

	for _, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return nil
		}

		parts = append(parts, number)
	}

	return parts
}
