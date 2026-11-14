package buildreg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
)

func TestCore(t *testing.T) {
	t.Parallel()

	registry := buildreg.Core()

	assert.Equal(t, []string{"make", "shell", "c", "cargo", "cmake", "go", "lua-c"},
		registry.Names())

	for _, name := range registry.Names() {
		backend, module, ok := registry.Backend(name)
		assert.True(t, ok, name)
		assert.Empty(t, module, name)
		// make and shell alone run hooks.
		assert.Equal(t, name == "make" || name == "shell", backend.Hook, name)
	}

	// A module cannot take a name of the core.
	err := registry.Add("other", []build.Backend{backend("make")}, nil)
	require.ErrorIs(t, err, buildreg.ErrBackend)
	assert.Contains(t, err.Error(), "the core provides one of the name too")
}

//nolint:paralleltest // The registry of the process is the process's.
func TestProcessIsCoreByDefault(t *testing.T) {
	assert.Equal(t, buildreg.Core().Names(), buildreg.Process().Names())
}

// zigBuild is the build zig returns.
type zigBuild struct {
	steps []string
}

func (zigBuild) Run(context.Context, *build.Context) error { return nil }

// zig is a backend of a module: of both kinds, incremental, with no output.
func zig() build.Backend {
	zigger := backend("zig")

	zigger.New = func(cfg any, _ build.Component, _ build.Common) (build.Build, error) {
		config, ok := cfg.(*zigConfig)
		if !ok {
			return nil, build.FieldErrorf("", "not a configuration of zig")
		}

		if len(config.Steps) == 0 {
			return nil, build.FieldErrorf("", "steps is required for backend %q", "zig")
		}

		if config.Bin == "." {
			return nil, build.FieldErrorf("bin", "%q is not a file name", config.Bin)
		}

		return zigBuild{steps: config.Steps}, nil
	}

	return zigger
}

// registry returns the registry of the core with zig of the module zig and a
// backend of programs only, wasm-tool, of the module wasm.
func registry(t *testing.T) *buildreg.Registry {
	t.Helper()

	registry := buildreg.Core()

	require.NoError(t, registry.Add("zig", []build.Backend{zig()}, nil))

	tool := backend("wasm-tool")

	tool.Config = func() any { return new(struct{}) }
	tool.Kinds = []string{build.KindExecutable}
	tool.Incremental = false
	tool.OutputFrom = build.OutputCwd

	require.NoError(t, registry.Add("wasm", []build.Backend{tool}, nil))

	return registry
}

// moduleTarget is a module component a of the package demo.
func moduleTarget() manifest.BuildTarget {
	return manifest.BuildTarget{
		Component: "a", Kind: manifest.KindModule, Path: "a", Namespace: "demo",
		Package: "demo", Hook: false,
	}
}

func TestCheckBuild(t *testing.T) {
	t.Parallel()

	check, err := registry(t).CheckBuild("components.a.build", moduleTarget(), manifest.Build{
		Backend: "zig", Rest: map[string]any{"steps": []any{"install"}, "bin": "a"},
	})
	require.NoError(t, err)
	assert.Equal(t, manifest.BuildCheck{Known: true, Unknown: nil}, check)
}

// namedBuild is a build that names its programs.
type namedBuild struct {
	zigBuild

	programs []string
}

func (b namedBuild) Programs() ([]string, bool) { return b.programs, true }

// TestCheckBuildPrograms checks that the programs of an executable component
// are those its build names, and that a module component has none.
func TestCheckBuildPrograms(t *testing.T) {
	t.Parallel()

	registry := buildreg.Core()
	named := zig()

	named.New = func(any, build.Component, build.Common) (build.Build, error) {
		return namedBuild{programs: []string{"gw", "gwctl"}}, nil
	}

	require.NoError(t, registry.Add("zig", []build.Backend{named}, nil))

	program := moduleTarget()

	program.Kind = manifest.KindExecutable

	check, err := registry.CheckBuild("components.a.build", program, manifest.Build{Backend: "zig"})
	require.NoError(t, err)
	assert.Equal(t, []string{"gw", "gwctl"}, check.Programs)

	check, err = registry.CheckBuild("components.a.build", moduleTarget(),
		manifest.Build{Backend: "zig"})
	require.NoError(t, err)
	assert.Nil(t, check.Programs)
}

// TestBuild checks that the registry returns the backend of a block and the
// build its New made, and names a backend it does not have.
func TestBuild(t *testing.T) {
	t.Parallel()

	backend, bld, err := registry(t).Build("components.a.build", moduleTarget(), manifest.Build{
		Backend: "zig", Rest: map[string]any{"steps": []any{"install"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "zig", backend.Name)
	assert.Equal(t, zigBuild{steps: []string{"install"}}, bld)

	_, _, err = registry(t).Build("components.a.build", moduleTarget(), manifest.Build{
		Backend: "zig", Rest: map[string]any{"steps": "install"},
	})
	require.EqualError(t, err, "components.a.build.steps: must be an array, not a string")

	_, _, err = registry(t).Build("components.a.build", moduleTarget(), manifest.Build{
		Backend: "zig",
	})
	require.EqualError(t, err, `components.a.build: steps is required for backend "zig"`)

	_, _, err = registry(t).Build("components.a.build", moduleTarget(),
		manifest.Build{Backend: "rust"})
	require.ErrorIs(t, err, buildreg.ErrNoBackend)
	require.EqualError(t, err, `no module of this tt provides backend "rust"`)
}

func TestCheckBuildUnknownBackend(t *testing.T) {
	t.Parallel()

	check, err := registry(t).CheckBuild("components.a.build", moduleTarget(), manifest.Build{
		Backend: "rust", Rest: map[string]any{"anything": true},
	})
	require.NoError(t, err)
	assert.Equal(t, manifest.BuildCheck{Known: false, Unknown: nil}, check)
}

func TestCheckBuildUnknownFields(t *testing.T) {
	t.Parallel()

	// A field no backend has is reported with the rest, however the backend
	// judges the block: the manifest decides by its version.
	check, err := registry(t).CheckBuild("components.a.build", moduleTarget(), manifest.Build{
		Backend: "zig", Rest: map[string]any{"steps": []any{"install"}, "zz": 1, "aa": 2},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"aa", "zz"}, check.Unknown)

	check, err = registry(t).CheckBuild("components.a.build", moduleTarget(), manifest.Build{
		Backend: "make", Rest: map[string]any{"make_target": "all", "jobs": 4},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"jobs"}, check.Unknown)

	check, err = registry(t).CheckBuild("components.a.build", moduleTarget(), manifest.Build{
		Backend: "zig", Rest: map[string]any{"zz": 1},
	})
	require.Error(t, err)
	assert.Equal(t, []string{"zz"}, check.Unknown)
}

func TestCheckBuildErrors(t *testing.T) {
	t.Parallel()

	program := moduleTarget()

	program.Kind = manifest.KindExecutable
	program.Namespace = ""

	hook := manifest.BuildTarget{
		Component: "", Kind: "", Path: "", Namespace: "", Package: "demo", Hook: true,
	}

	yes := true
	steps := []any{"install"}

	tests := []struct {
		name   string
		target manifest.BuildTarget
		block  manifest.Build
		field  string
		msg    string
	}{
		{
			"a type", moduleTarget(),
			manifest.Build{Backend: "zig", Rest: map[string]any{"steps": "install"}},
			"components.a.build.steps", "must be an array, not a string",
		},
		{
			"an error of the block", moduleTarget(),
			manifest.Build{Backend: "zig"},
			"components.a.build", `steps is required for backend "zig"`,
		},
		{
			"an error of a field", moduleTarget(),
			manifest.Build{Backend: "zig", Rest: map[string]any{"steps": steps, "bin": "."}},
			"components.a.build.bin", `"." is not a file name`,
		},
		{
			"a field of the core", moduleTarget(),
			manifest.Build{Backend: "zig", Rest: map[string]any{
				"steps": steps, "make_target": "all",
			}},
			"components.a.build.make_target", `is a field of backend make, not of "zig"`,
		},
		{
			"a field the core shares", moduleTarget(),
			manifest.Build{Backend: "zig", Rest: map[string]any{
				"steps": steps, "ldflags": []any{"-s"},
			}},
			"components.a.build.ldflags", `is a field of backend c, go or lua-c, not of "zig"`,
		},
		{
			"a field of a module on the core", moduleTarget(),
			manifest.Build{Backend: "make", Rest: map[string]any{
				"make_target": "all", "steps": steps,
			}},
			"components.a.build.steps", `is a field of backend zig, not of "make"`,
		},
		{
			"output", moduleTarget(),
			manifest.Build{Backend: "zig", Output: []string{"a.so"}, Rest: map[string]any{
				"steps": steps,
			}},
			"components.a.build.output",
			`is a field of backend make, shell, cmake or wasm-tool, not of "zig"`,
		},
		{
			"incremental", program,
			manifest.Build{Backend: "wasm-tool", Incremental: &yes},
			"components.a.build.incremental",
			`is a field of backend make, shell or zig, not of "wasm-tool"`,
		},
		{
			"a program of a backend of modules", program,
			manifest.Build{Backend: "c", Rest: map[string]any{
				"module": "m", "sources": []any{"m.c"},
			}},
			"components.a.build",
			`backend "c" builds a module, not a program (kind "executable" wants ` +
				`make, shell, cargo, cmake, go, wasm-tool or zig)`,
		},
		{
			"a module of a backend of programs", moduleTarget(),
			manifest.Build{Backend: "wasm-tool"},
			"components.a.build", `backend "wasm-tool" builds a program: set kind = ` +
				`"executable" on the component`,
		},
		{
			"a hook", hook,
			manifest.Build{Backend: "shell", Rest: map[string]any{
				"command": "./gen.sh", "cgo": true,
			}},
			"hooks.pre_build.cgo", `is a field of backend go, not of "shell"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			field := "components.a.build"
			if test.target.Hook {
				field = "hooks.pre_build"
			}

			check, err := registry(t).CheckBuild(field, test.target, test.block)
			assert.True(t, check.Known)

			var verr *manifest.ValidationError

			require.ErrorAs(t, err, &verr)
			assert.Equal(t, test.field, verr.Field, err.Error())
			assert.Equal(t, test.msg, verr.Msg)
		})
	}
}

// TestCheckBuildKindOfModules checks the kind a backend of a module builds
// against a list of the backends of programs with those of the modules.
func TestCheckBuildKindOfModules(t *testing.T) {
	t.Parallel()

	registry := registry(t)

	modules := backend("cpp")

	modules.Kinds = []string{build.KindModule}

	require.NoError(t, registry.Add("cpp", []build.Backend{modules}, nil))

	program := moduleTarget()

	program.Kind = manifest.KindExecutable

	_, err := registry.CheckBuild("components.a.build", program, manifest.Build{Backend: "cpp"})
	require.EqualError(t, err, `components.a.build: backend "cpp" builds a module, `+
		`not a program (kind "executable" wants make, shell, cargo, cmake, go, wasm-tool or zig)`)
}

// TestCheckBuildHookKind checks that a hook is no component of a kind, and
// that it names a backend that runs hooks: one of a module runs them when it
// says so.
func TestCheckBuildHookKind(t *testing.T) {
	t.Parallel()

	hook := manifest.BuildTarget{
		Component: "", Kind: "", Path: "", Namespace: "", Package: "demo", Hook: true,
	}
	block := manifest.Build{Backend: "zig", Rest: map[string]any{"steps": []any{"install"}}}

	check, err := registry(t).CheckBuild("hooks.pre_build", hook, block)
	assert.True(t, check.Known)
	require.EqualError(t, err,
		`hooks.pre_build: backend "zig" is not supported (want make or shell)`)

	registry := buildreg.Core()
	hooks := zig()

	hooks.Hook = true

	require.NoError(t, registry.Add("zig", []build.Backend{hooks}, nil))

	_, err = registry.CheckBuild("hooks.pre_build", hook, block)
	require.NoError(t, err)

	_, err = registry.CheckBuild("hooks.pre_build", hook, manifest.Build{Backend: "cmake"})
	require.EqualError(t, err,
		`hooks.pre_build: backend "cmake" is not supported (want make, shell or zig)`)

	// A backend of no module is not one that runs hooks either.
	check, err = registry.CheckBuild("hooks.pre_build", hook, manifest.Build{Backend: "rust"})
	assert.False(t, check.Known)
	require.EqualError(t, err,
		`hooks.pre_build: backend "rust" is not supported (want make, shell or zig)`)
}
