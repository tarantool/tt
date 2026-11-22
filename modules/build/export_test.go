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
	// fields and output are the fields of the block.
	fields map[string]any
	output []sdkbuild.OutputFile
	// files are the files of the working directory of the build, by path.
	files map[string]string
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
// one at least (TestExportCovered).
func exportCases() []exportCase {
	tricky := "it's $HOME $(echo no) `echo no` \\ *\nand a line"

	return []exportCase{
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

			backend := backendNamed(t, test.backend)
			block := buildtest.Block{
				Common:      sdkbuild.Common{Cwd: "", Env: nil, Output: test.output, Static: false},
				Incremental: false,
				Fields:      test.fields,
			}

			setup := func(h *buildtest.Harness) {
				h.Context.Env["TT_VERSION"] = "1.2.3"

				for path, content := range test.files {
					file := filepath.Join(h.Context.Dirs.Cwd, path)

					require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
					require.NoError(t, os.WriteFile(file, []byte(content), 0o700))
				}
			}

			var export sdkbuild.Export

			if test.hook {
				export = buildtest.CheckExportHook(t, backend, block, setup)
			} else {
				export = buildtest.CheckExport(t, backend, exportComponent(), block, setup)
			}

			assert.Empty(t, export.Lost, "make and shell lose nothing")
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

	assert.Equal(t, []string{"make", "shell"}, exports)
}
