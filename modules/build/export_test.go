package build_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"

	"github.com/tarantool/tt/modules/build"
)

// exportCase is a build of a backend whose builds are made without tt
// (sdkbuild.Backend.Exports), whose export TestExport checks against its Run
// with buildtest.CheckExport: a component of the package demo, or a hook.
type exportCase struct {
	// name names the case.
	name string
	// backend is the name of the backend.
	backend string
	// hook makes the build that of a lifecycle hook.
	hook bool
	// fields, output and static are the fields of the block.
	fields map[string]any
	output []sdkbuild.OutputFile
	static bool
	// files are the files of the working directory of the build, by path.
	files map[string]string
	// integration runs the case under the integration build tag alone: a
	// build of a toolchain that compiles, which a run of the tests of the
	// module without it skips.
	integration bool
	// setup, when set, prepares each harness after files are written, as
	// files cannot: the Tarantool of the build, what another component would
	// have made for linking, a file of a path of the harness. It may skip
	// the test.
	setup func(t *testing.T, h *buildtest.Harness)
	// observe, when set, is what the build of a harness made that the
	// comparison of the files does not see - what a program it made prints:
	// Run and the script are to make the same.
	observe func(t *testing.T, h *buildtest.Harness) string
	// lost says whether the export lacks something against Run.
	lost bool
}

// exportComponent is the component of the cases of exportCases.
func exportComponent() sdkbuild.Component {
	return sdkbuild.Component{
		Name: "native", Path: "native", Namespace: "demo", Package: "demo", Wants: nil,
	}
}

// exportMakefile makes a module and a data file of TT_OUTPUT_DIR, a data
// file of the working directory, which output lists, and a program, of what
// make is told: the variables of tt and those of the command line.
const exportMakefile = `NAME ?= unset

all:
	printf '%s %s %s\n' "$(NAME)" "$$TT_PACKAGE" "$$TT_COMPONENT_NAME" > $(TT_OUTPUT_DIR)/mod.so
	printf '%s\n' "$$TT_VERSION" > data.json
	printf '#!/bin/sh\necho %s\n' "$(NAME)" > tool
	chmod +x tool

gen:
	printf '%s %s\n' "$$TT_PACKAGE" "$${TT_OUTPUT_DIR-unset}" > gen.txt
`

// exportScript is a script the cases of shell run with sh, of its arguments:
// it writes the first into a module, keeps the second in TT_BUILD_DIR on
// the way, and makes a program and a header for linking, which output lists.
const exportScript = `set -eu
printf '%s\n' "$1" > "$TT_OUTPUT_DIR/args.so"
printf '%s\n' "$2" > "$TT_BUILD_DIR/second"
cp "$TT_BUILD_DIR/second" second.txt
printf '#!/bin/sh\nexit 0\n' > tool
chmod +x tool
printf '#define X 1\n' > x.h
`

// exportCases are the cases of TestExport: each backend that exports has
// one at least (TestExportCovered). Those of c and lua-c are ccExportCases.
func exportCases() []exportCase {
	tricky := "it's $HOME $(echo no) `echo no` \\ *\nand a line"

	return append([]exportCase{
		{
			// A flag of make is a word of sh; make reads a $ in it itself.
			name: "make", backend: "make",
			fields: map[string]any{"make_target": "all", "flags": []any{"NAME=it's a * \\ word"}},
			output: []sdkbuild.OutputFile{
				{Path: "data.json", To: sdkbuild.Data}, {Path: "tool", To: sdkbuild.Program},
			},
			files: map[string]string{"Makefile": exportMakefile},
		},
		{
			name: "make with an entrypoint", backend: "make",
			fields: map[string]any{"make_target": "all", "entrypoint": "tt.mk"},
			output: []sdkbuild.OutputFile{{Path: "tool", To: sdkbuild.Program}},
			files:  map[string]string{"tt.mk": exportMakefile},
		},
		{
			name: "make hook", backend: "make", hook: true,
			fields: map[string]any{"make_target": "gen"},
			files:  map[string]string{"Makefile": exportMakefile},
		},
		{
			name: "shell", backend: "shell",
			fields: map[string]any{
				"command": "sh", "args": []any{"./build.sh", tricky, "-x y"},
			},
			output: []sdkbuild.OutputFile{
				{Path: "second.txt", To: sdkbuild.Data}, {Path: "tool", To: sdkbuild.Program},
				{Path: "x.h", To: sdkbuild.Link},
			},
			files: map[string]string{"build.sh": exportScript},
		},
		{
			name: "shell of a path", backend: "shell",
			fields: map[string]any{"command": "./build.sh", "args": []any{"a", "b"}},
			files:  map[string]string{"build.sh": "#!/bin/sh\n" + exportScript},
		},
		{
			name: "shell hook", backend: "shell", hook: true,
			fields: map[string]any{"command": "sh", "args": []any{
				"-c", `printf '%s %s\n' "$1" "${TT_COMPONENT_NAME-unset}" > gen.txt`, "sh", tricky,
			}},
		},
		{
			// A program named as a builtin of sh is run, as Run runs it.
			name: "shell of a program of the name of a builtin", backend: "shell",
			fields: map[string]any{"command": "test", "args": []any{"-n", tricky}},
			setup:  builtinProgram("test", `printf '%s\n' "$@" > "$TT_OUTPUT_DIR/test.so"`),
		},
		{
			name: "shell hook of a program of the name of a builtin", backend: "shell", hook: true,
			fields: map[string]any{"command": "true", "args": []any{"gen.txt"}},
			setup:  builtinProgram("true", `printf 'ran\n' > "$1"`),
		},
		{
			// The prefix of Tarantool after that of the block on
			// CMAKE_PREFIX_PATH, where find_path finds module.h.
			name: "cmake", backend: "cmake", integration: true,
			fields: map[string]any{
				"build_type": "Debug", "targets": []any{"answer", "fnv", "tool"},
				"variables": map[string]any{"WORD": exportWord, "CMAKE_PREFIX_PATH": "/none"},
			},
			output: cmakeOutput(), files: cmakeProject(true),
			setup: func(t *testing.T, h *buildtest.Harness) {
				t.Helper()
				requireCC(t)
				exportTarantool(t, h)
			},
			observe: func(t *testing.T, h *buildtest.Harness) string {
				t.Helper()

				return runProgram(t, h, "tool")
			},
		},
		{
			// No Tarantool, no CMAKE_PREFIX_PATH; the jobs cmake reads.
			name: "cmake of no Tarantool", backend: "cmake", integration: true,
			output: cmakeOutput(), files: cmakeProject(false),
			setup: func(t *testing.T, h *buildtest.Harness) {
				t.Helper()
				requireCC(t)

				h.Context.Env["CMAKE_BUILD_PARALLEL_LEVEL"] = "2"
			},
		},
		{
			name: "go", backend: "go", integration: true,
			fields: map[string]any{
				"name": "hello", "tags": []any{"demo"},
				"ldflags": []any{"-X", "main.word=" + exportWord},
			},
			files: goProject(false),
			observe: func(t *testing.T, h *buildtest.Harness) string {
				t.Helper()

				return runProgram(t, h, "hello")
			},
		},
		{
			name: "go with cgo", backend: "go", integration: true,
			fields: map[string]any{"name": "hello", "cgo": true},
			files:  goProject(true),
			setup: func(t *testing.T, _ *buildtest.Harness) {
				t.Helper()
				requireCC(t)
			},
			observe: func(t *testing.T, h *buildtest.Harness) string {
				t.Helper()

				return runProgram(t, h, "hello")
			},
		},
		{
			// A profile of the package's own, the directory cargo builds it
			// into.
			name: "cargo", backend: "cargo", integration: true, lost: true,
			fields: map[string]any{
				"module": "demo", "bin": "demo-cli", "features": []any{"extra"}, "profile": "fast",
			},
			files: cargoProject(),
			observe: func(t *testing.T, h *buildtest.Harness) string {
				t.Helper()

				return runProgram(t, h, "demo-cli")
			},
		},
		{
			name: "cargo library under its own name", backend: "cargo", integration: true,
			lost: true, files: cargoProject(),
		},
		{
			// For the musl target of the machine.
			name: "cargo static program", backend: "cargo", integration: true, lost: true,
			fields: map[string]any{"bin": "demo-cli"}, static: true, files: cargoProject(),
			setup: func(t *testing.T, _ *buildtest.Harness) {
				t.Helper()
				requireMusl(t)
			},
			observe: func(t *testing.T, h *buildtest.Harness) string {
				t.Helper()

				return runProgram(t, h, "demo-cli")
			},
		},
		{
			name: "zig", backend: "zig", integration: true,
			fields: map[string]any{
				"module": "answer", "bin": "answer-cli", "optimize": "Debug",
				"options": map[string]any{"word": exportWord},
			},
			files: zigProject(false),
			observe: func(t *testing.T, h *buildtest.Harness) string {
				t.Helper()

				return runProgram(t, h, "answer-cli")
			},
		},
		{
			name: "zig of zig-tarantool", backend: "zig", integration: true,
			fields: map[string]any{"module": "hello", "bin": "hello-client", "optimize": "Debug"},
			setup:  zigTarantoolProject,
		},
		{
			// For the musl target of the machine, as the build.zig takes
			// -Dtarget.
			name: "zig static program", backend: "zig", integration: true,
			fields: map[string]any{"bin": "answer-cli", "optimize": "Debug"}, static: true,
			files: zigProject(true),
			setup: func(t *testing.T, _ *buildtest.Harness) {
				t.Helper()
				requireLinux(t)
			},
			observe: func(t *testing.T, h *buildtest.Harness) string {
				t.Helper()

				return runProgram(t, h, "answer-cli")
			},
		},
	}, ccExportCases()...)
}

// builtinProgram returns the setup of a case that puts a program name, a
// script of sh of body, first in the PATH of the build: one named as a
// builtin of sh, which the script of shell is to run as Run runs it.
func builtinProgram(name, body string) func(t *testing.T, h *buildtest.Harness) {
	return func(t *testing.T, h *buildtest.Harness) {
		t.Helper()

		tools := h.Context.Dirs.Tools

		require.NoError(t, os.MkdirAll(tools, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(tools, name),
			[]byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o755))
	}
}

// backendNamed returns the backend of the module named name.
func backendNamed(t *testing.T, name string) sdkbuild.Backend {
	t.Helper()

	index := slices.IndexFunc(build.Backends(),
		func(backend sdkbuild.Backend) bool { return backend.Name == name })
	require.GreaterOrEqual(t, index, 0, "no backend %q", name)

	return build.Backends()[index]
}

// TestExport checks the export of each case against its Run: the script
// makes what Run makes, in the same places.
func TestExport(t *testing.T) {
	t.Parallel()

	for _, test := range exportCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if test.integration && !integrationTag {
				t.Skip("a build of a toolchain, run with -tags integration")
			}

			backend := backendNamed(t, test.backend)
			block := buildtest.Block{
				Common: sdkbuild.Common{
					Cwd: "", Env: nil, Output: test.output, Static: test.static,
				},
				Incremental: false,
				Fields:      test.fields,
			}

			// The harnesses of Run and of the script, in this order.
			var harnesses []*buildtest.Harness

			setup := func(harness *buildtest.Harness) {
				harness.Context.Env["TT_VERSION"] = "1.2.3"

				for path, content := range test.files {
					file := filepath.Join(harness.Context.Dirs.Cwd, path)

					require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
					require.NoError(t, os.WriteFile(file, []byte(content), 0o700))
				}

				if test.setup != nil {
					test.setup(t, harness)
				}

				harnesses = append(harnesses, harness)
			}

			var export sdkbuild.Export

			if test.hook {
				export = buildtest.CheckExportHook(t, backend, block, setup)
			} else {
				export = buildtest.CheckExport(t, backend, exportComponent(), block, setup)
			}

			assert.Equal(t, test.lost, len(export.Lost) > 0, "what the script lacks: %q",
				export.Lost)

			if test.observe != nil {
				require.Len(t, harnesses, 2)
				assert.Equal(t, test.observe(t, harnesses[0]), test.observe(t, harnesses[1]),
					"what Run made, and what the script made")
			}
		})
	}
}

// TestExportCovered fails for a backend of the module whose builds are made
// without tt and that no case of exportCases checks with CheckExport: a
// backend that says so is to prove it, so that a script that makes
// something else than Run fails here rather than under LuaRocks.
func TestExportCovered(t *testing.T) {
	t.Parallel()

	covered := map[string]bool{}

	for _, test := range exportCases() {
		backend := backendNamed(t, test.backend)
		assert.True(t, backend.Exports, "the case %q is of backend %q, which does not export",
			test.name, test.backend)

		covered[test.backend] = true
	}

	var exports []string

	for _, backend := range build.Backends() {
		if !backend.Exports {
			continue
		}

		exports = append(exports, backend.Name)

		assert.True(t, covered[backend.Name], "backend %q says its builds are made without "+
			"tt, and no case of exportCases checks its export with buildtest.CheckExport",
			backend.Name)
	}

	assert.Equal(t, []string{"make", "shell", "c", "cargo", "cmake", "go", "lua-c", "zig"},
		exports)
}
