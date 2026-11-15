package build_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

func TestFieldError(t *testing.T) {
	t.Parallel()

	err := build.FieldErrorf("platforms.linux", "%q is not an OS", "linux")

	var fieldErr *build.FieldError

	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, "platforms.linux", fieldErr.Field)
	assert.Equal(t, `"linux" is not an OS`, fieldErr.Msg)
	assert.Equal(t, `platforms.linux: "linux" is not an OS`, err.Error())

	assert.Equal(t, "module is required", build.FieldErrorf("", "module is required").Error())
}

// typedConfig is the configuration of a backend of TestTyped.
type typedConfig struct {
	Module string `toml:"module"`
}

// typedBuild is the build of TestTyped.
type typedBuild struct{ module string }

func (typedBuild) Run(context.Context, *build.Context) error { return nil }

// TestTyped checks that the New Typed makes hands the function the
// configuration Config returned, decoded, and refuses one of another type.
func TestTyped(t *testing.T) {
	t.Parallel()

	newBuild := build.Typed(func(cfg *typedConfig, c build.Component, _ build.Common) (
		build.Build, error,
	) {
		return typedBuild{module: cfg.Module + "@" + c.Name}, nil
	})

	backend := build.Backend{
		Name: "typed", Produces: []build.Result{build.Module},
		Config: func() any { return new(typedConfig) }, New: newBuild,
	}

	require.NoError(t, backend.Validate())

	cfg, _, err := backend.Decode(map[string]any{"module": "fast"})
	require.NoError(t, err)

	bld, err := backend.New(cfg, build.Component{Name: "c"}, build.Common{})
	require.NoError(t, err)
	assert.Equal(t, typedBuild{module: "fast@c"}, bld)

	_, err = newBuild(new(struct{}), build.Component{}, build.Common{})
	require.ErrorIs(t, err, build.ErrConfig)
	assert.Contains(t, err.Error(), "*build_test.typedConfig")
}

func TestModuleExt(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ".so", build.Target{OS: "linux", Arch: "amd64"}.ModuleExt())
	assert.Equal(t, ".dylib", build.Target{OS: "darwin", Arch: "arm64"}.ModuleExt())
}

func TestDirsOf(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // Only the directories of the results matter.
	dirs := build.Dirs{Modules: "/stage/lib/app", Share: "/stage/lua/app", Programs: "/stage/bin"}

	assert.Equal(t, []build.Result{build.Module, build.Data, build.Program}, build.Results())
	// The values of the types never change: a Result is stored and compared
	// as the string it is.
	assert.Equal(t, []build.Result{"module", "data", "program"}, build.Results())
	assert.Equal(t, "/stage/lib/app", dirs.Of(build.Module))
	assert.Equal(t, "/stage/lua/app", dirs.Of(build.Data))
	assert.Equal(t, "/stage/bin", dirs.Of(build.Program))
	assert.Empty(t, dirs.Of("executable"))
}

func TestSupportedOS(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"linux", "darwin"}, build.SupportedOS())
}

// host records what a Context asks of it.
type host struct {
	commands []string
}

var errRun = errors.New("run")

func (h *host) Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	h.commands = append(h.commands, name)

	return exec.CommandContext(ctx, name, args...)
}

func (h *host) Run(*exec.Cmd) error { return errRun }

func (h *host) Output(*exec.Cmd) ([]byte, error) { return []byte("out"), nil }

func (h *host) LookPath(name string) (string, error) { return "/bin/" + name, nil }

func (h *host) LockedVersion(rock string) (string, bool) { return rock + "-1.0.0", true }

func TestContext(t *testing.T) {
	t.Parallel()

	recorder := &host{commands: nil}

	//nolint:exhaustruct // The facts of the build do not matter here.
	facts := build.NewContext(build.Context{Static: true}, recorder)

	assert.True(t, facts.Static)

	cmd := facts.Command(t.Context(), "make", "all")
	assert.Equal(t, []string{"make"}, recorder.commands)

	require.ErrorIs(t, facts.Run(cmd), errRun)

	out, err := facts.Output(cmd)
	require.NoError(t, err)
	assert.Equal(t, []byte("out"), out)

	path, err := facts.LookPath("zig")
	require.NoError(t, err)
	assert.Equal(t, "/bin/zig", path)

	version, ok := facts.LockedVersion("sdk")
	assert.True(t, ok)
	assert.Equal(t, "sdk-1.0.0", version)

	//nolint:exhaustruct // A Context made by hand.
	assert.Panics(t, func() { _ = (&build.Context{}).Run(cmd) })
}
