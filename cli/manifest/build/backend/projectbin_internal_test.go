package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/internal/execfile"
)

// writeProjectProgram puts an executable shell script named name into the
// project's .tarantool/bin and returns the project root.
func writeProjectProgram(t *testing.T, name, script string) string {
	t.Helper()

	project := t.TempDir()
	dir := ProjectBinDir(project)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	// The tests run the script while parallel tests fork: execfile keeps the
	// forks off the descriptor it writes it through.
	require.NoError(t, execfile.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))

	return project
}

func TestShellRunsProgramFromProjectBin(t *testing.T) {
	t.Parallel()

	project := writeProjectProgram(t, "tt-test-project-tool",
		"#!/bin/sh\nprintf '%s' \"$0 $*\" > \"$TT_OUTPUT_DIR/ran.txt\"\n")
	out := t.TempDir()

	build := manifest.Build{
		Backend: BackendShell,
		Command: "tt-test-project-tool",
		Args:    []string{"build"},
	}

	err := shellBackend{}.Run(context.Background(), build, project,
		Env{OutputDir: out, ProjectRoot: project})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(out, "ran.txt"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ProjectBinDir(project), "tt-test-project-tool")+" build",
		string(got))
}

func TestEnvironPutsProjectBinFirst(t *testing.T) {
	t.Parallel()

	project := writeProjectProgram(t, "tool", "#!/bin/sh\n")

	env := Env{ProjectRoot: project, Extra: map[string]string{"PATH": "/opt/extra/bin"}}

	path, ok := lookupPath(env.environ())
	require.True(t, ok)
	assert.Equal(t, ProjectBinDir(project)+string(os.PathListSeparator)+"/opt/extra/bin", path)
}

func TestEnvironWithoutProjectBinKeepsPath(t *testing.T) {
	t.Parallel()

	env := Env{ProjectRoot: t.TempDir(), Extra: map[string]string{"PATH": "/opt/extra/bin"}}

	path, ok := lookupPath(env.environ())
	require.True(t, ok)
	assert.Equal(t, "/opt/extra/bin", path)
}

func TestLookPathInSkipsRelativeEntries(t *testing.T) {
	t.Parallel()

	project := writeProjectProgram(t, "tool", "#!/bin/sh\n")
	relative, err := filepath.Rel(mustGetwd(t), ProjectBinDir(project))
	require.NoError(t, err)

	assert.Equal(t, "tool", lookPathIn("tool", []string{"PATH=" + relative}))
	assert.Equal(t, filepath.Join(ProjectBinDir(project), "tool"),
		lookPathIn("tool", []string{"PATH=" + strings.Join(
			[]string{relative, ProjectBinDir(project)}, string(os.PathListSeparator))}))
	assert.Equal(t, "./tool", lookPathIn("./tool", []string{"PATH=" + ProjectBinDir(project)}))
}

func mustGetwd(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)

	return wd
}
