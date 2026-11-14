package basic

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// component is a module component the tests build.
func component(kind string) build.Component {
	return build.Component{
		Name: "native", Kind: kind, Path: "native", Namespace: "demo", Package: "demo",
	}
}

// block is a build block with the fields rest of the backend and output.
func block(rest map[string]any, output ...string) buildtest.Block {
	return buildtest.Block{
		Common:      build.Common{Cwd: "", Env: nil, Output: output, Static: false},
		Incremental: false,
		Rest:        rest,
	}
}

func TestBackends(t *testing.T) {
	t.Parallel()

	backends := Backends()
	require.Len(t, backends, 2)

	for i, name := range []string{MakeName, ShellName} {
		backend := backends[i]

		assert.Equal(t, name, backend.Name)
		assert.Equal(t, []string{build.KindModule, build.KindExecutable}, backend.Kinds)
		assert.True(t, backend.Incremental, name)
		assert.True(t, backend.Hook, name)
		assert.Equal(t, build.OutputCwd, backend.OutputFrom, name)
		assert.Empty(t, backend.Exclude, name)
	}
}

func TestMakeConfig(t *testing.T) {
	t.Parallel()

	cfg, unknown, err := Make().Decode(map[string]any{
		"make_target": "all", "entrypoint": "Makefile.tt", "flags": []any{"-j4", "V=1"},
	})
	require.NoError(t, err)
	assert.Empty(t, unknown)
	assert.Equal(t, &MakeConfig{
		MakeTarget: "all", Entrypoint: "Makefile.tt", Flags: []string{"-j4", "V=1"},
	}, cfg)

	fields, err := Make().Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{"make_target", "entrypoint", "flags"}, fields)
}

func TestShellConfig(t *testing.T) {
	t.Parallel()

	cfg, unknown, err := Shell().Decode(map[string]any{
		"command": "./build.sh", "args": []any{"-v", "fast"},
	})
	require.NoError(t, err)
	assert.Empty(t, unknown)
	assert.Equal(t, &ShellConfig{Command: "./build.sh", Args: []string{"-v", "fast"}}, cfg)

	fields, err := Shell().Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{"command", "args"}, fields)
}

// TestPrograms checks that a make or shell build of an executable component
// names the files output lists, by base name, and that the list is not
// exhaustive: a target may write other programs into TT_OUTPUT_DIR.
func TestPrograms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		backend build.Backend
		rest    map[string]any
	}{
		{Make(), map[string]any{"make_target": "all"}},
		{Shell(), map[string]any{"command": "./build.sh"}},
	}

	for _, test := range tests {
		t.Run(test.backend.Name, func(t *testing.T) {
			t.Parallel()

			bld, err := buildtest.Validate(test.backend, component(build.KindExecutable),
				block(test.rest, "out/gw", "out/gw-cli", "gw"))
			require.NoError(t, err)

			namer, ok := bld.(build.ProgramNamer)
			require.True(t, ok)

			names, exhaustive := namer.Programs()
			assert.Equal(t, []string{"gw", "gw-cli"}, names)
			assert.False(t, exhaustive)
		})
	}
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend build.Backend
		rest    map[string]any
		field   string
		msg     string
	}{
		{
			"no make_target", Make(), map[string]any{"flags": []any{"-j4"}},
			"", `make_target is required for backend "make"`,
		},
		{
			"an empty make_target", Make(), map[string]any{"make_target": ""},
			"", `make_target is required for backend "make"`,
		},
		{
			"no command", Shell(), map[string]any{"args": []any{"x"}},
			"", `command is required for backend "shell"`,
		},
		{
			"a make_target of a wrong type", Make(), map[string]any{"make_target": int64(1)},
			"make_target", "must be a string, not an integer",
		},
		{
			"flags of a wrong type", Make(),
			map[string]any{"make_target": "all", "flags": "-j4"},
			"flags", "must be an array, not a string",
		},
		{
			"an arg of a wrong type", Shell(),
			map[string]any{"command": "sh", "args": []any{"-c", true}},
			"args[1]", "must be a string, not a boolean",
		},
		{
			"a field of shell on make", Make(),
			map[string]any{"make_target": "all", "command": "sh"},
			"command", "unknown field",
		},
		{
			"a field of make on shell", Shell(),
			map[string]any{"command": "sh", "make_target": "all"},
			"make_target", "unknown field",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			for _, validate := range []func() (build.Build, error){
				func() (build.Build, error) {
					return buildtest.Validate(test.backend, component(build.KindModule),
						block(test.rest))
				},
				func() (build.Build, error) {
					return buildtest.ValidateHook(test.backend, block(test.rest))
				},
			} {
				_, err := validate()

				var fieldErr *build.FieldError

				require.ErrorAs(t, err, &fieldErr)
				assert.Equal(t, test.field, fieldErr.Field)
				assert.Equal(t, test.msg, fieldErr.Msg)
			}
		})
	}
}

// TestValidate checks that both backends build both kinds of components and
// run hooks, with output and incremental.
func TestValidate(t *testing.T) {
	t.Parallel()

	for _, backend := range Backends() {
		rest := map[string]any{"make_target": "all"}
		if backend.Name == ShellName {
			rest = map[string]any{"command": "sh"}
		}

		with := block(rest, "out/x.so")

		with.Incremental = true

		for _, kind := range []string{build.KindModule, build.KindExecutable} {
			_, err := buildtest.Validate(backend, component(kind), with)
			require.NoError(t, err, backend.Name, kind)
		}

		_, err := buildtest.ValidateHook(backend, with)
		require.NoError(t, err, backend.Name)
	}
}

// requireMake skips the test where make is not installed.
func requireMake(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not available")
	}
}

func TestMakeRunsTargetIntoOutputDir(t *testing.T) {
	t.Parallel()
	requireMake(t)

	bld, err := buildtest.Validate(Make(), component(build.KindModule),
		block(map[string]any{"make_target": "build"}))
	require.NoError(t, err)

	harness := buildtest.New(t, component(build.KindModule))
	facts := harness.Context

	makefile := "build:\n\tprintf x > $(TT_OUTPUT_DIR)/mod.so\n"
	require.NoError(t, os.WriteFile(filepath.Join(facts.Dirs.Cwd, "Makefile"), []byte(makefile),
		0o600))

	require.NoError(t, harness.Run(t.Context(), bld))
	assert.FileExists(t, filepath.Join(facts.Dirs.Output, "mod.so"))
}

func TestMakeCustomEntrypointAndFlags(t *testing.T) {
	t.Parallel()
	requireMake(t)

	bld, err := buildtest.Validate(Make(), component(build.KindModule),
		block(map[string]any{
			"make_target": "build", "entrypoint": "tt.mk", "flags": []any{"NAME=custom"},
		}))
	require.NoError(t, err)

	harness := buildtest.New(t, component(build.KindModule))
	facts := harness.Context

	makefile := "build:\n\tprintf x > $(TT_OUTPUT_DIR)/$(NAME).so\n"
	require.NoError(t, os.WriteFile(filepath.Join(facts.Dirs.Cwd, "tt.mk"), []byte(makefile),
		0o600))

	require.NoError(t, harness.Run(t.Context(), bld))
	assert.FileExists(t, filepath.Join(facts.Dirs.Output, "custom.so"))
}

func TestShellWritesToOutputDir(t *testing.T) {
	t.Parallel()

	bld, err := buildtest.Validate(Shell(), component(build.KindModule),
		block(map[string]any{
			"command": "sh",
			"args":    []any{"-c", `printf "$TT_COMPONENT_NAME" > "$TT_OUTPUT_DIR/artifact.txt"`},
		}))
	require.NoError(t, err)

	harness := buildtest.New(t, component(build.KindModule))

	require.NoError(t, harness.Run(t.Context(), bld))

	got, err := os.ReadFile(filepath.Join(harness.Context.Dirs.Output, "artifact.txt"))
	require.NoError(t, err)
	assert.Equal(t, "native", string(got))
}

// TestShellRunsInCwd checks that the command runs in the working directory
// of the build, where output names the files it makes.
func TestShellRunsInCwd(t *testing.T) {
	t.Parallel()

	bld, err := buildtest.Validate(Shell(), component(build.KindModule),
		block(map[string]any{"command": "sh", "args": []any{"-c", "printf x > artifact.so"}},
			"artifact.so"))
	require.NoError(t, err)

	harness := buildtest.New(t, component(build.KindModule))

	require.NoError(t, harness.Run(t.Context(), bld))
	assert.FileExists(t, filepath.Join(harness.Context.Dirs.Cwd, "artifact.so"))
	// The core copies what output lists, not the backend.
	assert.NoFileExists(t, filepath.Join(harness.Context.Dirs.Output, "artifact.so"))
}

func TestShellNonZeroExitIsError(t *testing.T) {
	t.Parallel()

	bld, err := buildtest.Validate(Shell(), component(build.KindModule),
		block(map[string]any{"command": "sh", "args": []any{"-c", "echo boom >&2; exit 1"}}))
	require.NoError(t, err)

	harness := buildtest.New(t, component(build.KindModule))

	require.Error(t, harness.Run(t.Context(), bld))
	assert.Contains(t, harness.Printed(), "boom")
}

func TestMakeNonZeroExitIsError(t *testing.T) {
	t.Parallel()
	requireMake(t)

	bld, err := buildtest.Validate(Make(), component(build.KindModule),
		block(map[string]any{"make_target": "build"}))
	require.NoError(t, err)

	harness := buildtest.New(t, component(build.KindModule))

	require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Cwd, "Makefile"),
		[]byte("build:\n\texit 3\n"), 0o600))

	require.Error(t, harness.Run(t.Context(), bld))
}

// hookEnv is what a hook prints of its environment: the variables of the
// reduced contract a hook has, and those it has not, "unset".
const hookEnv = `echo "${TT_PACKAGE}:${TT_OUTPUT_DIR-unset}:${TT_COMPONENT_NAME-unset}:` +
	`${TT_BUILD_DIR-unset}" > hook.out`

// TestShellHook checks that a shell hook runs in the project with the
// reduced contract: no TT_OUTPUT_DIR, TT_COMPONENT_NAME and TT_BUILD_DIR.
func TestShellHook(t *testing.T) {
	t.Parallel()

	bld, err := buildtest.ValidateHook(Shell(), block(map[string]any{
		"command": "sh", "args": []any{"-c", hookEnv},
	}))
	require.NoError(t, err)

	harness := buildtest.NewHook(t)

	require.NoError(t, harness.Run(t.Context(), bld))

	got, err := os.ReadFile(filepath.Join(harness.Context.Dirs.Project, "hook.out"))
	require.NoError(t, err)
	assert.Equal(t, "demo:unset:unset:unset\n", string(got))
}

// TestMakeHook checks that a make hook runs make in the project with the
// reduced contract.
func TestMakeHook(t *testing.T) {
	t.Parallel()
	requireMake(t)

	bld, err := buildtest.ValidateHook(Make(), block(map[string]any{
		"make_target": "gen",
	}))
	require.NoError(t, err)

	harness := buildtest.NewHook(t)
	project := harness.Context.Dirs.Project

	makefile := "gen:\n\t" + `@echo "$${TT_PACKAGE}:$${TT_OUTPUT_DIR-unset}:` +
		`$${TT_COMPONENT_NAME-unset}:$${TT_BUILD_DIR-unset}" > hook.out` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(project, "Makefile"), []byte(makefile), 0o600))

	require.NoError(t, harness.Run(t.Context(), bld))

	got, err := os.ReadFile(filepath.Join(project, "hook.out"))
	require.NoError(t, err)
	assert.Equal(t, "demo:unset:unset:unset\n", string(got))
}
