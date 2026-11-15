package cc

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

// linuxToolchain is the toolchain of a build for linux, as toolchainOf
// derives it there: -shared and $ORIGIN, so that placement can be asserted
// without depending on the host OS.
func linuxToolchain() toolchain {
	return toolchain{
		cc: []string{"cc"}, cxx: []string{"c++"}, pkgConfig: []string{"pkg-config"},
		libFlag: []string{"-shared"}, origin: "$ORIGIN", includeDir: "/tt/include",
	}
}

// darwinToolchain is the toolchain of a build for darwin: the -bundle flags
// and @loader_path.
func darwinToolchain() toolchain {
	return toolchain{
		cc:         []string{"cc"},
		cxx:        []string{"c++"},
		pkgConfig:  []string{"pkg-config"},
		libFlag:    []string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"},
		origin:     "@loader_path",
		includeDir: "/tt/include",
	}
}

// TestToolchainOf pins the toolchain of a build: CC and CXX of the
// environment, split into words, cc and c++ without them; the flags of the
// target; the headers under the prefix of the Tarantool of the build,
// whether or not module.h is there, and none without a prefix.
func TestToolchainOf(t *testing.T) {
	t.Parallel()

	noEnv := func(string) (string, bool) { return "", false }
	withEnv := func(variable, value string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			if key != variable {
				return "", false
			}

			return value, true
		}
	}

	linux := build.NewContext(build.Context{
		Target: build.Target{OS: "linux", Arch: "amd64"},
		Tarantool: build.Tarantool{
			Prefix: "/tt", IncludeDir: "/tt/include/tarantool", Version: "", Executable: "",
		},
	}, nil)

	want := linuxToolchain()

	want.includeDir = "/tt/include/tarantool"

	for _, variable := range []string{"CC", "CXX"} {
		for _, unset := range []string{"", " \t"} {
			tools, err := toolchainOf(linux, withEnv(variable, unset))
			require.NoError(t, err)
			assert.Equal(t, want, tools, "%s=%q", variable, unset)
		}
	}

	for value, words := range map[string][]string{
		"clang":                 {"clang"},
		"ccache cc":             {"ccache", "cc"},
		"zig cc -target x86_64": {"zig", "cc", "-target", "x86_64"},
		"'/opt/my tools/cc'":    {"/opt/my tools/cc"},
	} {
		wantCC := want

		wantCC.cc = words

		tools, err := toolchainOf(linux, withEnv("CC", value))
		require.NoError(t, err)
		assert.Equal(t, wantCC, tools, "CC=%q", value)

		wantCXX := want

		wantCXX.cxx = words

		tools, err = toolchainOf(linux, withEnv("CXX", value))
		require.NoError(t, err)
		assert.Equal(t, wantCXX, tools, "CXX=%q", value)

		wantPkgConfig := want

		wantPkgConfig.pkgConfig = words

		tools, err = toolchainOf(linux, withEnv("PKG_CONFIG", value))
		require.NoError(t, err)
		assert.Equal(t, wantPkgConfig, tools, "PKG_CONFIG=%q", value)
	}

	for _, variable := range []string{"CC", "CXX", "PKG_CONFIG"} {
		for value, wantErr := range map[string]error{
			"'cc":   errUnterminatedQuote,
			"cc \\": errTrailingBackslash,
			"'' cc": errNoProgram,
		} {
			_, err := toolchainOf(linux, withEnv(variable, value))
			require.ErrorIs(t, err, wantErr, "%s=%q", variable, value)
			assert.Contains(t, err.Error(), variable+"=", "%s=%q", variable, value)
		}
	}

	darwin := build.NewContext(build.Context{
		Target:    build.Target{OS: "darwin", Arch: "arm64"},
		Tarantool: build.Tarantool{Prefix: "", IncludeDir: "", Version: "", Executable: ""},
	}, nil)

	want = darwinToolchain()
	want.includeDir = ""

	tools, err := toolchainOf(darwin, noEnv)
	require.NoError(t, err)
	assert.Equal(t, want, tools)
}

// TestSplitWords pins how CC is split: as a shell splits a simple command,
// with its quoting and without its expansions.
func TestSplitWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" \t\r\n", nil},
		{"cc", []string{"cc"}},
		{"  ccache   cc  ", []string{"ccache", "cc"}},
		{"zig cc", []string{"zig", "cc"}},
		{"'/opt/my tools/cc' -m64", []string{"/opt/my tools/cc", "-m64"}},
		{`"/opt/my tools/cc"`, []string{"/opt/my tools/cc"}},
		{`/opt/my\ tools/cc`, []string{"/opt/my tools/cc"}},
		{`a'b c'd`, []string{"ab cd"}},
		{`'' x`, []string{"", "x"}},
		{`""`, []string{""}},
		{`'a\b'`, []string{`a\b`}},
		{`"a\b"`, []string{`a\b`}},
		{`"a\"b\\c\$d"`, []string{`a"b\c$d`}},
		{"\"a\\\nb\"", []string{"ab"}},
		{"a\\\nb", []string{"ab"}},
		{`\'x`, []string{"'x"}},
		{`$CC ~/cc *.c`, []string{"$CC", "~/cc", "*.c"}},
		{`"it's"`, []string{"it's"}},
		{`'say "hi"'`, []string{`say "hi"`}},
	}

	for _, test := range tests {
		words, err := splitWords(test.in)
		require.NoError(t, err, "%q", test.in)
		assert.Equal(t, test.want, words, "%q", test.in)
	}

	for value, want := range map[string]error{
		"'cc":     errUnterminatedQuote,
		`"cc`:     errUnterminatedQuote,
		`"cc\"`:   errUnterminatedQuote,
		`cc\`:     errTrailingBackslash,
		`'a' 'b`:  errUnterminatedQuote,
		`"a" "b\`: errUnterminatedQuote,
	} {
		_, err := splitWords(value)
		require.ErrorIs(t, err, want, "%q", value)
	}
}

// TestLanguageOf pins the languages of the extensions of sources:
// case-sensitive, .C none.
func TestLanguageOf(t *testing.T) {
	t.Parallel()

	for source, want := range map[string]language{
		"a.c": langC, "dir/a.b.c": langC, "../x/y.c": langC,
		"a.cc": langCXX, "a.cpp": langCXX, "a.cxx": langCXX, "a.c++": langCXX,
		"a.S": langAsmCpp, "a.s": langAsm,
	} {
		lang, ok := languageOf(source)
		require.True(t, ok, source)
		assert.Equal(t, want, lang, source)
	}

	for _, source := range []string{
		"a.C", "a.CPP", "a.cp", "a.h", "a.hpp", "a.o", "a.a", "a.so", "a.m", "a", "c", ".c.d",
	} {
		_, ok := languageOf(source)
		assert.False(t, ok, source)
	}

	assert.Equal(t, "c", langC.String())
	assert.Equal(t, "c++", langCXX.String())
	assert.Equal(t, "assembler-with-cpp", langAsmCpp.String())
	assert.Equal(t, "assembler", langAsm.String())
}

// TestObjectName pins the names of the objects: the base name of the source
// and a hash of its cleaned path, so that two sources of one name do not
// share an object and one path written two ways does.
func TestObjectName(t *testing.T) {
	t.Parallel()

	name := objectName("src/x.c")
	assert.Regexp(t, `^x\.c\.[0-9a-f]{8}\.o$`, name)
	assert.Equal(t, name, objectName("./src//x.c"))
	assert.Equal(t, name, objectName("src/y/../x.c"))

	sources := []string{"src/x.c", "lib/x.c", "../x.c", "/abs/x.c", "x.c", "x.cpp"}
	names := make([]string, 0, len(sources))

	for _, source := range sources {
		names = append(names, objectName(source))
	}

	assert.Len(t, slices.Compact(slices.Sorted(slices.Values(names))), len(names))
}

// noEnvFlags is an environment with neither CFLAGS, CXXFLAGS nor LDFLAGS
// set.
func noEnvFlags() envFlags {
	return envFlags{
		cflagsSet: false, cflags: nil, cxxflagsSet: false, cxxflags: nil, ldflags: nil,
	}
}

// noPkgFlags are the flags of a build without pkg_config.
func noPkgFlags() pkgFlags {
	return pkgFlags{cflags: nil, libs: nil}
}

// samplePkgFlags are flags pkg-config could give for openssl.
func samplePkgFlags() pkgFlags {
	return pkgFlags{
		cflags: []string{"-I/opt/ssl/include", "-DOPENSSL_API"},
		libs:   []string{"-L/opt/ssl/lib", "-lssl", "-lcrypto", "-pthread"},
	}
}

// TestPkgConfigPlacement pins where the flags pkg-config gives go: those of
// --cflags after the macros and the headers of every compile, C and C++,
// those of --libs after libraries, before rpath and LDFLAGS.
func TestPkgConfigPlacement(t *testing.T) {
	t.Parallel()

	for _, lang := range []language{langC, langCXX} {
		flags := compileFlags(sampleConfig(), linuxToolchain(), noEnvFlags(), samplePkgFlags(),
			"linux", lang)
		assert.Equal(t, []string{"-I/inc/lin", "-I/opt/ssl/include", "-DOPENSSL_API"},
			flags[len(flags)-3:], lang)
	}

	args := linkArgs(sampleConfig(), linuxToolchain(), userEnvFlags(),
		libsOf(sampleConfig(), linux.OS, samplePkgFlags()), linux.OS, true, false,
		[]string{"/w/a.o"}, "/out/mymod.so")
	assert.Equal(t, []string{
		"-lm", "-lz", "-llinlib", "-L/opt/ssl/lib", "-lssl", "-lcrypto", "-pthread",
		"-Wl,-rpath,$ORIGIN", "-Wl,-rpath,/opt/lib", "-Wl,-rpath,$ORIGIN/lin", "-Wl,-z,now",
	}, args[len(args)-11:])
}

// noLibs are the libraries of the link of a build of none.
func noLibs() libraryArgs {
	return libraryArgs{args: nil, files: nil}
}

// libsOf are the libraries of the link of config for goos in /src, pkg
// those pkg-config gave, of a build without static = true.
func libsOf(config Config, goos string, pkg pkgFlags) libraryArgs {
	return planLibraries(config, goos, "/src", pkg, noLinked(), nil, false,
		func([]string, string) string {
			panic("a build without static = true looks for no static library")
		})
}

// noLinked are the libraries of a build of no link.
func noLinked() linkedLibraries {
	return linkedLibraries{dirs: nil, includeDirs: nil, names: nil, files: nil}
}

// TestPlanLibraries pins the libraries of the link: -L of library_dirs,
// then libraries in their order, a file entry as it is, then what
// pkg-config gave; of a build with static = true, each library but those of
// the C library taken from its static library where one is found, in
// library_dirs, then the -L of pkg-config, then where the compiler finds
// it, and the libraries of the key with it.
func TestPlanLibraries(t *testing.T) {
	t.Parallel()

	config := moduleConfig("a.c")

	config.LibraryDirs = []string{"vendor/lib", "/opt/lib"}
	config.Libraries = []string{
		"foo", "vendor/libbar.a", "libbaz.a", "../shared/libqux.so", ":libquux.a", "m", "gcc_s",
		"System",
	}

	pkg := pkgFlags{cflags: nil, libs: []string{"-L/usr/ssl/lib", "-lssl", "-pthread", "-lz"}}

	libs := planLibraries(config, "linux", "/src", pkg, noLinked(), nil, false, nil)
	assert.Equal(t, []string{
		"-Lvendor/lib", "-L/opt/lib",
		"-lfoo", "vendor/libbar.a", "libbaz.a", "../shared/libqux.so", "-l:libquux.a", "-lm",
		"-lgcc_s", "-lSystem",
		"-L/usr/ssl/lib", "-lssl", "-pthread", "-lz",
	}, libs.args)
	assert.Equal(t, concat(libraryFiles(config, "linux", "/src"), []string{
		"/src/vendor/libbar.a", "/src/libbaz.a", "/shared/libqux.so",
	}), libs.files)

	var asked []string

	find := func(dirs []string, name string) string {
		asked = append(asked, name)

		assert.Equal(t, []string{"/src/vendor/lib", "/opt/lib", "/usr/ssl/lib"}, dirs)

		if name == "ssl" || name == "foo" {
			return "/found/lib" + name + ".a"
		}

		return ""
	}

	libs = planLibraries(config, "linux", "/src", pkg, noLinked(), nil, true, find)

	assert.Equal(t, []string{"foo", "System", "ssl", "z"}, asked, "libc, libm and libgcc_s "+
		"are linked as they are, libSystem only on darwin")
	assert.Equal(t, []string{
		"-Lvendor/lib", "-L/opt/lib",
		"/found/libfoo.a", "vendor/libbar.a", "libbaz.a", "../shared/libqux.so", "-l:libquux.a",
		"-lm", "-lgcc_s", "-lSystem",
		"-L/usr/ssl/lib", "/found/libssl.a", "-pthread", "-lz",
	}, libs.args)
	assert.Equal(t, concat(libraryFiles(config, "linux", "/src"), []string{
		"/found/libfoo.a", "/src/vendor/libbar.a", "/src/libbaz.a", "/shared/libqux.so",
		"/found/libssl.a",
	}), libs.files)

	asked = nil

	planLibraries(config, "darwin", "/src", pkg, noLinked(), nil, true, find)
	assert.Equal(t, []string{"foo", "ssl", "z"}, asked)
}

// TestPlanLibrariesOfLink pins where the libraries of the components of link
// go: -L of each directory after those of library_dirs, -l of each library
// after libraries, but one libraries names, which keeps its place, and the
// files of the directories of the key; with static = true each is looked for
// in library_dirs, then in the directories of link.
func TestPlanLibrariesOfLink(t *testing.T) {
	t.Parallel()

	config := moduleConfig("a.c")

	config.LibraryDirs = []string{"/opt/lib"}
	config.Libraries = []string{"bar", "m"}

	linked := linkedLibraries{
		dirs:        []string{"/state/link/default/foo", "/state/link/default/bar"},
		includeDirs: []string{"/state/link/default/foo/include"},
		names:       []string{"foo", "foo-extra", "bar"},
		files: []string{
			"/state/link/default/foo/libfoo.a", "/state/link/default/foo/libfoo-extra.so",
			"/state/link/default/bar/libbar.a",
		},
	}
	pkg := pkgFlags{cflags: nil, libs: []string{"-lssl"}}

	libs := planLibraries(config, "linux", "/src", pkg, linked, nil, false, nil)
	assert.Equal(t, []string{
		"-L/opt/lib", "-L/state/link/default/foo", "-L/state/link/default/bar",
		"-lbar", "-lm", "-lfoo", "-lfoo-extra", "-lssl",
	}, libs.args)
	assert.Equal(t, concat(libraryFiles(config, "linux", "/src"), linked.files), libs.files)

	find := func(dirs []string, name string) string {
		assert.Equal(t, []string{"/opt/lib", "/state/link/default/foo",
			"/state/link/default/bar"}, dirs)

		if name == "foo-extra" {
			return ""
		}

		return "/state/link/default/" + name + "/lib" + name + ".a"
	}

	libs = planLibraries(config, "linux", "/src", pkg, linked, nil, true, find)
	assert.Equal(t, []string{
		"-L/opt/lib", "-L/state/link/default/foo", "-L/state/link/default/bar",
		"/state/link/default/bar/libbar.a", "-lm", "/state/link/default/foo/libfoo.a",
		"-lfoo-extra", "/state/link/default/ssl/libssl.a",
	}, libs.args)
	assert.Equal(t, []string{"-I/state/link/default/foo/include"}, linked.includeFlags())
}

// TestPlanLibrariesOfBundle pins the link of a build that bundles a library:
// the library stays -l<name> on Linux, static = true or not, and is the copy
// beside the module on darwin; its file is of the key; and the module gets
// an rpath of its directory, unless rpath has one.
func TestPlanLibrariesOfBundle(t *testing.T) {
	t.Parallel()

	config := moduleConfig("a.c")

	config.Libraries = []string{"foo", "bar"}

	bundled := bundledLibraries{{
		field: "bundle[0]", name: "foo", source: "/opt/lib/libfoo.so.1.2", copyName: "libfoo.so.1",
		copy: "/stage/lib/libfoo.so.1", linkArg: "",
	}}
	find := func(_ []string, name string) string { return "/opt/lib/lib" + name + ".a" }

	libs := planLibraries(config, "linux", "/src", noPkgFlags(), noLinked(), bundled, true, find)
	assert.Equal(t, []string{"-lfoo", "/opt/lib/libbar.a"}, libs.args)
	assert.Equal(t, []string{originToken}, libs.rpath)
	assert.Contains(t, libs.files, "/opt/lib/libfoo.so.1.2")

	args := linkArgs(config, linuxToolchain(), noEnvFlags(), libs, "linux", true, false,
		[]string{"/w/a.o"}, "/out/m.so")
	assert.Equal(t, "-Wl,-rpath,$ORIGIN", args[len(args)-1])

	darwinBundled := slices.Clone(bundled)

	darwinBundled[0].linkArg = "/stage/lib/libfoo.1.dylib"

	libs = planLibraries(config, "darwin", "/src", noPkgFlags(), noLinked(), darwinBundled,
		false, nil)
	assert.Equal(t, []string{"/stage/lib/libfoo.1.dylib", "-lbar"}, libs.args)

	args = linkArgs(config, darwinToolchain(), noEnvFlags(), libs, "darwin", false, false,
		[]string{"/w/a.o"}, "/out/m.dylib")
	assert.Equal(t, "-Wl,-rpath,@loader_path", args[len(args)-1])

	for _, rpath := range []string{"$ORIGIN", "${ORIGIN}", "$ORIGIN/"} {
		config.Rpath = []string{rpath}

		libs = planLibraries(config, "linux", "/src", noPkgFlags(), noLinked(), bundled, false,
			nil)
		assert.Empty(t, libs.rpath, rpath)
	}

	config.Rpath = []string{"$ORIGIN/lib"}

	libs = planLibraries(config, "linux", "/src", noPkgFlags(), noLinked(), bundled, false, nil)
	assert.Equal(t, []string{originToken}, libs.rpath)
}

// TestLinkedNames pins the libraries a build may bundle: the names of
// libraries, those of its shared library files, with the files, those of
// link and the -l of pkg-config; not a static library file.
func TestLinkedNames(t *testing.T) {
	t.Parallel()

	config := moduleConfig("a.c")

	config.Libraries = []string{"foo", "vendor/libbar.so.2", "/opt/libbaz.1.dylib",
		"vendor/libqux.a", "./libquux.so"}
	config.Platforms = map[string]Overlay{"linux": {Libraries: []string{"rt"}}}

	linked := noLinked()

	linked.names = []string{"link"}

	assert.Equal(t, map[string]string{
		"foo": "", "bar": "/src/vendor/libbar.so.2", "baz": "/opt/libbaz.1.dylib",
		"quux": "/src/libquux.so", "rt": "", "link": "", "ssl": "",
	}, linkedNames(config, "linux", "/src",
		pkgFlags{cflags: nil, libs: []string{"-L/x", "-lssl", "-pthread"}}, linked))
}

// TestIsLibraryFile pins which entries of libraries are files.
func TestIsLibraryFile(t *testing.T) {
	t.Parallel()

	for _, entry := range []string{"libfoo.a", "vendor/libfoo.a", "/opt/lib/libfoo.so",
		"./libfoo.so.1", "../x/libfoo.dylib"} {
		assert.True(t, isLibraryFile(entry), entry)
	}

	for _, entry := range []string{"foo", "ssl", ":libfoo.a", ":libfoo.so.1", "stdc++"} {
		assert.False(t, isLibraryFile(entry), entry)
	}
}

// TestPieHint pins the hint of a link that fails for an object that is not
// position-independent, as GNU ld and lld say it, and that another failure
// gets none.
func TestPieHint(t *testing.T) {
	t.Parallel()

	gnu := "/usr/bin/ld: /src/vendor/libfoo.a(foo.o): relocation R_X86_64_32 against " +
		"`.rodata' can not be used when making a shared object; recompile with -fPIC\n" +
		"/usr/bin/ld: failed to set dynamic section sizes: bad value\n" +
		"collect2: error: ld returned 1 exit status"
	lld := "ld.lld: error: relocation R_X86_64_32 cannot be used against local symbol; " +
		"recompile with -fPIC\n>>> defined in /src/vendor/libfoo.a(foo.o)\n" +
		">>> referenced by foo.c\n>>>               foo.o:(foo) in archive /src/vendor/libfoo.a"

	for _, printed := range []string{gnu, lld} {
		assert.Equal(t, "hint: the static library /src/vendor/libfoo.a was not built with "+
			"-fPIC: a module is a shared object, which takes position-independent code only: "+
			"build the library with -fPIC (CFLAGS=-fPIC, or "+
			"-DCMAKE_POSITION_INDEPENDENT_CODE=ON of cmake), or link its shared library",
			pieHint(printed))
	}

	assert.Contains(t, pieHint(gnu+"\n/usr/bin/ld: /lib/libbar.a(bar.o): relocation "+
		"R_X86_64_32 against `.bss' can not be used when making a shared object; "+
		"recompile with -fPIC"),
		"hint: the static libraries /src/vendor/libfoo.a and /lib/libbar.a were not built")
	assert.Contains(t, pieHint("/usr/bin/ld: foo.o: relocation R_X86_64_32 against `.rodata' "+
		"can not be used when making a shared object; recompile with -fPIC"),
		"hint: an object of the link was not built with -fPIC")
	assert.Empty(t, pieHint("/usr/bin/ld: cannot find -lfoo: No such file or directory"))
}

// userEnvFlags is an environment with CFLAGS, CXXFLAGS and LDFLAGS set.
func userEnvFlags() envFlags {
	return envFlags{
		cflagsSet:   true,
		cflags:      []string{"-O0", "-g"},
		cxxflagsSet: true,
		cxxflags:    []string{"-O1", "-g3"},
		ldflags:     []string{"-Wl,-z,now"},
	}
}

// sampleConfig carries base lists plus distinct linux/darwin overlays so the
// overlay-selection and append-only-merge behavior is observable.
func sampleConfig() Config {
	return Config{
		Module:      "mymod",
		Sources:     []string{"a.c", "b.cpp"},
		Defines:     []string{"BASE=1"},
		IncludeDirs: []string{"/inc/base"},
		LibraryDirs: []string{"/lib/base"},
		Libraries:   []string{"m", "z"},
		Rpath:       []string{"$ORIGIN", "/opt/lib"},
		Cflags:      []string{"-std=gnu99", "-Werror"},
		Cxxflags:    []string{"-std=c++17", "-fno-rtti"},
		Ldflags:     []string{"-Wl,--as-needed"},
		Platforms: map[string]Overlay{
			"linux": {
				Defines:     []string{"LIN"},
				IncludeDirs: []string{"/inc/lin"},
				LibraryDirs: []string{"/lib/lin"},
				Libraries:   []string{"linlib"},
				Rpath:       []string{"${ORIGIN}/lin"},
				Cflags:      []string{"-Wno-psabi"},
				Cxxflags:    []string{"-Wno-class-memaccess"},
				Ldflags:     []string{"-Wl,-z,defs"},
			},
			"darwin": {
				Defines:     []string{"DARWIN"},
				IncludeDirs: []string{"/inc/dar"},
				LibraryDirs: []string{"/lib/dar"},
				Libraries:   []string{"darlib"},
				Rpath:       []string{"${ORIGIN}/dar"},
				Cflags:      []string{"-Wno-deprecated-declarations"},
				Cxxflags:    []string{"-stdlib=libc++"},
				Ldflags:     []string{"-Wl,-dead_strip"},
			},
		},
	}
}

// moduleConfig is the least config of a module m of sources.
func moduleConfig(sources ...string) Config {
	return Config{
		Module: "m", Sources: sources, IncludeDirs: nil, Libraries: nil, LibraryDirs: nil,
		Rpath: nil, PkgConfig: nil, Link: nil, Bundle: nil, Defines: nil, Cflags: nil,
		Cxxflags: nil, Ldflags: nil, Platforms: nil,
	}
}

// TestCompileFlagsLinux pins the flags of the compiles for linux: tt's, the
// project's of the language, the user's of it, then the macros and the
// headers of both, each list then its overlay.
func TestCompileFlagsLinux(t *testing.T) {
	t.Parallel()

	tail := []string{
		// the headers of Tarantool.
		"-I/tt/include",
		// defines: base then linux overlay.
		"-DBASE=1", "-DLIN",
		// include dirs: base then linux overlay.
		"-I/inc/base", "-I/inc/lin",
	}

	cflags := []string{"-O2", "-g", "-fPIC", "-std=gnu99", "-Werror", "-Wno-psabi"}

	for _, lang := range []language{langC, langAsmCpp, langAsm} {
		assert.Equal(t, concat(cflags, tail),
			compileFlags(sampleConfig(), linuxToolchain(), noEnvFlags(), noPkgFlags(), "linux",
				lang), lang)
	}

	assert.Equal(t,
		concat([]string{"-O2", "-g", "-fPIC", "-std=c++17", "-fno-rtti", "-Wno-class-memaccess"},
			tail),
		compileFlags(sampleConfig(), linuxToolchain(), noEnvFlags(), noPkgFlags(), "linux",
			langCXX))
}

func TestCompileFlagsDarwin(t *testing.T) {
	t.Parallel()

	tail := []string{"-I/tt/include", "-DBASE=1", "-DDARWIN", "-I/inc/base", "-I/inc/dar"}

	assert.Equal(t,
		concat([]string{
			"-O2", "-g", "-fPIC", "-std=gnu99", "-Werror", "-Wno-deprecated-declarations",
		}, tail),
		compileFlags(sampleConfig(), darwinToolchain(), noEnvFlags(), noPkgFlags(), "darwin",
			langC))
	assert.Equal(t,
		concat([]string{"-O2", "-g", "-fPIC", "-std=c++17", "-fno-rtti", "-stdlib=libc++"}, tail),
		compileFlags(sampleConfig(), darwinToolchain(), noEnvFlags(), noPkgFlags(), "darwin",
			langCXX))
}

// TestCompileFlagsUserFlagsComeLast pins where the flags of the environment
// go: CFLAGS after the project's cflags, CXXFLAGS after its cxxflags, so
// that the user's win a conflict, each in place of tt's -O2 -g of its
// language.
func TestCompileFlagsUserFlagsComeLast(t *testing.T) {
	t.Parallel()

	tail := []string{"-I/tt/include", "-DBASE=1", "-DLIN", "-I/inc/base", "-I/inc/lin"}

	assert.Equal(t,
		concat([]string{"-fPIC", "-std=gnu99", "-Werror", "-Wno-psabi", "-O0", "-g"}, tail),
		compileFlags(sampleConfig(), linuxToolchain(), userEnvFlags(), noPkgFlags(), "linux",
			langC))
	assert.Equal(t,
		concat([]string{"-fPIC", "-std=c++17", "-fno-rtti", "-Wno-class-memaccess", "-O1", "-g3"},
			tail),
		compileFlags(sampleConfig(), linuxToolchain(), userEnvFlags(), noPkgFlags(), "linux",
			langCXX))

	// CFLAGS alone set keeps the -O2 of C++, and the other way round.
	env := noEnvFlags()

	env.cflagsSet = true

	assert.Equal(t, []string{"-fPIC", "-I/tt/include"},
		compileFlags(moduleConfig("m.c"), linuxToolchain(), env, noPkgFlags(), "linux", langC))
	assert.Equal(t, []string{"-O2", "-g", "-fPIC", "-I/tt/include"},
		compileFlags(moduleConfig("m.c"), linuxToolchain(), env, noPkgFlags(), "linux", langCXX))

	env = noEnvFlags()
	env.cxxflagsSet = true

	assert.Equal(t, []string{"-O2", "-g", "-fPIC", "-I/tt/include"},
		compileFlags(moduleConfig("m.c"), linuxToolchain(), env, noPkgFlags(), "linux", langC))
	assert.Equal(t, []string{"-fPIC", "-I/tt/include"},
		compileFlags(moduleConfig("m.c"), linuxToolchain(), env, noPkgFlags(), "linux", langCXX))
}

// TestCompileFlagsWithoutIncludeDir pins that a toolchain without the
// headers of Tarantool gives no -I of them.
func TestCompileFlagsWithoutIncludeDir(t *testing.T) {
	t.Parallel()

	tools := linuxToolchain()

	tools.includeDir = ""

	assert.Equal(t, []string{"-O2", "-g", "-fPIC"},
		compileFlags(moduleConfig("m.c"), tools, noEnvFlags(), noPkgFlags(), "linux", langC))
}

// linux and darwin are the targets of the link tests.
var (
	linux  = build.Target{OS: "linux", Arch: "amd64"}
	darwin = build.Target{OS: "darwin", Arch: "arm64"}
)

func TestLinkArgsLinuxPlacement(t *testing.T) {
	t.Parallel()

	args := linkArgs(sampleConfig(), linuxToolchain(), noEnvFlags(),
		libsOf(sampleConfig(), linux.OS, noPkgFlags()), linux.OS, true, false,
		[]string{"/w/a.o", "/w/b.o"}, "/out/mymod.so")

	want := []string{
		// per-OS shared-link flag.
		"-shared",
		// the calls of the module to its own functions bound to them.
		"-Wl,-Bsymbolic-functions",
		// ldflags: base then linux overlay, before the objects.
		"-Wl,--as-needed", "-Wl,-z,defs",
		// output.
		"-o", "/out/mymod.so",
		// objects, in the order of the sources.
		"/w/a.o", "/w/b.o",
		// library dirs, no rpath of theirs, base then overlay.
		"-L/lib/base",
		"-L/lib/lin",
		// libraries: base order preserved, then overlay.
		"-lm", "-lz", "-llinlib",
		// rpath, base then overlay, ${ORIGIN} as $ORIGIN.
		"-Wl,-rpath,$ORIGIN", "-Wl,-rpath,/opt/lib", "-Wl,-rpath,$ORIGIN/lin",
	}
	assert.Equal(t, want, args)
}

func TestLinkArgsDarwinPlacement(t *testing.T) {
	t.Parallel()

	args := linkArgs(sampleConfig(), darwinToolchain(), noEnvFlags(),
		libsOf(sampleConfig(), darwin.OS, noPkgFlags()), darwin.OS, false, false,
		[]string{"/w/a.o", "/w/b.o"}, "/out/mymod.dylib")

	want := []string{
		// per-OS shared-link flag (bundle list on darwin).
		"-bundle", "-undefined", "dynamic_lookup", "-all_load",
		// ldflags: base then darwin overlay.
		"-Wl,--as-needed", "-Wl,-dead_strip",
		"-o", "/out/mymod.dylib",
		"/w/a.o", "/w/b.o",
		"-L/lib/base",
		"-L/lib/dar",
		"-lm", "-lz", "-ldarlib",
		// $ORIGIN is @loader_path on darwin.
		"-Wl,-rpath,@loader_path", "-Wl,-rpath,/opt/lib", "-Wl,-rpath,@loader_path/dar",
	}
	assert.Equal(t, want, args)
}

// TestLinkArgsUserFlagsComeLast pins that LDFLAGS of the environment ends the
// line and that no flag of a compiler reaches the link.
func TestLinkArgsUserFlagsComeLast(t *testing.T) {
	t.Parallel()

	args := linkArgs(sampleConfig(), linuxToolchain(), userEnvFlags(),
		libsOf(sampleConfig(), linux.OS, noPkgFlags()), linux.OS, true, false,
		[]string{"/w/a.o"}, "/out/mymod.so")

	assert.Equal(t, "-Wl,-z,now", args[len(args)-1])

	for _, flag := range []string{"-O2", "-O0", "-O1", "-fPIC", "-std=gnu99", "-std=c++17",
		"-I/tt/include", "-DBASE=1"} {
		assert.NotContains(t, args, flag)
	}
}

// TestLinkArgsStaticLibstdcxx pins that the C++ library is linked statically,
// its symbols hidden, into a module of C++ of Linux with static = true by
// any compiler but zig, and into no other.
func TestLinkArgsStaticLibstdcxx(t *testing.T) {
	t.Parallel()

	objects := []string{"/w/a.o"}

	static := linkArgs(moduleConfig("a.cpp"), linuxToolchain(), noEnvFlags(), noLibs(), linux.OS,
		true, true, objects, "/out/m.so")
	assert.Equal(t, []string{
		"-shared", "-Wl,-Bsymbolic-functions", "-static-libstdc++", "-Wl,--exclude-libs,ALL",
		"-o", "/out/m.so", "/w/a.o",
	}, static)
	assert.NotContains(t, linkArgs(moduleConfig("a.cpp"), linuxToolchain(), noEnvFlags(),
		noLibs(), linux.OS, true, false, objects, "/out/m.so"), "-static-libstdc++")

	gxx := []string{"c++"}

	assert.True(t, linksLibstdcxx(linux, true, true, gxx))
	assert.True(t, linksLibstdcxx(linux, true, true, []string{"ccache", "g++"}))

	for _, other := range []struct {
		target      build.Target
		static, cxx bool
		linker      []string
	}{
		{linux, false, true, gxx},
		{linux, true, false, gxx},
		{darwin, true, true, gxx},
		{linux, true, true, []string{"zig", "c++"}},
		{linux, true, true, []string{"/opt/zig-0.17.1/zig", "c++", "-target", "x86_64-linux"}},
		{linux, true, true, []string{"ccache", "zig", "c++"}},
	} {
		assert.False(t, linksLibstdcxx(other.target, other.static, other.cxx, other.linker),
			other)
	}
}

// TestBindsLocally pins that a module of Linux is linked with
// -Bsymbolic-functions by any compiler but zig, which refuses it, and a
// module of macOS is not.
func TestBindsLocally(t *testing.T) {
	t.Parallel()

	assert.True(t, bindsLocally(linux, []string{"cc"}))
	assert.True(t, bindsLocally(linux, []string{"ccache", "clang++"}))
	assert.False(t, bindsLocally(linux, []string{"zig", "cc"}))
	assert.False(t, bindsLocally(linux, []string{"ccache", "zig", "c++"}))
	assert.False(t, bindsLocally(darwin, []string{"cc"}))
}

// TestIsZig pins how zig cc and zig c++ are told from the words of the
// compiler.
func TestIsZig(t *testing.T) {
	t.Parallel()

	for _, words := range [][]string{
		{"zig", "cc"}, {"zig", "c++"}, {"/home/me/.local/zig/0.17.1/zig", "c++"},
		{"ccache", "zig", "cc"}, {"zig.exe", "cc"}, {"zig", "cc", "-target", "aarch64-linux"},
	} {
		assert.True(t, isZig(words), words)
	}

	for _, words := range [][]string{
		nil, {"zig"}, {"cc"}, {"clang++"}, {"zig", "build"}, {"zigcc"}, {"zig-cc", "cc"},
		{"cc", "zig"}, {"/usr/bin/zig/cc"},
	} {
		assert.False(t, isZig(words), words)
	}
}

// TestCompileStepArgs pins the argv of a compile: the words of the compiler,
// the flags, the list of the files it reads, the source and the output.
func TestCompileStepArgs(t *testing.T) {
	t.Parallel()

	step := compileStep{
		source: "src/a.c", lang: langC, object: "/w/obj/a.c.1.o",
		compiler: []string{"ccache", "cc"}, flags: []string{"-O2", "-g", "-fPIC"},
	}

	assert.Equal(t, []string{
		"ccache", "cc", "-O2", "-g", "-fPIC", "-MD", "-MF", "/w/obj/a.c.1.o.d",
		"-MQ", "/w/obj/a.c.1.o", "-c", "src/a.c", "-o", "/w/obj/a.c.1.o.tmp",
	}, step.args("/w/obj/a.c.1.o.tmp"))
}

// TestPlanOf pins the plan of a build: a compile a source with the compiler
// and the flags of its language, and the link with CXX when a source is
// C++, else with CC, of the objects in the order of the sources.
func TestPlanOf(t *testing.T) {
	t.Parallel()

	tools := linuxToolchain()

	tools.cc = []string{"zig", "cc"}
	tools.cxx = []string{"zig", "c++"}

	context := func(static bool) *build.Context {
		return build.NewContext(build.Context{
			Target: linux,
			Dirs:   build.Dirs{Work: "/w", Modules: "/lib", Cwd: "/src"},
			Static: static,
		}, nil)
	}

	config := moduleConfig("a.c", "b.cpp", "c.S")

	config.Cflags = []string{"-std=c11"}
	config.Cxxflags = []string{"-std=c++17"}

	built := planOf(config, tools, noEnvFlags(), noPkgFlags(), noLibs(), context(true))

	require.Len(t, built.steps, 3)
	assert.True(t, built.cxx)
	assert.Equal(t, "/lib/m.so", built.out)

	objects := make([]string, 0, len(built.steps))

	for index, want := range []struct {
		lang     language
		compiler []string
		flag     string
	}{
		{langC, tools.cc, "-std=c11"},
		{langCXX, tools.cxx, "-std=c++17"},
		{langAsmCpp, tools.cc, "-std=c11"},
	} {
		step := built.steps[index]

		assert.Equal(t, config.Sources[index], step.source)
		assert.Equal(t, want.lang, step.lang, step.source)
		assert.Equal(t, want.compiler, step.compiler, step.source)
		assert.Contains(t, step.flags, want.flag, step.source)
		assert.Equal(t, "/w/obj/"+objectName(step.source), step.object)

		objects = append(objects, step.object)
	}

	// zig c++ links its libc++ in itself, and refuses --exclude-libs and
	// -Bsymbolic-functions.
	assert.Equal(t, concat([]string{"zig", "c++", "-shared", "-o", "/lib/m.so"}, objects),
		built.link)

	gcc := linuxToolchain()
	gxx := planOf(config, gcc, noEnvFlags(), noPkgFlags(), noLibs(), context(true))

	assert.Equal(t, concat([]string{"c++", "-shared", "-Wl,-Bsymbolic-functions",
		"-static-libstdc++", "-Wl,--exclude-libs,ALL", "-o", "/lib/m.so"}, objects), gxx.link)

	withoutCXX := planOf(moduleConfig("a.c", "b.s"), tools, noEnvFlags(), noPkgFlags(), noLibs(),
		context(true))
	assert.False(t, withoutCXX.cxx)
	assert.Equal(t, []string{"zig", "cc", "-shared", "-o", "/lib/m.so"}, withoutCXX.link[:5])
}

// TestEnvironFlags pins how CFLAGS, CXXFLAGS and LDFLAGS are read: split on
// blanks, CFLAGS and CXXFLAGS set even when empty.
func TestEnvironFlags(t *testing.T) {
	t.Parallel()

	lookup := func(environ map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			value, ok := environ[key]

			return value, ok
		}
	}

	unset := environFlags(lookup(nil))
	assert.False(t, unset.cflagsSet)
	assert.False(t, unset.cxxflagsSet)
	assert.Empty(t, unset.cflags)
	assert.Empty(t, unset.cxxflags)
	assert.Empty(t, unset.ldflags)

	empty := environFlags(lookup(map[string]string{"CFLAGS": "", "CXXFLAGS": ""}))
	assert.True(t, empty.cflagsSet)
	assert.True(t, empty.cxxflagsSet)
	assert.Empty(t, empty.cflags)

	assert.Equal(t, envFlags{
		cflagsSet:   true,
		cflags:      []string{"-O0", "-g"},
		cxxflagsSet: true,
		cxxflags:    []string{"-O1"},
		ldflags:     []string{"-Wl,--as-needed", "-L/opt/lib"},
	}, environFlags(lookup(map[string]string{
		"CFLAGS":   " -O0\t-g ",
		"CXXFLAGS": "-O1",
		"LDFLAGS":  "-Wl,--as-needed  -L/opt/lib",
	})))
}

// TestCompileAll checks the scheduler of the compiles: no more than jobs at
// once, every step run when none fails, and the first failure stopping the
// others and being the error.
func TestCompileAll(t *testing.T) {
	t.Parallel()

	const count = 20

	steps := make([]compileStep, count)
	for index := range steps {
		steps[index].source = string(rune('a'+index)) + ".c"
	}

	var (
		running, peak atomic.Int32
		// ranMu guards ran.
		ranMu sync.Mutex
		ran   []string
	)

	err := compileAll(t.Context(), steps, 3, func(_ context.Context, step compileStep) error {
		now := running.Add(1)
		defer running.Add(-1)

		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}

		ranMu.Lock()
		defer ranMu.Unlock()

		ran = append(ran, step.source)

		return nil
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, peak.Load(), int32(3))
	assert.Len(t, ran, len(steps))

	errBroken := errors.New("broken")

	var started atomic.Int32

	err = compileAll(t.Context(), steps, 2, func(ctx context.Context, step compileStep) error {
		started.Add(1)

		if step.source == "a.c" {
			return errBroken
		}

		// The others wait to be stopped.
		<-ctx.Done()

		return ctx.Err()
	})
	require.ErrorIs(t, err, errBroken)
	assert.Less(t, started.Load(), int32(count), "no compile starts after a failure")

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	err = compileAll(cancelled, steps, 2, func(context.Context, compileStep) error {
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestOrList(t *testing.T) {
	t.Parallel()

	assert.Empty(t, orList(nil))
	assert.Equal(t, "linux", orList([]string{"linux"}))
	assert.Equal(t, "linux or darwin", orList([]string{"linux", "darwin"}))
	assert.Equal(t, "a, b or c", orList([]string{"a", "b", "c"}))
}
