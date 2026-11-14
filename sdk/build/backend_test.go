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

func TestModuleExt(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ".so", build.Target{OS: "linux", Arch: "amd64"}.ModuleExt())
	assert.Equal(t, ".dylib", build.Target{OS: "darwin", Arch: "arm64"}.ModuleExt())
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
