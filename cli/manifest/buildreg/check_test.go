package buildreg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
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
func TestProcessIsEmptyByDefault(t *testing.T) {
	assert.Empty(t, buildreg.Process().Names())
}

// zigBuild is the build zig returns.
type zigBuild struct {
	steps []string
}

func (zigBuild) Run(context.Context, *build.Context) error { return nil }

// zig is a backend of a module: of modules and programs, incremental, with
// no output.
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

// registry returns the registry of the backends of the tests with zig of the
// module zig and a backend of programs only, wasm-tool, of the module wasm.
func registry(t *testing.T) *buildreg.Registry {
	t.Helper()

	registry := buildregtest.Registry()

	require.NoError(t, registry.Add("zig", []build.Backend{zig()}, nil))

	tool := backend("wasm-tool")

	tool.Config = func() any { return new(struct{}) }
	tool.Produces = []build.Result{build.Program}
	tool.Incremental = false
	tool.OutputFrom = build.OutputCwd

	require.NoError(t, registry.Add("wasm", []build.Backend{tool}, nil))

	return registry
}

// moduleTarget is a component a of the package demo, of no kind, as a
// component is.
func moduleTarget() manifest.BuildTarget {
	return manifest.BuildTarget{
		Component: "a", Kind: "", Path: "a", Namespace: "demo", Package: "demo", Hook: false,
	}
}

// kindTarget is moduleTarget of the deprecated kind.
func kindTarget(kind string) manifest.BuildTarget {
	target := moduleTarget()

	target.Kind = kind

	return target
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

// TestCheckBuildPrograms checks that the programs of a component are those
// its build names, whatever the kind of the component, and that a build
// that names none has none.
func TestCheckBuildPrograms(t *testing.T) {
	t.Parallel()

	registry := buildregtest.Registry()
	named := zig()

	named.New = func(any, build.Component, build.Common) (build.Build, error) {
		return namedBuild{programs: []string{"gw", "gwctl"}}, nil
	}

	require.NoError(t, registry.Add("zig", []build.Backend{named}, nil))

	for _, target := range []manifest.BuildTarget{
		moduleTarget(), kindTarget(manifest.KindExecutable),
	} {
		check, err := registry.CheckBuild("components.a.build", target,
			manifest.Build{Backend: "zig"})
		require.NoError(t, err)
		assert.Equal(t, []string{"gw", "gwctl"}, check.Programs)
	}

	check, err := registry.CheckBuild("components.a.build", moduleTarget(),
		manifest.Build{Backend: "make", Rest: map[string]any{"make_target": "all"}})
	require.NoError(t, err)
	assert.Empty(t, check.Programs)

	check, err = registry.CheckBuild("components.a.build", moduleTarget(), manifest.Build{
		Backend: "make", Rest: map[string]any{"make_target": "all"},
		Output: []manifest.OutputEntry{
			{Path: "out/a.so", To: ""}, {Path: "out/gw", To: manifest.OutputToBin},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"gw"}, check.Programs)
}

// TestCheckBuildExpects checks what a backend is told the component expects
// of its build: nothing for a component of no kind, as a component is, and
// what the deprecated kind says otherwise; and the types of the files of
// output by the kind.
func TestCheckBuildExpects(t *testing.T) {
	t.Parallel()

	var told []build.Component

	registry := buildregtest.Registry()
	expecting := zig()

	expecting.New = func(_ any, component build.Component, common build.Common) (
		build.Build, error,
	) {
		told = append(told, component)

		return zigBuild{steps: nil}, nil
	}

	require.NoError(t, registry.Add("zig", []build.Backend{expecting}, nil))

	for _, target := range []manifest.BuildTarget{
		moduleTarget(), kindTarget(manifest.KindModule), kindTarget(manifest.KindExecutable),
	} {
		_, err := registry.CheckBuild("components.a.build", target, manifest.Build{Backend: "zig"})
		require.NoError(t, err)
	}

	require.Len(t, told, 3)
	assert.Nil(t, told[0].Expects)
	assert.Equal(t, []build.Result{build.Module, build.Data}, told[1].Expects)
	assert.Equal(t, []build.Result{build.Program}, told[2].Expects)

	entries := []manifest.OutputEntry{
		{Path: "a", To: ""}, {Path: "a", To: manifest.OutputToLib},
		{Path: "a", To: manifest.OutputToShare}, {Path: "a", To: manifest.OutputToBin},
	}

	types := make([]build.Result, 0, len(entries))
	programs := make([]build.Result, 0, len(entries))

	for _, entry := range entries {
		types = append(types, buildreg.OutputType(entry, ""))
		programs = append(programs, buildreg.OutputType(entry, manifest.KindExecutable))
	}

	assert.Equal(t, []build.Result{build.Module, build.Module, build.Data, build.Program}, types)
	assert.Equal(t, []build.Result{build.Program, build.Module, build.Data, build.Program},
		programs)
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

	program := kindTarget(manifest.KindExecutable)
	module := kindTarget(manifest.KindModule)

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
			"a field of another module", moduleTarget(),
			manifest.Build{Backend: "zig", Rest: map[string]any{
				"steps": steps, "make_target": "all",
			}},
			"components.a.build.make_target", `is a field of backend make, not of "zig"`,
		},
		{
			"a field two backends share", moduleTarget(),
			manifest.Build{Backend: "zig", Rest: map[string]any{
				"steps": steps, "ldflags": []any{"-s"},
			}},
			"components.a.build.ldflags", `is a field of backend exe or lib, not of "zig"`,
		},
		{
			"a field of a module added later", moduleTarget(),
			manifest.Build{Backend: "make", Rest: map[string]any{
				"make_target": "all", "steps": steps,
			}},
			"components.a.build.steps", `is a field of backend zig, not of "make"`,
		},
		{
			"output", moduleTarget(),
			manifest.Build{
				Backend: "zig", Output: []manifest.OutputEntry{{Path: "a.so", To: ""}},
				Rest: map[string]any{"steps": steps},
			},
			"components.a.build.output",
			`is a field of backend make, shell, tree or wasm-tool, not of "zig"`,
		},
		{
			"incremental", program,
			manifest.Build{Backend: "wasm-tool", Incremental: &yes},
			"components.a.build.incremental",
			`is a field of backend make, shell or zig, not of "wasm-tool"`,
		},
		{
			"a module of a backend of programs", moduleTarget(),
			manifest.Build{
				Backend: "wasm-tool", Output: []manifest.OutputEntry{{Path: "a.so", To: ""}},
			},
			"components.a.build.output[0]", `backend "wasm-tool" makes no modules`,
		},
		{
			"a data file of a backend of programs", moduleTarget(),
			manifest.Build{Backend: "wasm-tool", Output: []manifest.OutputEntry{
				{Path: "gw", To: manifest.OutputToBin},
				{Path: "a.wasm", To: manifest.OutputToShare},
			}},
			"components.a.build.output[1].to", `backend "wasm-tool" makes no data files`,
		},
		{
			"kind = executable of a backend of modules", program,
			manifest.Build{Backend: "lib", Rest: map[string]any{
				"module": "m", "sources": []any{"m.c"},
			}},
			"components.a.kind", `is "executable", but backend "lib" makes no programs`,
		},
		{
			"kind = module of a backend of programs", module,
			manifest.Build{Backend: "exe"},
			"components.a.kind", `is "module", but backend "exe" makes programs only`,
		},
		{
			"kind = executable of a build of a module", program,
			manifest.Build{Backend: "both", Rest: map[string]any{"module": "m"}},
			"components.a.kind", `is "executable", but the build makes modules`,
		},
		{
			"kind = module of a build of a program", module,
			manifest.Build{Backend: "both", Rest: map[string]any{"bin": "gw"}},
			"components.a.kind", `is "module", but the build makes programs`,
		},
		{
			"kind = executable of an output of a module", program,
			manifest.Build{
				Backend: "shell", Rest: map[string]any{"command": "./b.sh"},
				Output: []manifest.OutputEntry{
					{Path: "gw", To: ""}, {Path: "gw.so", To: manifest.OutputToLib},
				},
			},
			"components.a.kind", `is "executable", but the build makes modules`,
		},
		{
			"a hook", hook,
			manifest.Build{Backend: "shell", Rest: map[string]any{
				"command": "./gen.sh", "cgo": true,
			}},
			"hooks.pre_build.cgo", `is a field of backend exe, not of "shell"`,
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

// TestCheckBuildResultsOfModules checks what a backend of a module makes
// against what its block asks for and the deprecated kind of the component:
// one that makes modules only, and one whose build says it makes what the
// backend does not make.
func TestCheckBuildResultsOfModules(t *testing.T) {
	t.Parallel()

	registry := registry(t)

	modules := backend("cpp")

	modules.Produces = []build.Result{build.Module}

	liar := backend("liar")

	liar.Produces = []build.Result{build.Data}
	liar.New = func(any, build.Component, build.Common) (build.Build, error) {
		return producerBuild{results: []build.Result{build.Data, build.Program}}, nil
	}

	require.NoError(t, registry.Add("cpp", []build.Backend{modules, liar}, nil))

	_, err := registry.CheckBuild("components.a.build", moduleTarget(),
		manifest.Build{Backend: "cpp"})
	require.NoError(t, err)

	_, err = registry.CheckBuild("components.a.build", kindTarget(manifest.KindExecutable),
		manifest.Build{Backend: "cpp"})
	require.EqualError(t, err,
		`components.a.kind: is "executable", but backend "cpp" makes no programs`)

	_, err = registry.CheckBuild("components.a.build", moduleTarget(),
		manifest.Build{Backend: "liar"})
	require.EqualError(t, err, `components.a.build: backend "liar" makes no programs`)
}

// producerBuild is a build that says it makes results.
type producerBuild struct {
	zigBuild

	results []build.Result
}

func (b producerBuild) Produces() []build.Result { return b.results }

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

	registry := buildregtest.Registry()
	hooks := zig()

	hooks.Hook = true

	require.NoError(t, registry.Add("zig", []build.Backend{hooks}, nil))

	_, err = registry.CheckBuild("hooks.pre_build", hook, block)
	require.NoError(t, err)

	_, err = registry.CheckBuild("hooks.pre_build", hook, manifest.Build{Backend: "tree"})
	require.EqualError(t, err,
		`hooks.pre_build: backend "tree" is not supported (want make, shell or zig)`)

	// A backend of no module is not one that runs hooks either.
	check, err = registry.CheckBuild("hooks.pre_build", hook, manifest.Build{Backend: "rust"})
	assert.False(t, check.Known)
	require.EqualError(t, err,
		`hooks.pre_build: backend "rust" is not supported (want make, shell or zig)`)

	// A tt none of whose modules provides a backend that runs hooks, as a
	// distribution without the standard backends, says so of the shell of a
	// hook that names no backend.
	none := buildreg.New()

	require.NoError(t, none.Add("zig", []build.Backend{zig()}, nil))

	check, err = none.CheckBuild("hooks.pre_build", hook, manifest.Build{Backend: "shell"})
	assert.False(t, check.Known)
	require.EqualError(t, err, `hooks.pre_build: backend "shell" is not supported: `+
		"no module of this tt provides a backend that runs hooks")
}
