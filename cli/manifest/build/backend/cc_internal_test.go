package backend

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	lrbuild "github.com/tarantool/go-luarocks/build"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// indexOf returns the index of the first occurrence of tok in args, or -1.
func indexOf(args []string, tok string) int {
	for i, a := range args {
		if a == tok {
			return i
		}
	}

	return -1
}

// linuxFlags is a hand-built lrbuild.Flags fixture mirroring flags.go for
// linux: -shared LIBFLAG and GccRpath on, so placement can be asserted without
// depending on the host OS. Its CFLAGS and LDFLAGS, which the cc driver does
// not take, are those of an environment the argv must not show.
func linuxFlags() lrbuild.Flags {
	return lrbuild.Flags{
		CC:        "cc",
		CFLAGS:    []string{"-Ostale", "-fPIC", "-I/tt/include"},
		LDFLAGS:   []string{"-Wl,--stale"},
		LIBFLAG:   []string{"-shared"},
		LuaIncDir: "/tt/include",
		Ext:       ".so",
		GccRpath:  true,
	}
}

// darwinFlags is a hand-built lrbuild.Flags fixture mirroring flags.go for
// darwin: the -bundle LIBFLAG list and GccRpath off.
func darwinFlags() lrbuild.Flags {
	return lrbuild.Flags{
		CC:        "cc",
		CFLAGS:    []string{"-Ostale", "-fPIC", "-I/tt/include"},
		LDFLAGS:   []string{"-Wl,--stale"},
		LIBFLAG:   []string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"},
		LuaIncDir: "/tt/include",
		Ext:       ".so",
		GccRpath:  false,
	}
}

// noEnvFlags is an environment with neither CFLAGS nor LDFLAGS set.
func noEnvFlags() envFlags {
	return envFlags{}
}

// userEnvFlags is an environment with CFLAGS and LDFLAGS set.
func userEnvFlags() envFlags {
	return envFlags{
		cflagsSet: true,
		cflags:    []string{"-O0", "-g"},
		ldflags:   []string{"-Wl,-z,now"},
	}
}

// sampleBuild carries base lists plus distinct linux/darwin overlays so the
// overlay-selection and append-only-merge behavior is observable.
func sampleBuild() manifest.Build {
	return manifest.Build{
		Backend:     BackendC,
		Module:      "mymod",
		Sources:     []string{"a.c", "b.c"},
		Defines:     []string{"BASE=1"},
		IncludeDirs: []string{"/inc/base"},
		LibraryDirs: []string{"/lib/base"},
		Libraries:   []string{"m", "z"},
		Cflags:      []string{"-std=gnu99", "-Werror"},
		Ldflags:     []string{"-Wl,--as-needed"},
		Platforms: map[string]manifest.BuildOverlay{
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

func TestCcArgsLinuxPlacement(t *testing.T) {
	t.Parallel()

	args := ccArgs(sampleBuild(), linuxFlags(), noEnvFlags(), "/out/mymod.so", "linux")

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
		// library dirs with interleaved rpath (GccRpath=true), base then overlay.
		"-L/lib/base", "-Wl,-rpath,/lib/base",
		"-L/lib/lin", "-Wl,-rpath,/lib/lin",
		// libraries: base order preserved, then overlay.
		"-lm", "-lz", "-llinlib",
	}
	assert.Equal(t, want, args)
}

func TestCcArgsDarwinPlacement(t *testing.T) {
	t.Parallel()

	args := ccArgs(sampleBuild(), darwinFlags(), noEnvFlags(), "/out/mymod.so", "darwin")

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
		"-o", "/out/mymod.so",
		// sources.
		"a.c", "b.c",
		// library dirs, no rpath (GccRpath=false on darwin), base then overlay.
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

	args := ccArgs(sampleBuild(), linuxFlags(), userEnvFlags(), "/out/mymod.so", "linux")

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

	b := manifest.Build{Backend: BackendC, Module: "m", Sources: []string{"m.c"}}
	args := ccArgs(b, linuxFlags(), envFlags{cflagsSet: true}, "/out/m.so", "linux")

	assert.Equal(t, []string{
		"-fPIC", "-I/tt/include", "-shared", "-o", "/out/m.so", "m.c",
	}, args)
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

	b := manifest.Build{Backend: BackendC, Module: "solo", Sources: []string{"solo.c"}}
	args := ccArgs(b, linuxFlags(), noEnvFlags(), "/out/solo.so", "linux")

	// -o <out> immediately precedes the single source.
	i := indexOf(args, "-o")
	require.GreaterOrEqual(t, i, 0)
	require.Less(t, i+2, len(args))
	assert.Equal(t, "/out/solo.so", args[i+1])
	assert.Equal(t, "solo.c", args[i+2])
}

func TestCcArgsMultiSourceOrder(t *testing.T) {
	t.Parallel()

	b := manifest.Build{
		Backend: BackendC,
		Module:  "multi",
		Sources: []string{"one.c", "two.c", "three.c"},
	}
	args := ccArgs(b, linuxFlags(), noEnvFlags(), "/out/multi.so", "linux")

	i := indexOf(args, "-o")
	require.GreaterOrEqual(t, i, 0)
	require.Less(t, i+4, len(args))
	// All sources follow -o <out> in the given order.
	assert.Equal(t, []string{"one.c", "two.c", "three.c"}, args[i+2:i+5])
}

func TestCcArgsHostParityWithDeriveFlags(t *testing.T) {
	t.Parallel()

	cfg := luarocks.Config{}

	cfg.Tarantool.IncludeDir = "/opt/tt/include/tarantool"

	flags := lrbuild.DeriveFlags(cfg)

	b := manifest.Build{Backend: BackendC, Module: "m", Sources: []string{"m.c"}}
	args := ccArgs(b, flags, noEnvFlags(), "/out/m.so", runtime.GOOS)

	// tt's defaults lead, then the headers of the include dir go-luarocks
	// was configured with.
	assert.Equal(t, []string{"-O2", "-fPIC", "-I/opt/tt/include/tarantool"}, args[:3])

	// The derived LIBFLAG appears immediately before "-o".
	oi := indexOf(args, "-o")
	require.GreaterOrEqual(t, oi, len(flags.LIBFLAG))
	assert.Equal(t, flags.LIBFLAG, args[oi-len(flags.LIBFLAG):oi])

	// The artifact extension is the derived one, not a hardcoded ".so".
	assert.Equal(t, ".so", flags.Ext)
}

func TestArtifactName(t *testing.T) {
	t.Parallel()

	// The extension comes from flags.Ext, so a non-".so" ext must be honored;
	// a regression hardcoding ".so" would fail this.
	assert.Equal(t, "fast_hash.dylib", artifactName("fast_hash", ".dylib"))
	assert.Equal(t, "m.so", artifactName("m", ".so"))
	// A dotted module stays a flat leaf name (not slashed into subdirectories).
	assert.Equal(t, "a.b.so", artifactName("a.b", ".so"))
}

func TestModuleExt(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ".so", moduleExt("linux", ".so"))
	// Tarantool looks for ?.dylib on macOS, whatever the toolchain says.
	assert.Equal(t, ".dylib", moduleExt("darwin", ".so"))
}

func TestCcMissingHeadersPreflight(t *testing.T) {
	t.Parallel()

	// A CC that would fail loudly if invoked, proving the preflight returns
	// before exec: LuaIncDir is empty, so ErrMissingHeaders comes back instead
	// of a run failure from the bogus compiler.
	be := ccBackend{flags: lrbuild.Flags{CC: "/nonexistent/cc", Ext: ".so"}}

	b := manifest.Build{Backend: BackendC, Module: "m", Sources: []string{"m.c"}}
	err := be.Run(context.Background(), b, t.TempDir(), Env{OutputDir: t.TempDir()})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingHeaders)
}

func TestCcRejectsRelativePaths(t *testing.T) {
	t.Parallel()

	be := ccBackend{flags: linuxFlags()}
	b := manifest.Build{Backend: BackendC, Module: "m", Sources: []string{"m.c"}}

	err := be.Run(context.Background(), b, "rel/cwd", Env{OutputDir: t.TempDir()})
	require.Error(t, err)
}
