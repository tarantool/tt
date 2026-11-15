package cc

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

// linuxToolchain is the toolchain of a build for linux, as toolchainOf
// derives it there: -shared and an rpath for every library directory, so
// that placement can be asserted without depending on the host OS.
func linuxToolchain() toolchain {
	return toolchain{
		cc: "cc", libFlag: []string{"-shared"}, rpath: true, includeDir: "/tt/include",
	}
}

// darwinToolchain is the toolchain of a build for darwin: the -bundle flags
// and no rpath.
func darwinToolchain() toolchain {
	return toolchain{
		cc:         "cc",
		libFlag:    []string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"},
		rpath:      false,
		includeDir: "/tt/include",
	}
}

// TestToolchainOf pins the toolchain of a build: CC of the environment, cc
// without one; the flags of the target; the headers under the prefix of the
// Tarantool of the build, whether or not module.h is there, and none
// without a prefix.
func TestToolchainOf(t *testing.T) {
	t.Parallel()

	noEnv := func(string) (string, bool) { return "", false }
	clang := func(key string) (string, bool) { return "clang", key == "CC" }

	linux := build.NewContext(build.Context{
		Target:    build.Target{OS: "linux", Arch: "amd64"},
		Tarantool: build.Tarantool{Prefix: "/tt", IncludeDir: "", Version: ""},
	}, nil)

	want := linuxToolchain()

	want.includeDir = "/tt/include/tarantool"

	assert.Equal(t, want, toolchainOf(linux, func(string) (string, bool) { return "", true }))

	want.cc = "clang"

	assert.Equal(t, want, toolchainOf(linux, clang))

	darwin := build.NewContext(build.Context{
		Target:    build.Target{OS: "darwin", Arch: "arm64"},
		Tarantool: build.Tarantool{Prefix: "", IncludeDir: "", Version: ""},
	}, nil)

	want = darwinToolchain()
	want.includeDir = ""

	assert.Equal(t, want, toolchainOf(darwin, noEnv))
}

// noEnvFlags is an environment with neither CFLAGS nor LDFLAGS set.
func noEnvFlags() envFlags {
	return envFlags{cflagsSet: false, cflags: nil, ldflags: nil}
}

// userEnvFlags is an environment with CFLAGS and LDFLAGS set.
func userEnvFlags() envFlags {
	return envFlags{
		cflagsSet: true,
		cflags:    []string{"-O0", "-g"},
		ldflags:   []string{"-Wl,-z,now"},
	}
}

// sampleConfig carries base lists plus distinct linux/darwin overlays so the
// overlay-selection and append-only-merge behavior is observable.
func sampleConfig() Config {
	return Config{
		Module:      "mymod",
		Sources:     []string{"a.c", "b.c"},
		Defines:     []string{"BASE=1"},
		IncludeDirs: []string{"/inc/base"},
		LibraryDirs: []string{"/lib/base"},
		Libraries:   []string{"m", "z"},
		Cflags:      []string{"-std=gnu99", "-Werror"},
		Ldflags:     []string{"-Wl,--as-needed"},
		Platforms: map[string]Overlay{
			"linux": {
				Defines:     []string{"LIN"},
				IncludeDirs: []string{"/inc/lin"},
				LibraryDirs: []string{"/lib/lin"},
				Libraries:   []string{"linlib"},
				Cflags:      []string{"-Wno-psabi"},
				Ldflags:     []string{"-Wl,-z,defs"},
			},
			"darwin": {
				Defines:     []string{"DARW"},
				IncludeDirs: []string{"/inc/dar"},
				LibraryDirs: []string{"/lib/dar"},
				Libraries:   []string{"darlib"},
				Cflags:      []string{"-Wno-deprecated-declarations"},
				Ldflags:     []string{"-Wl,-dead_strip"},
			},
		},
	}
}

// moduleConfig is the least config of a module m of m.c, with sources.
func moduleConfig(sources ...string) Config {
	return Config{
		Module: "m", Sources: sources, IncludeDirs: nil, Libraries: nil, LibraryDirs: nil,
		Defines: nil, Cflags: nil, Ldflags: nil, Platforms: nil,
	}
}

func TestCcArgsLinuxPlacement(t *testing.T) {
	t.Parallel()

	args := ccArgs(sampleConfig(), linuxToolchain(), noEnvFlags(), "/out/mymod.so", "linux")

	want := []string{
		// tt's defaults: -O2, CFLAGS being unset, and -fPIC.
		"-O2", "-fPIC",
		// cflags: base then linux overlay.
		"-std=gnu99", "-Werror", "-Wno-psabi",
		// the headers of Tarantool.
		"-I/tt/include",
		// defines: base then linux overlay.
		"-DBASE=1", "-DLIN",
		// include dirs: base then linux overlay.
		"-I/inc/base", "-I/inc/lin",
		// per-OS shared-link flag.
		"-shared",
		// ldflags: base then linux overlay, before the sources.
		"-Wl,--as-needed", "-Wl,-z,defs",
		// output.
		"-o", "/out/mymod.so",
		// sources, emitted verbatim.
		"a.c", "b.c",
		// library dirs with interleaved rpath, base then overlay.
		"-L/lib/base", "-Wl,-rpath,/lib/base",
		"-L/lib/lin", "-Wl,-rpath,/lib/lin",
		// libraries: base order preserved, then overlay.
		"-lm", "-lz", "-llinlib",
	}
	assert.Equal(t, want, args)
}

func TestCcArgsDarwinPlacement(t *testing.T) {
	t.Parallel()

	args := ccArgs(sampleConfig(), darwinToolchain(), noEnvFlags(), "/out/mymod.dylib", "darwin")

	want := []string{
		// tt's defaults.
		"-O2", "-fPIC",
		// cflags: base then darwin overlay.
		"-std=gnu99", "-Werror", "-Wno-deprecated-declarations",
		// the headers of Tarantool.
		"-I/tt/include",
		// defines: base then darwin overlay.
		"-DBASE=1", "-DDARW",
		// include dirs: base then darwin overlay.
		"-I/inc/base", "-I/inc/dar",
		// per-OS shared-link flag (bundle list on darwin).
		"-bundle", "-undefined", "dynamic_lookup", "-all_load",
		// ldflags: base then darwin overlay.
		"-Wl,--as-needed", "-Wl,-dead_strip",
		// output.
		"-o", "/out/mymod.dylib",
		// sources.
		"a.c", "b.c",
		// library dirs, no rpath on darwin, base then overlay.
		"-L/lib/base",
		"-L/lib/dar",
		// libraries: base order preserved, then overlay.
		"-lm", "-lz", "-ldarlib",
	}
	assert.Equal(t, want, args)
}

// TestCcArgsUserFlagsComeLast pins where the flags of the environment go:
// CFLAGS after the project's cflags, so that the user's win a conflict, and
// in place of tt's -O2; LDFLAGS at the end of the line.
func TestCcArgsUserFlagsComeLast(t *testing.T) {
	t.Parallel()

	args := ccArgs(sampleConfig(), linuxToolchain(), userEnvFlags(), "/out/mymod.so", "linux")

	want := []string{
		// No -O2: CFLAGS is set.
		"-fPIC",
		"-std=gnu99", "-Werror", "-Wno-psabi",
		// CFLAGS of the environment, after the project's.
		"-O0", "-g",
		"-I/tt/include",
		"-DBASE=1", "-DLIN",
		"-I/inc/base", "-I/inc/lin",
		"-shared",
		"-Wl,--as-needed", "-Wl,-z,defs",
		"-o", "/out/mymod.so",
		"a.c", "b.c",
		"-L/lib/base", "-Wl,-rpath,/lib/base",
		"-L/lib/lin", "-Wl,-rpath,/lib/lin",
		"-lm", "-lz", "-llinlib",
		// LDFLAGS of the environment, last.
		"-Wl,-z,now",
	}
	assert.Equal(t, want, args)
}

// TestCcArgsEmptyCFLAGSDropsTheDefault pins that CFLAGS set to nothing
// leaves tt's -O2 out, as autoconf leaves out its -g -O2.
func TestCcArgsEmptyCFLAGSDropsTheDefault(t *testing.T) {
	t.Parallel()

	args := ccArgs(moduleConfig("m.c"), linuxToolchain(),
		envFlags{cflagsSet: true, cflags: nil, ldflags: nil}, "/out/m.so", "linux")

	assert.Equal(t, []string{
		"-fPIC", "-I/tt/include", "-shared", "-o", "/out/m.so", "m.c",
	}, args)
}

// TestCcArgsWithoutIncludeDir pins that a toolchain without the headers of
// Tarantool gives no -I of them.
func TestCcArgsWithoutIncludeDir(t *testing.T) {
	t.Parallel()

	tools := linuxToolchain()

	tools.includeDir = ""

	args := ccArgs(moduleConfig("m.c"), tools, noEnvFlags(), "/out/m.so", "linux")
	assert.Equal(t, []string{"-O2", "-fPIC", "-shared", "-o", "/out/m.so", "m.c"}, args)
}

// TestEnvironFlags pins how CFLAGS and LDFLAGS are read: split on blanks,
// CFLAGS set even when empty.
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
	assert.Empty(t, unset.cflags)
	assert.Empty(t, unset.ldflags)

	empty := environFlags(lookup(map[string]string{"CFLAGS": ""}))
	assert.True(t, empty.cflagsSet)
	assert.Empty(t, empty.cflags)

	assert.Equal(t, envFlags{
		cflagsSet: true,
		cflags:    []string{"-O0", "-g"},
		ldflags:   []string{"-Wl,--as-needed", "-L/opt/lib"},
	}, environFlags(lookup(map[string]string{
		"CFLAGS":  " -O0\t-g ",
		"LDFLAGS": "-Wl,--as-needed  -L/opt/lib",
	})))
}

func TestCcArgsSingleSource(t *testing.T) {
	t.Parallel()

	args := ccArgs(moduleConfig("solo.c"), linuxToolchain(), noEnvFlags(), "/out/solo.so",
		"linux")

	// -o <out> immediately precedes the single source.
	i := slices.Index(args, "-o")
	require.GreaterOrEqual(t, i, 0)
	require.Less(t, i+2, len(args))
	assert.Equal(t, "/out/solo.so", args[i+1])
	assert.Equal(t, "solo.c", args[i+2])
}

func TestCcArgsMultiSourceOrder(t *testing.T) {
	t.Parallel()

	args := ccArgs(moduleConfig("one.c", "two.c", "three.c"), linuxToolchain(), noEnvFlags(),
		"/out/multi.so", "linux")

	i := slices.Index(args, "-o")
	require.GreaterOrEqual(t, i, 0)
	require.Less(t, i+4, len(args))
	// All sources follow -o <out> in the given order.
	assert.Equal(t, []string{"one.c", "two.c", "three.c"}, args[i+2:i+5])
}

func TestOrList(t *testing.T) {
	t.Parallel()

	assert.Empty(t, orList(nil))
	assert.Equal(t, "linux", orList([]string{"linux"}))
	assert.Equal(t, "linux or darwin", orList([]string{"linux", "darwin"}))
	assert.Equal(t, "a, b or c", orList([]string{"a", "b", "c"}))
}
