package buildtest_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// writeConfig is the configuration of the write backend.
type writeConfig struct {
	// Text is what the build writes.
	Text string `toml:"text"`
	// Other has the script write other text than Run.
	Other bool `toml:"other"`
	// Tool is a tool the script runs.
	Tool string `toml:"tool"`
}

// writeBuild writes its text into out.txt of the directory of the modules,
// gen.txt of the working directory, which its output lists, and, for a
// hook, hook.txt of the project; its export writes the same with sh.
type writeBuild struct {
	config writeConfig
}

var _ build.Exporter = writeBuild{}

func (b writeBuild) Run(_ context.Context, c *build.Context) error {
	text := []byte(b.config.Text + " " + c.Env["TT_VERSION"])

	if c.Hook {
		return os.WriteFile(filepath.Join(c.Dirs.Project, "hook.txt"), text, 0o600)
	}

	err := os.WriteFile(filepath.Join(c.Dirs.Modules, "out.txt"), text, 0o600)
	if err == nil {
		err = os.WriteFile(filepath.Join(c.Dirs.Cwd, "gen.txt"), text, 0o600)
	}

	return err
}

func (b writeBuild) Export(export *build.ExportContext) (build.Export, error) {
	text := b.config.Text
	if b.config.Other {
		text += " other"
	}

	write := `printf '%s %s' ` + build.ShellQuote(text) + ` "$TT_VERSION"`

	script := write + ` > "$TT_OUTPUT_DIR/out.txt"` + "\n" + write + " > gen.txt"
	if export.Hook {
		script = write + ` > "$TT_PROJECT_ROOT/hook.txt"`
	}

	var tools []string

	if b.config.Tool != "" {
		tools = []string{b.config.Tool}
	}

	return build.Export{Script: script, Tools: tools, Lost: []string{"nothing at all"}}, nil
}

// writeBackend is the backend of writeBuild, which exports when exports is
// set.
func writeBackend(exports bool) build.Backend {
	return build.Backend{
		Name:     "write",
		Produces: build.Results(),
		Config:   func() any { return new(writeConfig) },
		New: build.Typed(func(cfg *writeConfig, _ build.Component, _ build.Common) (
			build.Build, error,
		) {
			if !exports {
				return touchBuild{file: "x"}, nil
			}

			return writeBuild{config: *cfg}, nil
		}),
		Incremental: false, OutputFrom: build.OutputCwd, Exclude: nil, NoSourceLayout: false,
		Hook: true, Cross: false, Exports: exports,
	}
}

// writeBlock is a block of write with the fields fields, whose output lists
// gen.txt as a data file.
func writeBlock(fields map[string]any) buildtest.Block {
	return buildtest.Block{
		Common: build.Common{
			Cwd: "", Env: nil, Static: false,
			Output: []build.OutputFile{{Path: "gen.txt", To: build.Data}},
		},
		Incremental: false, Fields: fields,
	}
}

// recorder is a testing.TB that records the errors a check reports instead
// of failing the test.
type recorder struct {
	testing.TB

	mu     sync.Mutex
	errors []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// TestCheckExport checks that the export of a build whose script makes what
// Run makes passes, with what output lists copied after each, and that one
// whose script makes another file fails, saying which.
func TestCheckExport(t *testing.T) {
	t.Parallel()

	var setups int

	export := buildtest.CheckExport(t, writeBackend(true), component(),
		writeBlock(map[string]any{"text": "it's $(x)"}), func(h *buildtest.Harness) {
			setups++

			h.Context.Env["TT_VERSION"] = "1.2.3"
		})

	assert.Equal(t, 2, setups)
	assert.Equal(t, []string{"nothing at all"}, export.Lost)

	failed := &recorder{TB: t, mu: sync.Mutex{}, errors: nil}

	buildtest.CheckExport(failed, writeBackend(true), component(),
		writeBlock(map[string]any{"text": "a", "other": true}), nil)

	require.Len(t, failed.errors, 2, failed.errors)
	assert.Contains(t, failed.errors[0], "the script of the export makes another data than "+
		"Run:\n  gen.txt: \"a 0.0.0-dev.0\" by Run, \"a other 0.0.0-dev.0\" by the script\n")
	assert.Contains(t, failed.errors[1], "another modules than Run:\n  out.txt: ")
}

// TestCheckExportHook checks the export of a hook against its Run by the
// files of the project.
func TestCheckExportHook(t *testing.T) {
	t.Parallel()

	buildtest.CheckExportHook(t, writeBackend(true), writeBlock(map[string]any{"text": "h"}),
		nil)

	failed := &recorder{TB: t, mu: sync.Mutex{}, errors: nil}

	buildtest.CheckExportHook(failed, writeBackend(true),
		writeBlock(map[string]any{"text": "h", "other": true}), nil)

	require.Len(t, failed.errors, 1, failed.errors)
	assert.Contains(t, failed.errors[0], "another project than Run:\n  hook.txt: ")
}

// TestHarnessExport checks the context an export is given and the variables
// its script runs with.
func TestHarnessExport(t *testing.T) {
	t.Parallel()

	_, err := buildtest.New(t, writeBackend(false), component()).Export(touchBuild{file: "x"})
	require.ErrorContains(t, err, `the build is no build.Exporter: backend "write"`)

	_, err = buildtest.New(t, writeBackend(true), component()).Export(touchBuild{file: "x"})
	require.ErrorIs(t, err, build.ErrBackend, "no Export of a backend that exports")

	exports := writeBackend(true)
	harness := buildtest.New(t, exports, component())

	harness.Locked["zig-tarantool"] = "0.3.0"

	var got *build.ExportContext

	bld := exportContextBuild{got: &got}

	export, err := harness.Export(bld)
	require.NoError(t, err)
	assert.Equal(t, "native", got.Component.Name)
	assert.Equal(t, "demo", got.Component.Package)
	assert.False(t, got.Hook)

	version, ok := got.LockedVersion("zig-tarantool")
	assert.True(t, ok)
	assert.Equal(t, "0.3.0", version)

	require.NoError(t, harness.RunExport(t.Context(), export))

	dirs := harness.Context.Dirs
	vars, err := os.ReadFile(filepath.Join(dirs.Modules, "vars"))
	require.NoError(t, err)

	want := []string{
		"TT_COMPONENT_NAME=native", "TT_OUTPUT_DIR=" + dirs.Modules,
		"TT_SHARE_DIR=" + dirs.Share, "TT_PROGRAMS_DIR=" + dirs.Programs,
		"TT_LINK_DIR=" + dirs.Link, "TT_LINK_ROOT=" + filepath.Dir(dirs.Link),
		"TT_STATE_DIR=" + dirs.State, "TT_BUILD_DIR=" + dirs.Work,
		"TT_MODULE_EXT=" + build.HostTarget().ModuleExt(), "PWD=" + dirs.Cwd,
	}
	assert.Equal(t, strings.Join(want, "\n")+"\n", string(vars))

	hook := buildtest.NewHook(t, exports)

	export, err = hook.Export(bld)
	require.NoError(t, err)
	assert.True(t, got.Hook)
	assert.Empty(t, got.Component.Name)

	export.Tools = []string{"no-such-tool-of-tt"}

	err = hook.RunExport(t.Context(), export)
	require.ErrorIs(t, err, build.ErrNoTool)
}

// exportContextBuild records the context of its export, whose script writes
// the variables of the export into the directory of the modules.
type exportContextBuild struct {
	got **build.ExportContext
}

func (exportContextBuild) Run(context.Context, *build.Context) error { return nil }

func (b exportContextBuild) Export(e *build.ExportContext) (build.Export, error) {
	*b.got = e

	return build.Export{Script: `{
	for name in TT_COMPONENT_NAME TT_OUTPUT_DIR TT_SHARE_DIR TT_PROGRAMS_DIR TT_LINK_DIR \
		TT_LINK_ROOT TT_STATE_DIR TT_BUILD_DIR TT_MODULE_EXT PWD; do
		eval "printf '%s=%s\n' $name \"\${$name-unset}\""
	done
} > "$TT_OUTPUT_DIR/vars"`, Tools: []string{"sh"}, Lost: nil}, nil
}
