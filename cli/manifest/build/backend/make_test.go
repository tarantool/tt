package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

func TestMakeArgs(t *testing.T) {
	t.Parallel()

	b := manifest.Build{Backend: BackendMake, MakeTarget: "all", Flags: []string{"-j4", "V=1"}}

	got := makeArgs(b, "/proj")
	want := []string{"-C", "/proj", "-f", "Makefile", "all", "-j4", "V=1"}
	assert.Equal(t, want, got)
}

func TestMakeArgsCustomEntrypoint(t *testing.T) {
	t.Parallel()

	b := manifest.Build{Backend: BackendMake, MakeTarget: "build", Entrypoint: "Makefile.tt"}

	got := makeArgs(b, "/proj")
	assert.Equal(t, []string{"-C", "/proj", "-f", "Makefile.tt", "build"}, got)
}

func TestMakeRunsTargetIntoOutputDir(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not available")
	}

	cwd := t.TempDir()
	out := t.TempDir()

	makefile := "build:\n\tprintf x > $(TT_OUTPUT_DIR)/mod.so\n"
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "Makefile"), []byte(makefile), 0o600))

	b := manifest.Build{Backend: BackendMake, MakeTarget: "build"}

	err := makeBackend{}.Run(context.Background(), b, cwd, Env{OutputDir: out})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(out, "mod.so"))
}

func TestMakeCopiesDeclaredOutputs(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not available")
	}

	cwd := t.TempDir()
	out := t.TempDir()

	// The target writes into cwd; the backend copies the declared output out.
	makefile := "build:\n\tprintf x > libmod.so\n"
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "Makefile"), []byte(makefile), 0o600))

	b := manifest.Build{Backend: BackendMake, MakeTarget: "build", Output: []string{"libmod.so"}}

	err := makeBackend{}.Run(context.Background(), b, cwd, Env{OutputDir: out})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(out, "libmod.so"))
}
