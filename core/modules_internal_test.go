package core

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/standard"
)

// testConstructor is a constructor of this package defined by name.
func testConstructor(sdk.Services) []sdk.Mount { return nil }

// TestConstructorPackage checks that the package of a constructor is the
// import path the runtime names it by, for a function and a closure alike.
func TestConstructorPackage(t *testing.T) {
	t.Parallel()

	closure := func(sdk.Services) []sdk.Mount { return nil }

	assert.Equal(t, "github.com/tarantool/tt/v3/core", constructorPackage(Builtin))
	assert.Equal(t, "github.com/tarantool/tt/v3/core", constructorPackage(testConstructor))
	assert.Equal(t, "github.com/tarantool/tt/v3/core", constructorPackage(closure))
	assert.Empty(t, constructorPackage(nil))
}

// TestModuleTable checks that the table tt version --modules reads lists
// the modules by name with the packages of their constructors.
func TestModuleTable(t *testing.T) {
	t.Parallel()

	got := moduleTable(Modules{
		"zeta": testConstructor, "builtin": Builtin, "none": nil,
		"backends": testModuleConstructor, "text": "not a constructor",
	})

	assert.Equal(t, []version.Module{
		{Name: "backends", Package: "github.com/tarantool/tt/v3/core"},
		{Name: "builtin", Package: "github.com/tarantool/tt/v3/core"},
		{Name: "none", Package: ""},
		{Name: "text", Package: ""},
		{Name: "zeta", Package: "github.com/tarantool/tt/v3/core"},
	}, got)
}

// testBackend is a backend a module of the tests provides.
func testBackend(name string) sdkbuild.Backend {
	return sdkbuild.Backend{
		Name:     name,
		Produces: []sdkbuild.Result{sdkbuild.Module},
		Config:   func() any { return new(struct{}) },
		New: func(any, sdkbuild.Component, sdkbuild.Common) (sdkbuild.Build, error) {
			return nil, nil
		},
		Incremental: false,
		OutputFrom:  sdkbuild.OutputNone,
		Exclude:     nil,
	}
}

// testModuleConstructor is a module constructor of this package with a
// command, a backend and a file check.
func testModuleConstructor(sdk.Services) sdk.Module {
	return sdk.Module{
		Commands: []sdk.Mount{{Path: "", Cmd: &cobra.Command{Use: "demo"}}},
		Backends: []sdkbuild.Backend{testBackend("demo")},
		FileChecks: []sdkbuild.FileCheck{{
			Suffix: ".demo",
			Check:  func(context.Context, string) (string, error) { return "", nil },
		}},
	}
}

// TestModuleConstructor checks the constructors the core calls, and that it
// refuses a module without one and one of another type.
func TestModuleConstructor(t *testing.T) {
	t.Parallel()

	mounts := []sdk.Mount{{Path: "", Cmd: &cobra.Command{Use: "demo"}}}
	module := sdk.Module{Commands: mounts, Backends: []sdkbuild.Backend{}, FileChecks: nil}

	commands := func(sdk.Services) []sdk.Mount { return mounts }
	everything := func(sdk.Services) sdk.Module { return module }

	for name, ctor := range map[string]any{
		"a Constructor":           sdk.Constructor(commands),
		"a function of one":       commands,
		"a ModuleConstructor":     sdk.ModuleConstructor(everything),
		"a function of the other": everything,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			construct, err := moduleConstructor("demo", ctor)
			require.NoError(t, err)
			assert.Equal(t, mounts, construct(nil).Commands)
		})
	}

	for name, ctor := range map[string]any{
		"nil":                 nil,
		"a nil Constructor":   sdk.Constructor(nil),
		"a nil function":      (func(sdk.Services) sdk.Module)(nil),
		"a string":            "demo.New",
		"another function":    func() []sdk.Mount { return nil },
		"a pointer to a func": &mounts,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := moduleConstructor("demo", ctor)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `module "demo": `)

			if ctor == nil || name != "a string" && name != "another function" &&
				name != "a pointer to a func" {
				require.ErrorIs(t, err, errNoConstructor)
			} else {
				require.ErrorIs(t, err, errConstructorType)
			}
		})
	}
}

// TestConstruct checks that construct registers the backends and the file
// checks of the modules, and reports, with the module, those it refuses and
// a constructor of another type, having called every constructor.
func TestConstruct(t *testing.T) {
	t.Parallel()

	ready := &atomic.Bool{}
	noVersion := func(bool, bool) string { return "" }

	entries, registry, err := construct(Modules{
		"backends": testModuleConstructor, "commands": testConstructor,
	}, ready, noVersion)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "backends", entries[0].Module)
	assert.Equal(t, []string{"demo"}, registry.Names())
	assert.Len(t, registry.FileChecks(), 1)

	_, module, ok := registry.Backend("demo")
	assert.True(t, ok)
	assert.Equal(t, "backends", module)

	_, registry, err = construct(Modules{
		"backends": testModuleConstructor,
		"again":    func(sdk.Services) sdk.Module { return testModuleConstructor(nil) },
		"text":     "not a constructor",
	}, ready, noVersion)
	require.ErrorIs(t, err, buildreg.ErrBackend)
	require.ErrorIs(t, err, errConstructorType)
	assert.Contains(t, err.Error(), `module "backends": invalid build backend "demo": `+
		`module "again" provides one of the name too`)
	assert.Equal(t, []string{"demo"}, registry.Names())

	// A module cannot take the name of a backend of a standard module.
	modules := Modules(standard.Modules())

	modules["make"] = func(sdk.Services) sdk.Module {
		return sdk.Module{Commands: nil, Backends: []sdkbuild.Backend{testBackend("make")},
			FileChecks: nil}
	}

	_, _, err = construct(modules, ready, noVersion)
	require.ErrorContains(t, err, `module "make": invalid build backend "make": `+
		`module "build" provides one of the name too`)
}

// TestStandardBackends checks that the core has no build backend of its
// own, and that the standard modules give tt its backends: those of the
// module build, make and shell first, which run the hooks.
func TestStandardBackends(t *testing.T) {
	t.Parallel()

	noVersion := func(bool, bool) string { return "" }

	_, registry, err := construct(Modules{}, &atomic.Bool{}, noVersion)
	require.NoError(t, err)
	assert.Empty(t, registry.Names())

	_, registry, err = construct(standard.Modules(), &atomic.Bool{}, noVersion)
	require.NoError(t, err)
	assert.Equal(t, []string{"make", "shell", "c", "cargo", "cmake", "go", "lua-c", "zig"},
		registry.Names())

	for _, name := range registry.Names() {
		backend, module, ok := registry.Backend(name)
		require.True(t, ok, name)
		assert.Equal(t, "build", module, name)
		assert.Equal(t, name == "make" || name == "shell", backend.Hook, name)
	}
}
