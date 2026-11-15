package cc

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// component is a component the tests build, which wants its build to make
// results of the types wants (build.Component.Wants).
func component(wants ...build.Result) build.Component {
	return build.Component{
		Name: "native", Path: "native", Namespace: "demo", Package: "demo", Wants: wants,
	}
}

// block is a build block with the fields rest of the backend and env.
func block(rest map[string]any, env map[string]string) buildtest.Block {
	return buildtest.Block{
		Common:      build.Common{Cwd: "", Env: env, Output: nil, Static: false},
		Incremental: false,
		Fields:      rest,
	}
}

// module is the least block of a module m: its module and its sources, with
// the fields of extra.
func module(extra map[string]any) map[string]any {
	rest := map[string]any{"module": "m", "sources": []any{"m.c"}}

	maps.Copy(rest, extra)

	return rest
}

func TestBackends(t *testing.T) {
	t.Parallel()

	backends := Backends()
	require.Len(t, backends, 2)

	for i, name := range []string{CName, LuaCName} {
		backend := backends[i]

		assert.Equal(t, name, backend.Name)
		assert.Equal(t, []build.Result{build.Module}, backend.Produces)
		assert.False(t, backend.NoSourceLayout, name)
		assert.False(t, backend.Incremental, name)
		assert.False(t, backend.Hook, name)
		assert.Equal(t, build.OutputNone, backend.OutputFrom, name)
		assert.Empty(t, backend.Exclude, name)
	}

	fields, err := C().Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"module", "sources", "include_dirs", "libraries", "library_dirs", "defines", "cflags",
		"ldflags", "platforms",
	}, fields)
}

func TestConfig(t *testing.T) {
	t.Parallel()

	cfg, unknown, err := LuaC().Decode(map[string]any{
		"module":       "fast",
		"sources":      []any{"a.c", "b.c"},
		"include_dirs": []any{"include"},
		"libraries":    []any{"m"},
		"library_dirs": []any{"/opt/lib"},
		"defines":      []any{"NDEBUG"},
		"cflags":       []any{"-Wall"},
		"ldflags":      []any{"-Wl,--as-needed"},
		"platforms": map[string]any{
			"linux": map[string]any{"libraries": []any{"rt"}, "cflags": []any{"-pthread"}},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, unknown)
	assert.Equal(t, &Config{
		Module:      "fast",
		Sources:     []string{"a.c", "b.c"},
		IncludeDirs: []string{"include"},
		Libraries:   []string{"m"},
		LibraryDirs: []string{"/opt/lib"},
		Defines:     []string{"NDEBUG"},
		Cflags:      []string{"-Wall"},
		Ldflags:     []string{"-Wl,--as-needed"},
		Platforms: map[string]Overlay{
			"linux": {
				IncludeDirs: nil, Libraries: []string{"rt"}, LibraryDirs: nil, Defines: nil,
				Cflags: []string{"-pthread"}, Ldflags: nil,
			},
		},
	}, cfg)

	for _, backend := range Backends() {
		_, err := buildtest.Validate(backend, component(), block(module(nil), nil))
		require.NoError(t, err, backend.Name)
	}
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rest  map[string]any
		env   map[string]string
		field string
		msg   string
	}{
		{"no module", map[string]any{"sources": []any{"m.c"}}, nil,
			"", `module is required for backend "%s"`},
		{"no sources", map[string]any{"module": "m"}, nil,
			"", `sources is required for backend "%s"`},
		{"empty sources", map[string]any{"module": "m", "sources": []any{}}, nil,
			"", `sources is required for backend "%s"`},
		{"CC in env", module(nil), map[string]string{"CC": "clang"},
			"env.CC", `does nothing on backend "%s": the compiler is CC of the environment ` +
				"tt runs in, cc by default"},
		{"CFLAGS in env", module(nil), map[string]string{"CFLAGS": "-O0"},
			"env.CFLAGS", `does nothing on backend "%s": put the flags of the compiler into ` +
				"cflags; CFLAGS of the environment tt runs in comes after them"},
		{"CPPFLAGS in env", module(nil), map[string]string{"CPPFLAGS": "-DX"},
			"env.CPPFLAGS", `does nothing on backend "%s": put the macros into defines, the ` +
				"directories of headers into include_dirs and other flags of the preprocessor " +
				"into cflags"},
		{"LDFLAGS in env", module(nil), map[string]string{"LDFLAGS": "-s"},
			"env.LDFLAGS", `does nothing on backend "%s": put the flags of the link into ` +
				"ldflags; LDFLAGS of the environment tt runs in comes after them"},
		{"LIBS in env", module(nil), map[string]string{"LIBS": "-lm"},
			"env.LIBS", `does nothing on backend "%s": put the libraries into libraries and ` +
				"their directories into library_dirs"},
		{"LDLIBS in env", module(nil), map[string]string{"LDLIBS": "-lm"},
			"env.LDLIBS", `does nothing on backend "%s": put the libraries into libraries and ` +
				"their directories into library_dirs"},
		{"a platform of no OS", module(map[string]any{
			"platforms": map[string]any{"macos": map[string]any{}},
		}), nil, "platforms.macos", `"macos" is not an OS tt builds for (want linux or darwin)`},
		{"an empty cflag", module(map[string]any{"cflags": []any{"-Wall", ""}}), nil,
			"cflags", "must not contain an empty entry"},
		{"an empty ldflag", module(map[string]any{"ldflags": []any{""}}), nil,
			"ldflags", "must not contain an empty entry"},
		{"an empty cflag of a platform", module(map[string]any{
			"platforms": map[string]any{"linux": map[string]any{"cflags": []any{""}}},
		}), nil, "platforms.linux.cflags", "must not contain an empty entry"},
		{"an empty ldflag of a platform", module(map[string]any{
			"platforms": map[string]any{"darwin": map[string]any{"ldflags": []any{""}}},
		}), nil, "platforms.darwin.ldflags", "must not contain an empty entry"},
		{"cflags of a wrong type", module(map[string]any{"cflags": "-Wall"}), nil,
			"cflags", "must be an array, not a string"},
		{"a source of a wrong type", map[string]any{"module": "m", "sources": []any{int64(1)}},
			nil, "sources[0]", "must be a string, not an integer"},
		{"a platform of a wrong type", module(map[string]any{
			"platforms": map[string]any{"linux": []any{}},
		}), nil, "platforms.linux", "must be a table, not an array"},
		{"an unknown field", module(map[string]any{"make_target": "all"}), nil,
			"make_target", "unknown field"},
		{"an unknown field of a platform", module(map[string]any{
			"platforms": map[string]any{"linux": map[string]any{"sources": []any{"x.c"}}},
		}), nil, "platforms.linux.sources", "unknown field"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			for _, backend := range Backends() {
				_, err := buildtest.Validate(backend, component(),
					block(test.rest, test.env))

				var fieldErr *build.FieldError

				require.ErrorAs(t, err, &fieldErr, backend.Name)
				assert.Equal(t, test.field, fieldErr.Field, backend.Name)

				// %s in a message is the name of the backend.
				msg := strings.ReplaceAll(test.msg, "%s", backend.Name)
				assert.Equal(t, msg, fieldErr.Msg, backend.Name)
			}
		})
	}
}

// TestWants checks that neither backend builds a component that wants
// programs only nor runs a hook.
func TestWants(t *testing.T) {
	t.Parallel()

	for _, backend := range Backends() {
		_, err := buildtest.Validate(backend, component(build.Program),
			block(module(nil), nil))

		var fieldErr *build.FieldError

		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, `backend "`+backend.Name+`" makes nothing the component wants`,
			fieldErr.Msg)

		_, err = buildtest.ValidateHook(backend, block(module(nil), nil))
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, `backend "`+backend.Name+`" runs no hook`, fieldErr.Msg)
	}
}

// TestMissingHeaders checks that a build without the headers of Tarantool
// fails its preflight, and its run before the compiler is invoked: CC is a
// program that would fail loudly.
func TestMissingHeaders(t *testing.T) {
	t.Parallel()

	validated, err := buildtest.Validate(C(), component(), block(module(nil), nil))
	require.NoError(t, err)

	bld, ok := validated.(ccBuild)
	require.True(t, ok)

	bld.lookupEnv = func(key string) (string, bool) { return "/nonexistent/cc", key == "CC" }

	// The harness knows no Tarantool, and so no prefix of its headers.
	harness := buildtest.New(t, C(), component())

	preflighter := build.Preflighter(bld)

	err = preflighter.Preflight(t.Context(), harness.Context)
	require.ErrorIs(t, err, ErrMissingHeaders)
	assert.Contains(t, err.Error(), "tarantool development headers not found")
	assert.Contains(t, err.Error(), "tt binary install tarantool installs one")

	err = bld.Run(t.Context(), harness.Context)
	require.ErrorIs(t, err, ErrMissingHeaders)
	assert.Empty(t, harness.Printed())

	harness.SetTarantool(build.Tarantool{Prefix: "/tt", IncludeDir: "", Version: ""})
	require.NoError(t, preflighter.Preflight(t.Context(), harness.Context))
}

// fakeCC is a compiler that records its arguments one a line and creates
// the file of its -o.
const fakeCC = `#!/bin/sh
printf '%s\n' "$@" > "$TT_BUILD_DIR/args"
while [ $# -gt 0 ]; do
	if [ "$1" = -o ]; then : > "$2"; fi
	shift
done
`

// TestRun checks that a build runs the compiler of the toolchain in the
// working directory, with the module of the extension of the target in the
// output directory: .so, or .dylib on darwin, a dotted module a flat file.
func TestRun(t *testing.T) {
	t.Setenv("CC", "fakecc")
	t.Setenv("CFLAGS", "")
	t.Setenv("LDFLAGS", "")

	link := map[string][]string{
		"linux":  {"-shared"},
		"darwin": {"-bundle", "-undefined", "dynamic_lookup", "-all_load"},
	}

	for _, goos := range []string{"linux", "darwin"} {
		bld, err := buildtest.Validate(LuaC(), component(),
			block(map[string]any{"module": "a.b", "sources": []any{"a.c"}}, nil))
		require.NoError(t, err)

		harness := buildtest.New(t, LuaC(), component())
		facts := harness.Context

		require.NoError(t, os.WriteFile(filepath.Join(facts.Dirs.Tools, "fakecc"), []byte(fakeCC),
			0o700))

		harness.SetTarantool(build.Tarantool{Prefix: "/tt", IncludeDir: "", Version: ""})
		facts.Target.OS = goos

		require.NoError(t, harness.Run(t.Context(), bld))

		out := filepath.Join(facts.Dirs.Modules, "a.b"+facts.Target.ModuleExt())
		assert.FileExists(t, out)

		args, err := os.ReadFile(filepath.Join(facts.Dirs.Work, "args"))
		require.NoError(t, err)

		want := append([]string{"-fPIC", "-I/tt/include/tarantool"}, link[goos]...)

		assert.Equal(t, strings.Join(append(want, "-o", out, "a.c"), "\n")+"\n", string(args))
	}
}
