package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

func TestShellWritesToOutputDir(t *testing.T) {
	t.Parallel()

	out := t.TempDir()

	b := manifest.Build{
		Backend: BackendShell,
		Command: "sh",
		Args:    []string{"-c", `printf hi > "$TT_OUTPUT_DIR/artifact.txt"`},
	}

	err := shellBackend{}.Run(context.Background(), b, t.TempDir(), Env{OutputDir: out})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(out, "artifact.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hi", string(got))
}

func TestShellCopiesDeclaredOutputs(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	cwd := t.TempDir()

	// The command writes into cwd; the declared output is copied (flat) out.
	b := manifest.Build{
		Backend: BackendShell,
		Command: "sh",
		Args:    []string{"-c", "printf x > artifact.so"},
		Output:  []string{"artifact.so"},
	}

	err := shellBackend{}.Run(context.Background(), b, cwd, Env{OutputDir: out})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(out, "artifact.so"))
}

func TestShellNonZeroExitIsError(t *testing.T) {
	t.Parallel()

	b := manifest.Build{Backend: BackendShell, Command: "sh", Args: []string{"-c", "exit 1"}}

	err := shellBackend{}.Run(context.Background(), b, t.TempDir(), Env{OutputDir: t.TempDir()})
	require.Error(t, err)
}

func TestShellMissingDeclaredOutputIsError(t *testing.T) {
	t.Parallel()

	// The command succeeds but never produces the declared output.
	b := manifest.Build{
		Backend: BackendShell,
		Command: "sh",
		Args:    []string{"-c", "true"},
		Output:  []string{"ghost.so"},
	}

	err := shellBackend{}.Run(context.Background(), b, t.TempDir(), Env{OutputDir: t.TempDir()})
	require.Error(t, err)
}

func TestShellRejectsRelativeCwd(t *testing.T) {
	t.Parallel()

	b := manifest.Build{Backend: BackendShell, Command: "true"}

	err := shellBackend{}.Run(context.Background(), b, "rel/dir", Env{OutputDir: t.TempDir()})
	require.Error(t, err)
}
