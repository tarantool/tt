package buildtest_test

import (
	"context"
	"os"
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

func (b touchBuild) Preflight(c *build.Context) error {
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

// touch is a backend of module components.
func touch() build.Backend {
	return build.Backend{
		Name:   "touch",
		Kinds:  []string{build.KindModule},
		Config: func() any { return new(touchConfig) },
		New: func(cfg any, _ build.Component, _ build.Common) (build.Build, error) {
			config, ok := cfg.(*touchConfig)
			if !ok {
				return nil, build.FieldErrorf("", "not a configuration of touch")
			}

			file := config.File
			if file == "" {
				return nil, build.FieldErrorf("", "file is required for backend %q", "touch")
			}

			return touchBuild{file: file}, nil
		},
		Incremental: false,
		OutputFrom:  build.OutputNone,
		Exclude:     nil,
	}
}

func component() build.Component {
	return build.Component{
		Name: "native", Kind: build.KindModule, Path: "native", Namespace: "", Package: "",
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

	executable.Kind = build.KindExecutable

	tests := []struct {
		name      string
		component build.Component
		block     buildtest.Block
		field     string
		msg       string
	}{
		{"a type", component(), buildtest.Block{Rest: map[string]any{"file": int64(1)}},
			"file", "must be a string, not an integer"},
		{"an unknown field", component(), buildtest.Block{Rest: map[string]any{
			"file": "a", "make_target": "all",
		}}, "make_target", "unknown field"},
		{"New", component(), buildtest.Block{}, "", `file is required for backend "touch"`},
		{"output", component(), buildtest.Block{
			Common: build.Common{Output: []string{"a"}}, Rest: map[string]any{"file": "a"},
		}, "output", `is not a field of backend "touch"`},
		{"incremental", component(), buildtest.Block{
			Incremental: true, Rest: map[string]any{"file": "a"},
		}, "incremental", `is not a field of backend "touch"`},
		{"a kind", executable, buildtest.Block{Rest: map[string]any{"file": "a"}},
			"", `backend "touch" does not build a component of kind "executable"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildtest.Validate(touch(), test.component, test.block)

			var fieldErr *build.FieldError

			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, test.field, fieldErr.Field)
			assert.Equal(t, test.msg, fieldErr.Msg)
		})
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

	harness := buildtest.NewHook(t)
	facts := harness.Context

	assert.True(t, facts.Hook)
	assert.Empty(t, facts.Component.Name)
	assert.Empty(t, facts.Dirs.Output)
	assert.Empty(t, facts.Dirs.Work)
	assert.Empty(t, facts.Dirs.Component)
	assert.Equal(t, facts.Dirs.Project, facts.Dirs.Cwd)
	assert.DirExists(t, facts.Dirs.Bin)

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

	harness := buildtest.New(t, component())

	harness.Locked["sdk"] = "3.1.0"

	assert.DirExists(t, harness.Context.Dirs.Component)
	assert.DirExists(t, harness.Context.Dirs.Bin)
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

	facts := buildtest.New(t, component()).Context

	tool := filepath.Join(facts.Dirs.Bin, "tool")
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\necho tool\n"), 0o700))

	path, err := facts.LookPath("tool")
	require.NoError(t, err)
	assert.Equal(t, tool, path)

	out, err := facts.Output(facts.Command(t.Context(), "tool"))
	require.NoError(t, err)
	assert.Equal(t, "tool\n", string(out))

	_, err = facts.LookPath("no-such-tool-of-buildtest")
	require.Error(t, err)

	err = facts.Run(facts.Command(t.Context(), "sh", "-c", "exit 3"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "running sh -c exit 3")
}
