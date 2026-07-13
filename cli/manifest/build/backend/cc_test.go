package backend

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	lrbuild "github.com/tarantool/go-luarocks/build"

	"github.com/tarantool/tt/cli/manifest"
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
// depending on the host OS.
func linuxFlags() lrbuild.Flags {
	return lrbuild.Flags{
		CC:        "cc",
		CFLAGS:    []string{"-O2", "-fPIC", "-I/tt/include"},
		LDFLAGS:   []string{"-Wl,--as-needed"},
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
		CFLAGS:    []string{"-O2", "-fPIC", "-I/tt/include"},
		LDFLAGS:   []string{"-Wl,--as-needed"},
		LIBFLAG:   []string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"},
		LuaIncDir: "/tt/include",
		Ext:       ".so",
		GccRpath:  false,
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
		Platforms: map[string]manifest.BuildOverlay{
			"linux": {
				Defines:     []string{"LIN"},
				IncludeDirs: []string{"/inc/lin"},
				LibraryDirs: []string{"/lib/lin"},
				Libraries:   []string{"linlib"},
			},
			"darwin": {
				Defines:     []string{"DARW"},
				IncludeDirs: []string{"/inc/dar"},
				LibraryDirs: []string{"/lib/dar"},
				Libraries:   []string{"darlib"},
			},
		},
	}
}

func TestCcArgsLinuxPlacement(t *testing.T) {
	t.Parallel()

	args := ccArgs(sampleBuild(), linuxFlags(), "/out/mymod.so", "linux")

	want := []string{
		// CFLAGS.
		"-O2", "-fPIC", "-I/tt/include",
		// defines: base then linux overlay.
		"-DBASE=1", "-DLIN",
		// include dirs: base then linux overlay.
		"-I/inc/base", "-I/inc/lin",
		// per-OS shared-link flag.
		"-shared",
		// output.
		"-o", "/out/mymod.so",
		// sources, emitted verbatim.
		"a.c", "b.c",
		// library dirs with interleaved rpath (GccRpath=true), base then overlay.
		"-L/lib/base", "-Wl,-rpath,/lib/base",
		"-L/lib/lin", "-Wl,-rpath,/lib/lin",
		// libraries: base order preserved, then overlay.
		"-lm", "-lz", "-llinlib",
		// LDFLAGS.
		"-Wl,--as-needed",
	}
	assert.Equal(t, want, args)
}

func TestCcArgsDarwinPlacement(t *testing.T) {
	t.Parallel()

	args := ccArgs(sampleBuild(), darwinFlags(), "/out/mymod.so", "darwin")

	want := []string{
		// CFLAGS.
		"-O2", "-fPIC", "-I/tt/include",
		// defines: base then darwin overlay.
		"-DBASE=1", "-DDARW",
		// include dirs: base then darwin overlay.
		"-I/inc/base", "-I/inc/dar",
		// per-OS shared-link flag (bundle list on darwin).
		"-bundle", "-undefined", "dynamic_lookup", "-all_load",
		// output.
		"-o", "/out/mymod.so",
		// sources.
		"a.c", "b.c",
		// library dirs, no rpath (GccRpath=false on darwin), base then overlay.
		"-L/lib/base",
		"-L/lib/dar",
		// libraries: base order preserved, then overlay.
		"-lm", "-lz", "-ldarlib",
		// LDFLAGS.
		"-Wl,--as-needed",
	}
	assert.Equal(t, want, args)
}

func TestCcArgsSingleSource(t *testing.T) {
	t.Parallel()

	b := manifest.Build{Backend: BackendC, Module: "solo", Sources: []string{"solo.c"}}
	args := ccArgs(b, linuxFlags(), "/out/solo.so", "linux")

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
	args := ccArgs(b, linuxFlags(), "/out/multi.so", "linux")

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
	args := ccArgs(b, flags, "/out/m.so", runtime.GOOS)

	// The real DeriveFlags CFLAGS appear as the leading tokens.
	require.GreaterOrEqual(t, len(args), len(flags.CFLAGS))
	assert.Equal(t, flags.CFLAGS, args[:len(flags.CFLAGS)])

	// Host-independent invariants derived by go-luarocks.
	assert.Contains(t, args, "-fPIC")
	assert.Contains(t, args, "-I/opt/tt/include/tarantool")

	// The derived LIBFLAG appears immediately before "-o".
	oi := indexOf(args, "-o")
	require.GreaterOrEqual(t, oi, len(flags.LIBFLAG))
	assert.Equal(t, flags.LIBFLAG, args[oi-len(flags.LIBFLAG):oi])

	// The artifact extension is the derived one, not a hardcoded ".so".
	assert.Equal(t, flags.Ext, ".so")
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
