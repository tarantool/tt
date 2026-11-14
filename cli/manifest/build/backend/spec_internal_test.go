package backend

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

func TestDecode(t *testing.T) {
	t.Parallel()

	yes := true

	spec, err := Decode(manifest.Build{
		Backend: BackendLuaC, Cwd: "src", Env: map[string]string{"A": "1"},
		Output: nil, Incremental: nil, Static: &yes,
		Rest: map[string]any{
			"module": "m", "sources": []any{"m.c"},
			"platforms": map[string]any{"linux": map[string]any{"cflags": []any{"-O2"}}},
			"unknown":   "left out",
		},
	})
	require.NoError(t, err)

	assert.Equal(t, Spec{
		Backend: BackendLuaC, Cwd: "src", Env: map[string]string{"A": "1"}, Static: &yes,
		Module: "m", Sources: []string{"m.c"},
		Platforms: map[string]BuildOverlay{"linux": {Cflags: []string{"-O2"}}},
	}, spec)

	_, err = Decode(manifest.Build{Backend: BackendMake, Rest: map[string]any{"flags": "-j4"}})
	require.ErrorContains(t, err, "flags: must be an array, not a string")
}

// TestCore checks the backends of the core: their names in their order, and
// what each allows of the common fields and of the kinds, as backendFields
// and checkKind have it.
func TestCore(t *testing.T) {
	t.Parallel()

	names := make([]string, 0, len(Core()))

	for _, backend := range Core() {
		names = append(names, backend.Name)

		assert.Equal(t, Owns(backend.Name, "output"), backend.OutputFrom != sdkbuild.OutputNone,
			backend.Name)
		assert.Equal(t, Owns(backend.Name, "incremental"), backend.Incremental, backend.Name)

		fields, err := backend.Fields()
		require.NoError(t, err)

		for _, field := range manifest.CommonBuildFields() {
			assert.NotContains(t, fields, field)
		}
	}

	assert.Equal(t, []string{
		BackendMake, BackendShell, BackendC, BackendCargo, BackendCMake, BackendGo, BackendLuaC,
	}, names)
}

func TestOwns(t *testing.T) {
	t.Parallel()

	assert.True(t, Owns(BackendMake, "make_target"))
	assert.False(t, Owns(BackendShell, "make_target"))
	assert.True(t, Owns(BackendCargo, "module"))
	assert.True(t, Owns(BackendGo, "ldflags"))
	assert.False(t, Owns(BackendGo, "static"), "static is every block's")
	assert.False(t, Owns(BackendGo, "unknown"))
}

// TestSupportedOS checks that the OSes the SDK names are those tt builds
// for.
func TestSupportedOS(t *testing.T) {
	t.Parallel()

	assert.Equal(t, native.SupportedOS(), sdkbuild.SupportedOS())
}

// TestCoreBuildRun checks that a build of the core refuses the SDK's run.
func TestCoreBuildRun(t *testing.T) {
	t.Parallel()

	spec := Spec{Backend: BackendMake, MakeTarget: "all"}

	built, err := newCoreBuild(&spec, sdkbuild.Component{Kind: sdkbuild.KindModule},
		sdkbuild.Common{})
	require.NoError(t, err)
	require.ErrorIs(t, built.Run(t.Context(), nil), errNotRun)

	_, err = newCoreBuild(new(struct{}), sdkbuild.Component{}, sdkbuild.Common{})
	require.ErrorIs(t, err, errNotSpec)
}
