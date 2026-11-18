package build_test

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
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

// TestTarget: the host is the platform of the machine, named <os>-<arch>,
// and a target is a cross one when it is another.
func TestTarget(t *testing.T) {
	t.Parallel()

	host := build.HostTarget()

	assert.Equal(t, build.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}, host)
	assert.Equal(t, runtime.GOOS+"-"+runtime.GOARCH, host.String())
	assert.Equal(t, "linux-arm64", build.Target{OS: "linux", Arch: "arm64"}.String())
	assert.False(t, host.Cross())
	assert.True(t, otherTarget().Cross())
}

// otherTarget is a platform other than the machine of the test.
func otherTarget() build.Target {
	if runtime.GOARCH == "arm64" {
		return build.Target{OS: runtime.GOOS, Arch: "amd64"}
	}

	return build.Target{OS: runtime.GOOS, Arch: "arm64"}
}

// TestCheckTarget: a backend builds for the machine whatever it says, and
// for another platform only with Cross; the refusal names the backend and
// both platforms.
func TestCheckTarget(t *testing.T) {
	t.Parallel()

	backend := build.Backend{Name: "c", Cross: false}

	require.NoError(t, backend.CheckTarget(build.HostTarget()))

	err := backend.CheckTarget(otherTarget())
	require.ErrorIs(t, err, build.ErrTarget)
	assert.Equal(t, "the build cannot be made for the target platform: the c backend builds "+
		"for the machine tt runs on alone, "+build.HostTarget().String()+", and the build is "+
		"for "+otherTarget().String(), err.Error())

	backend.Cross = true

	require.NoError(t, backend.CheckTarget(otherTarget()))
	require.NoError(t, backend.CheckTarget(build.HostTarget()))
}

func TestDirsOf(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // Only the directories of the results matter.
	dirs := build.Dirs{
		Modules: "/stage/lib/app", Share: "/stage/lua/app", Programs: "/stage/bin",
		Link: "/state/link/default/foo",
	}

	assert.Equal(t, []build.Result{build.Module, build.Data, build.Program, build.Link},
		build.Results())
	// The values of the types never change: a Result is stored and compared
	// as the string it is.
	assert.Equal(t, []build.Result{"module", "data", "program", "link"}, build.Results())
	assert.Equal(t, "/stage/lib/app", dirs.Of(build.Module))
	assert.Equal(t, "/stage/lua/app", dirs.Of(build.Data))
	assert.Equal(t, "/stage/bin", dirs.Of(build.Program))
	assert.Equal(t, "/state/link/default/foo", dirs.Of(build.Link))
	assert.Empty(t, dirs.Of("executable"))
}

// TestDirsOutputPath pins where a file of output goes: under its name in the
// directory of its type, a header of a library to link under include/.
func TestDirsOutputPath(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // Only the directories of the results matter.
	dirs := build.Dirs{Modules: "/stage/lib/app", Link: "/state/link/default/foo"}

	assert.Equal(t, "/stage/lib/app/foo.h", dirs.OutputPath(build.Module, "foo.h"))
	assert.Equal(t, "/state/link/default/foo/libfoo.a", dirs.OutputPath(build.Link, "libfoo.a"))

	for _, header := range []string{"foo.h", "foo.hh", "foo.hpp", "foo.hxx"} {
		assert.Equal(t, "/state/link/default/foo/include/"+header,
			dirs.OutputPath(build.Link, header))
	}

	assert.Empty(t, dirs.OutputPath(build.Program, "tool"), "no directory of programs")
	assert.Empty(t, dirs.OutputPath("executable", "tool"))
}

// TestDirsLinkOf pins where the libraries another component made for linking
// are: beside the Link of the build, and nowhere for a build of no Link.
func TestDirsLinkOf(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // Only Link matters.
	dirs := build.Dirs{Link: "/state/link/default/app"}

	assert.Equal(t, "/state/link/default/foo", dirs.LinkOf("foo"))
	assert.Empty(t, build.Dirs{}.LinkOf("foo")) //nolint:exhaustruct // A hook has no Link.
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

// TestContextJobsOrCPUs: the number of jobs of a build is its Jobs, and one
// a CPU when it is 0; a Context made by hand, with no host, has it too.
func TestContextJobsOrCPUs(t *testing.T) {
	t.Parallel()

	for jobs, want := range map[int]int{0: runtime.NumCPU(), 1: 1, 3: 3, 64: 64} {
		//nolint:exhaustruct // The jobs alone matter here.
		facts := build.Context{Jobs: jobs}

		assert.Equal(t, want, facts.JobsOrCPUs(), "jobs %d", jobs)
	}
}
