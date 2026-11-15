package buildreg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
)

// zigConfig is the configuration of a backend of the tests.
type zigConfig struct {
	Steps []string `toml:"steps"`
	Bin   string   `toml:"bin"`
}

// noBuild is a build that builds nothing.
type noBuild struct{}

func (noBuild) Run(context.Context, *build.Context) error { return nil }

// backend returns a well-formed backend name of the tests, which change
// what they check.
func backend(name string) build.Backend {
	return build.Backend{
		Name:     name,
		Produces: []build.Result{build.Module, build.Program},
		Config:   func() any { return new(zigConfig) },
		New: func(any, build.Component, build.Common) (build.Build, error) {
			return noBuild{}, nil
		},
		Incremental: true,
		OutputFrom:  build.OutputNone,
		Exclude:     []string{"/zig-out/"},
	}
}

// fileCheck returns a well-formed file check of suffix.
func fileCheck(suffix string) build.FileCheck {
	return build.FileCheck{Suffix: suffix, Check: func(context.Context, string) (string, error) {
		return "", nil
	}}
}

func TestAdd(t *testing.T) {
	t.Parallel()

	registry := buildreg.New()

	require.NoError(t, registry.Add("zig", []build.Backend{backend("zig")},
		[]build.FileCheck{fileCheck(".zig")}))
	require.NoError(t, registry.Add("wasm", []build.Backend{backend("wasm"), backend("cwasm")},
		[]build.FileCheck{fileCheck(".wasm")}))

	// In the order they were added: the core adds the modules by name.
	assert.Equal(t, []string{"zig", "wasm", "cwasm"}, registry.Names())

	got, module, ok := registry.Backend("wasm")
	require.True(t, ok)
	assert.Equal(t, "wasm", got.Name)
	assert.Equal(t, "wasm", module)

	_, _, ok = registry.Backend("make")
	assert.False(t, ok)

	checks := registry.FileChecks()
	require.Len(t, checks, 2)
	assert.Equal(t, ".zig", checks[0].Suffix)
	assert.Equal(t, ".wasm", checks[1].Suffix)
}

func TestAddRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*build.Backend)
		msg    string
	}{
		{"a name with a capital", func(b *build.Backend) { b.Name = "Zig" },
			`"Zig": the name must match [a-z][a-z0-9-]*`},
		{"no name", func(b *build.Backend) { b.Name = "" }, `"": the name must match`},
		{"no New", func(b *build.Backend) { b.New = nil }, `"zig": no New`},
		{"no Config", func(b *build.Backend) { b.Config = nil }, `"zig": invalid configuration`},
		{"a Config of a map", func(b *build.Backend) {
			b.Config = func() any { return map[string]any{} }
		}, "Config returned map[string]interface {}, not a pointer to a struct"},
		{"no results", func(b *build.Backend) { b.Produces = nil }, "the Produces must name"},
		{"an unknown result", func(b *build.Backend) {
			b.Produces = []build.Result{"executable"}
		}, `the Produces must name module, data or program, once each, not "executable"`},
		{"a result twice", func(b *build.Backend) {
			b.Produces = []build.Result{build.Module, build.Module}
		}, `not "module, module"`},
		{"an OutputFrom of no meaning", func(b *build.Backend) { b.OutputFrom = 7 },
			"OutputFrom is none of OutputNone, OutputCwd and OutputBackend: 7"},
		{"an empty pattern", func(b *build.Backend) { b.Exclude = []string{""} },
			"an empty pattern in Exclude"},
		{"a common field", func(b *build.Backend) {
			b.Config = func() any {
				return &struct {
					Output []string `toml:"output"`
				}{}
			}
		}, "output is a field of every build block, not one of a backend"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			refused := backend("zig")
			test.change(&refused)

			registry := buildreg.New()

			err := registry.Add("zig", []build.Backend{refused, backend("cargo-zig")}, nil)
			require.ErrorIs(t, err, buildreg.ErrBackend)
			assert.Contains(t, err.Error(), `module "zig": invalid build backend "`)
			assert.Contains(t, err.Error(), test.msg)

			// The well-formed one is added all the same.
			assert.Equal(t, []string{"cargo-zig"}, registry.Names())

			// buildtest refuses what the registry refuses, with the same
			// error: a backend whose tests pass is one tt takes.
			_, testErr := buildtest.Validate(refused, build.Component{
				Name: "a", Path: "a", Namespace: "demo", Package: "demo", Wants: nil,
			}, buildtest.Block{Common: build.Common{}, Incremental: false, Rest: nil})
			require.ErrorIs(t, testErr, buildreg.ErrBackend)
			assert.Equal(t, `module "zig": `+testErr.Error(), err.Error())
		})
	}
}

// TestBuildtestTakesWhatTheRegistryTakes: a backend the registry takes is
// one buildtest takes, and both refuse no file check the other takes.
func TestBuildtestTakesWhatTheRegistryTakes(t *testing.T) {
	t.Parallel()

	taken := backend("zig")

	require.NoError(t, buildreg.New().Add("zig", []build.Backend{taken}, nil))

	_, err := buildtest.Validate(taken, build.Component{
		Name: "a", Path: "a", Namespace: "demo", Package: "demo", Wants: nil,
	}, buildtest.Block{Common: build.Common{}, Incremental: false, Rest: nil})
	require.NoError(t, err)

	for _, check := range []build.FileCheck{fileCheck(""), {Suffix: ".wasm", Check: nil}} {
		err := buildreg.New().Add("wasm", nil, []build.FileCheck{check})
		require.ErrorIs(t, err, buildreg.ErrFileCheck)
		assert.Equal(t, `module "wasm": `+check.Validate().Error(), err.Error())
	}
}

// TestCommonFields: the keys of the fields every build block has are the
// same for the manifest, which decodes them, and for the SDK, which keeps
// them out of the configuration of a backend. The manifest does not depend
// on the SDK, so the two lists are tied here.
func TestCommonFields(t *testing.T) {
	t.Parallel()

	assert.Equal(t, manifest.CommonBuildFields(), build.CommonFields())
}

func TestAddTwice(t *testing.T) {
	t.Parallel()

	registry := buildreg.New()

	require.NoError(t, registry.Add("zig", []build.Backend{backend("zig")}, nil))

	err := registry.Add("other", []build.Backend{backend("zig")}, nil)
	require.ErrorIs(t, err, buildreg.ErrBackend)
	require.EqualError(t, err,
		`module "other": invalid build backend "zig": module "zig" provides one of the name too`)

	backend, module, ok := registry.Backend("zig")
	require.True(t, ok)
	assert.Equal(t, "zig", backend.Name)
	assert.Equal(t, "zig", module)
}

func TestAddFileCheckRefused(t *testing.T) {
	t.Parallel()

	registry := buildreg.New()

	err := registry.Add("wasm", nil, []build.FileCheck{
		fileCheck(""), {Suffix: ".wasm", Check: nil}, fileCheck(".wat"),
	})
	require.ErrorIs(t, err, buildreg.ErrFileCheck)
	assert.Contains(t, err.Error(), `module "wasm": invalid file check: no Suffix`)
	assert.Contains(t, err.Error(), `module "wasm": invalid file check of ".wasm": no Check`)

	checks := registry.FileChecks()
	require.Len(t, checks, 1)
	assert.Equal(t, ".wat", checks[0].Suffix)
}

//nolint:paralleltest // The registry of the process is the process's.
func TestProcess(t *testing.T) {
	previous := buildreg.Process()

	t.Cleanup(func() { buildreg.SetProcess(previous) })

	registry := buildreg.New()
	require.NoError(t, registry.Add("zig", []build.Backend{backend("zig")}, nil))

	buildreg.SetProcess(registry)
	assert.Same(t, registry, buildreg.Process())
}
