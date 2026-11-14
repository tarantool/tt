package cmake

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// module is a module component of the cmake backend.
func module() build.Component {
	return build.Component{
		Name: "answer", Kind: build.KindModule, Path: "answer", Namespace: "demo", Package: "demo",
	}
}

// program is an executable component of the cmake backend.
func program() build.Component {
	return build.Component{
		Name: "tool", Kind: build.KindExecutable, Path: "tool", Namespace: "", Package: "demo",
	}
}

// block is a cmake build block with output and the fields of rest.
func block(output []string, rest map[string]any) buildtest.Block {
	return buildtest.Block{
		Common:      build.Common{Cwd: "", Env: nil, Output: output, Static: false},
		Incremental: false,
		Rest:        rest,
	}
}

func TestBackend(t *testing.T) {
	t.Parallel()

	backend := Backend()

	assert.Equal(t, "cmake", backend.Name)
	assert.Equal(t, []string{build.KindModule, build.KindExecutable}, backend.Kinds)
	assert.Equal(t, build.OutputBackend, backend.OutputFrom)
	assert.False(t, backend.Incremental)
	assert.False(t, backend.Hook)
	assert.Empty(t, backend.Exclude)

	fields, err := backend.Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{"variables", "targets", "build_type"}, fields)
}

func TestValidate(t *testing.T) {
	t.Parallel()

	for _, comp := range []build.Component{module(), program()} {
		bld, err := buildtest.Validate(Backend(), comp, block(
			[]string{"lib/answer.so", "bin/answer.so"},
			map[string]any{
				"variables":  map[string]any{"WITH_SASL": "OFF", "CMAKE_PREFIX_PATH:PATH": "/d"},
				"targets":    []any{"answer", "ns.target+1"},
				"build_type": "Debug",
			}))
		require.NoError(t, err, comp.Kind)

		namer, ok := bld.(build.ProgramNamer)
		require.True(t, ok)

		// The files output lists, by base name, each once: the build copies
		// them so, and may not know them all.
		names, exhaustive := namer.Programs()
		assert.Equal(t, []string{"answer.so"}, names)
		assert.False(t, exhaustive)

		_, ok = bld.(build.Preflighter)
		assert.True(t, ok)
	}
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	output := []string{"answer.so"}

	tests := []struct {
		name  string
		block buildtest.Block
		field string
		msg   string
	}{
		{"no output", block(nil, nil), "",
			`output is required for backend "cmake": the files of its build tree the build takes`},
		{"an empty output", block([]string{"a.so", ""}, nil), "output",
			"must not contain an empty entry"},
		{"an absolute output", block([]string{"/b/a.so"}, nil), "output",
			`"/b/a.so" is not relative to the build tree`},
		{"a variable with =", block(output, map[string]any{
			"variables": map[string]any{"A=B": "x"},
		}), "variables", `"A=B" is not a variable name: letters, digits and _.+-, ` +
			"not - first, then :<TYPE> if it has one"},
		{"a variable with a dash first", block(output, map[string]any{
			"variables": map[string]any{"-DX": "x"},
		}), "variables", `"-DX" is not a variable name: letters, digits and _.+-, ` +
			"not - first, then :<TYPE> if it has one"},
		{"a variable of a lowercase type", block(output, map[string]any{
			"variables": map[string]any{"X:bool": "ON"},
		}), "variables", `"X:bool" is not a variable name: letters, digits and _.+-, ` +
			"not - first, then :<TYPE> if it has one"},
		{"the build type in variables", block(output, map[string]any{
			"variables": map[string]any{"CMAKE_BUILD_TYPE": "Debug"},
		}), "variables", "CMAKE_BUILD_TYPE is set by build_type"},
		{"the typed build type in variables", block(output, map[string]any{
			"variables": map[string]any{"CMAKE_BUILD_TYPE:STRING": "Debug"},
		}), "variables", "CMAKE_BUILD_TYPE is set by build_type"},
		{"a target with a space", block(output, map[string]any{
			"targets": []any{"a b"},
		}), "targets", `"a b" is not a target name`},
		{"a target like an option", block(output, map[string]any{
			"targets": []any{"--clean-first"},
		}), "targets", `"--clean-first" is not a target name`},
		{"a build type with a space", block(output, map[string]any{
			"build_type": "Rel With",
		}), "build_type", `"Rel With" is not a build type`},
		{"variables of a wrong type", block(output, map[string]any{
			"variables": "WITH_SASL=OFF",
		}), "variables", "must be a table, not a string"},
		{"a variable of a wrong type", block(output, map[string]any{
			"variables": map[string]any{"WITH_SASL": false},
		}), "variables.WITH_SASL", "must be a string, not a boolean"},
		{"a target of a wrong type", block(output, map[string]any{
			"targets": []any{"a", int64(1)},
		}), "targets[1]", "must be a string, not an integer"},
		{"a build type of a wrong type", block(output, map[string]any{
			"build_type": []any{"Debug"},
		}), "build_type", "must be a string, not an array"},
		{"an unknown field", block(output, map[string]any{
			"make_target": "all",
		}), "make_target", "unknown field"},
		{"incremental", buildtest.Block{
			Common:      build.Common{Cwd: "", Env: nil, Output: output, Static: false},
			Incremental: true,
			Rest:        nil,
		}, "incremental", `is not a field of backend "cmake"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildtest.Validate(Backend(), module(), test.block)

			var fieldErr *build.FieldError

			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, test.field, fieldErr.Field)
			assert.Equal(t, test.msg, fieldErr.Msg)
		})
	}
}

func TestPreflightNamesTheMissingTool(t *testing.T) {
	t.Parallel()

	bld, err := buildtest.Validate(Backend(), module(), block([]string{"a.so"}, nil))
	require.NoError(t, err)

	harness := buildtest.New(t, module())

	harness.Context.Env["PATH"] = t.TempDir()

	err = harness.Run(t.Context(), bld)
	require.ErrorIs(t, err, build.ErrNoTool)
	assert.Contains(t, err.Error(), `the cmake backend runs "cmake", which is not in PATH`)

	// The tools of the project are in the PATH of the build.
	tool := filepath.Join(harness.Context.Dirs.Bin, "cmake")
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o700))

	preflighter, ok := bld.(build.Preflighter)
	require.True(t, ok)
	require.NoError(t, preflighter.Preflight(harness.Context))
}
