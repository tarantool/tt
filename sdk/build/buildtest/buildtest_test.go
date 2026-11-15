package buildtest_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// touchConfig is the configuration of the touch backend.
type touchConfig struct {
	File string `toml:"file"`
}

// touchBuild writes File into the output directory with sh, and warns.
type touchBuild struct {
	file string
}

var _ build.Preflighter = touchBuild{}

func (b touchBuild) Preflight(_ context.Context, c *build.Context) error {
	_, err := c.LookPath("sh")

	return err
}

func (b touchBuild) Run(ctx context.Context, facts *build.Context) error {
	version, _ := facts.LockedVersion("sdk")
	facts.Warn("sdk " + version)

	out, err := facts.Output(facts.Command(ctx, "sh", "-c",
		"echo $TT_COMPONENT_NAME; echo err >&2"))
	if err != nil {
		return err
	}

	return facts.Run(facts.Command(ctx, "sh", "-c",
		`printf %s "$1" > "$TT_OUTPUT_DIR/$2"; echo done`, "sh", string(out), b.file))
}

// programBuild is a build of touch that says it makes a program.
type programBuild struct {
	touchBuild
}

func (programBuild) Produces() []build.Result {
	return []build.Result{build.Program}
}

// touch is a backend of modules, whose file "program" asks for a program
// all the same. Its output lists the files the core copies.
func touch() build.Backend {
	return build.Backend{
		Name:     "touch",
		Produces: []build.Result{build.Module},
		Config:   func() any { return new(touchConfig) },
		New: func(cfg any, _ build.Component, _ build.Common) (build.Build, error) {
			config, ok := cfg.(*touchConfig)
			if !ok {
				return nil, build.FieldErrorf("", "not a configuration of touch")
			}

			file := config.File
			if file == "" {
				return nil, build.FieldErrorf("", "file is required for backend %q", "touch")
			}

			if file == "program" {
				return programBuild{touchBuild{file: file}}, nil
			}

			return touchBuild{file: file}, nil
		},
		Incremental:    false,
		OutputFrom:     build.OutputCwd,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           false,
	}
}

func component() build.Component {
	return build.Component{
		Name: "native", Path: "native", Namespace: "", Package: "", Expects: nil,
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	bld, err := buildtest.Validate(touch(), component(), buildtest.Block{
		Common: build.Common{Cwd: "", Env: nil, Output: nil, Static: false}, Incremental: false,
		Rest: map[string]any{"file": "a.txt"},
	})
	require.NoError(t, err)
	assert.Equal(t, touchBuild{file: "a.txt"}, bld)
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	executable := component()

	executable.Expects = []build.Result{build.Program}

	module := component()

	module.Expects = []build.Result{build.Module, build.Data}

	noOutput := touch()

	noOutput.OutputFrom = build.OutputNone

	programs := touch()

	programs.Produces = []build.Result{build.Module, build.Program}

	tests := []struct {
		backend   build.Backend
		name      string
		component build.Component
		block     buildtest.Block
		field     string
		msg       string
	}{
		{touch(), "a type", component(), buildtest.Block{Rest: map[string]any{"file": int64(1)}},
			"file", "must be a string, not an integer"},
		{touch(), "an unknown field", component(), buildtest.Block{Rest: map[string]any{
			"file": "a", "make_target": "all",
		}}, "make_target", "unknown field"},
		{touch(), "New", component(), buildtest.Block{}, "",
			`file is required for backend "touch"`},
		{noOutput, "output", component(), buildtest.Block{
			Common: build.Common{Output: []build.OutputFile{{Path: "a", To: build.Module}}},
			Rest:   map[string]any{"file": "a"},
		}, "output", `is not a field of backend "touch"`},
		{touch(), "incremental", component(), buildtest.Block{
			Incremental: true, Rest: map[string]any{"file": "a"},
		}, "incremental", `is not a field of backend "touch"`},
		{touch(), "the type of an output", component(), buildtest.Block{
			Common: build.Common{Output: []build.OutputFile{
				{Path: "a", To: build.Module}, {Path: "b", To: build.Program},
			}},
			Rest: map[string]any{"file": "a"},
		}, "output[1].to", `backend "touch" makes no program`},
		{touch(), "what the build makes", component(), buildtest.Block{
			Rest: map[string]any{"file": "program"},
		}, "", `backend "touch" makes no program, but the build says it does`},
		{touch(), "what the component expects", executable, buildtest.Block{
			Rest: map[string]any{"file": "a"},
		}, "", `backend "touch" makes nothing the component expects`},
		{programs, "what the build makes for the component", module, buildtest.Block{
			Rest: map[string]any{"file": "program"},
		}, "", `the build makes a program, which the component does not expect`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildtest.Validate(test.backend, test.component, test.block)

			var fieldErr *build.FieldError

			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, test.field, fieldErr.Field)
			assert.Equal(t, test.msg, fieldErr.Msg)
		})
	}
}

// TestValidateOfABackendTheCoreRefuses: a backend the core would refuse to
// add is refused whatever the block, as tt would not start with it.
func TestValidateOfABackendTheCoreRefuses(t *testing.T) {
	t.Parallel()

	common := touch()

	common.Config = func() any {
		return &struct {
			Output string `toml:"output"`
		}{}
	}

	none := touch()

	none.Produces = nil

	for _, test := range []struct {
		backend build.Backend
		msg     string
	}{
		{common, `invalid build backend "touch": output is a field of every build block, ` +
			`not one of a backend`},
		{none, `invalid build backend "touch": the Produces must name module, data or ` +
			`program, once each`},
	} {
		_, err := buildtest.Validate(test.backend, component(), buildtest.Block{
			Rest: map[string]any{"file": "a"},
		})
		require.ErrorIs(t, err, build.ErrBackend)
		require.EqualError(t, err, test.msg)
	}
}

func TestValidateHook(t *testing.T) {
	t.Parallel()

	_, err := buildtest.ValidateHook(touch(), buildtest.Block{Rest: map[string]any{"file": "a"}})

	var fieldErr *build.FieldError

	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, `backend "touch" runs no hook`, fieldErr.Msg)

	hook := touch()

	hook.Hook = true

	bld, err := buildtest.ValidateHook(hook, buildtest.Block{Rest: map[string]any{"file": "a"}})
	require.NoError(t, err)
	assert.Equal(t, touchBuild{file: "a"}, bld)

	_, err = buildtest.ValidateHook(hook, buildtest.Block{})
	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, `file is required for backend "touch"`, fieldErr.Msg)
}

func TestNewHook(t *testing.T) {
	t.Parallel()

	hook := touch()

	hook.Hook = true

	harness := buildtest.NewHook(t, hook)
	facts := harness.Context

	assert.True(t, facts.Hook)
	assert.Empty(t, facts.Component.Name)
	assert.Empty(t, facts.Dirs.Output)
	assert.Empty(t, facts.Dirs.Work)
	assert.Empty(t, facts.Dirs.Component)
	assert.Equal(t, facts.Dirs.Project, facts.Dirs.Cwd)
	assert.Empty(t, facts.Dirs.Programs)
	assert.DirExists(t, facts.Dirs.Tools)

	out, err := facts.Output(facts.Command(t.Context(), "sh", "-c",
		`echo "${TT_PACKAGE}:${TT_OUTPUT_DIR-unset}:${TT_COMPONENT_NAME-unset}:`+
			`${TT_BUILD_DIR-unset}"; pwd`))
	require.NoError(t, err)

	cwd, err := filepath.EvalSymlinks(facts.Dirs.Project)
	require.NoError(t, err)
	assert.Equal(t, "demo:unset:unset:unset\n"+cwd+"\n", string(out))
}

func TestHarness(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, touch(), component())

	harness.Locked["sdk"] = "3.1.0"

	assert.DirExists(t, harness.Context.Dirs.Component)
	assert.DirExists(t, harness.Context.Dirs.Tools)
	assert.DirExists(t, harness.Context.Dirs.Programs)
	assert.NotEqual(t, harness.Context.Dirs.Tools, harness.Context.Dirs.Programs)
	assert.Equal(t, harness.Context.Dirs.Component, harness.Context.Dirs.Cwd)
	assert.Equal(t, "demo", harness.Context.Component.Package)

	require.NoError(t, harness.Run(t.Context(), touchBuild{file: "made.txt"}))

	data, err := os.ReadFile(filepath.Join(harness.Context.Dirs.Output, "made.txt"))
	require.NoError(t, err)
	assert.Equal(t, "native\n", string(data))

	assert.Equal(t, "err\ndone\n", harness.Printed())
	assert.Equal(t, []string{"sdk 3.1.0"}, harness.Warnings())
}

func TestHarnessBin(t *testing.T) {
	t.Parallel()

	facts := buildtest.New(t, touch(), component()).Context

	// The script is copied into place with no fork of the parallel tests
	// in between, which would hold it open for writing (ETXTBSY).
	script := filepath.Join(t.TempDir(), "tool")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho tool\n"), 0o700))

	tool := filepath.Join(facts.Dirs.Tools, "tool")
	require.NoError(t, build.CopyFile(tool, script))

	path, err := facts.LookPath("tool")
	require.NoError(t, err)
	assert.Equal(t, tool, path)

	out, err := facts.Output(facts.Command(t.Context(), "tool"))
	require.NoError(t, err)
	assert.Equal(t, "tool\n", string(out))

	_, err = facts.LookPath("no-such-tool-of-buildtest")
	require.ErrorIs(t, err, exec.ErrNotFound)

	err = facts.Run(facts.Command(t.Context(), "sh", "-c", "exit 3"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "running sh -c exit 3")
}

// TestHarnessChecksWhatTheBuildMade: a build that leaves a file in the
// directory of a type its backend does not produce fails its run, as it
// fails a build of the core.
func TestHarnessChecksWhatTheBuildMade(t *testing.T) {
	t.Parallel()

	data := touch()

	data.Produces = []build.Result{build.Data}

	harness := buildtest.New(t, data, component())

	err := harness.Run(t.Context(), touchBuild{file: "made.so"})
	require.ErrorIs(t, err, build.ErrNotProduced)
	require.EqualError(t, err, `buildtest: the build made what its backend does not produce: `+
		`backend "touch" makes no module: `+
		filepath.Join(harness.Context.Dirs.Output, "made.so"))

	// A file that was there and that the build left alone is not one it made.
	harness = buildtest.New(t, data, component())

	require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Output, "old.so"), nil,
		0o600))
	require.NoError(t, harness.Run(t.Context(), dataBuild{}))
}

// versionBuild records TT_VERSION in its Preflight and in its Run, as the
// Env and as its command sees it.
type versionBuild struct {
	seen *[]string
}

func (b versionBuild) Preflight(_ context.Context, c *build.Context) error {
	*b.seen = append(*b.seen, "preflight "+c.Env["TT_VERSION"])

	return nil
}

func (b versionBuild) Run(ctx context.Context, c *build.Context) error {
	out, err := c.Output(c.Command(ctx, "sh", "-c",
		`printf '%s|%s|%s' "$TT_VERSION" "${TT_TARANTOOL_PREFIX-unset}" `+
			`"${TT_TARANTOOL_INCLUDE_DIR-unset}"`))

	*b.seen = append(*b.seen, "run "+c.Env["TT_VERSION"]+" "+string(out))

	return err
}

// TestHarnessEnvironment: a build of a harness sees the TT_* variables a
// build of the core sees - TT_VERSION, empty in its Preflight as the core
// runs it before the version is known, and those of the Tarantool the test
// tells it, in the Context and the toolchain too.
func TestHarnessEnvironment(t *testing.T) {
	t.Parallel()

	var seen []string

	harness := buildtest.New(t, touch(), component())

	require.NoError(t, harness.Run(t.Context(), versionBuild{seen: &seen}))
	assert.Equal(t, []string{
		"preflight ", "run " + buildtest.Version + " " + buildtest.Version + "|unset|unset",
	}, seen)

	seen = nil

	harness.Context.Env["TT_VERSION"] = "1.2.3"
	harness.SetTarantool(build.Tarantool{
		Prefix: "/opt/tarantool", IncludeDir: "/opt/tarantool/include/tarantool",
		Version: "3.4.0",
	})

	require.NoError(t, harness.Run(t.Context(), versionBuild{seen: &seen}))
	assert.Equal(t, []string{
		"preflight ",
		"run 1.2.3 1.2.3|/opt/tarantool|/opt/tarantool/include/tarantool",
	}, seen)
	assert.Equal(t, "3.4.0", harness.Context.Tarantool.Version)
	assert.Equal(t, "/opt/tarantool/include/tarantool", harness.Context.Toolchain.IncludeDir)

	// A Tarantool tt knows no headers of: no TT_TARANTOOL_INCLUDE_DIR.
	harness.SetTarantool(build.Tarantool{Prefix: "/opt/t", IncludeDir: "", Version: ""})

	assert.Equal(t, "/opt/t", harness.Context.Env["TT_TARANTOOL_PREFIX"])
	assert.NotContains(t, harness.Context.Env, "TT_TARANTOOL_INCLUDE_DIR")
	assert.Equal(t, "/opt/t/include/tarantool", harness.Context.Toolchain.IncludeDir)
}

// dataBuild makes a data file.
type dataBuild struct{}

func (dataBuild) Run(_ context.Context, c *build.Context) error {
	return os.WriteFile(filepath.Join(c.Dirs.Share, "made.wasm"), nil, 0o600)
}

// TestHarnessOnlyThePathOfTheBuild: a PATH of the Env of the Context is the
// whole PATH of the build, after Tools, as the core has it: a program of the
// PATH of the test alone is not found, and a command of it fails to start.
func TestHarnessOnlyThePathOfTheBuild(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("sh")
	require.NoError(t, err)

	facts := buildtest.New(t, touch(), component()).Context

	facts.Env["PATH"] = t.TempDir()

	_, err = facts.LookPath("sh")
	require.ErrorIs(t, err, exec.ErrNotFound)

	err = facts.Run(facts.Command(t.Context(), "sh", "-c", "true"))
	require.ErrorIs(t, err, exec.ErrNotFound)
}
