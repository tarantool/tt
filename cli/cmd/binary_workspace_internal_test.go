package cmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runBinaryWorkspace runs tt binary workspace with args in dir.
func runBinaryWorkspace(t *testing.T, dir string, args ...string) error {
	t.Helper()

	restoreProcessState(t)
	withProjectDir(t, "")
	t.Setenv(distCoreEnv, "")

	root := NewCmdRoot()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"binary", "workspace", "-C", dir}, args...))

	return root.Execute()
}

// TestBinaryWorkspace checks that tt binary workspace writes go.work for the
// local module of the manifest in the -C directory on the core --core
// names, and removes it.
func TestBinaryWorkspace(t *testing.T) {
	_, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not in PATH")
	}

	core, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/tt-foo\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tt.manifest.toml"), []byte(
		"manifest_version = \"0.1\"\nmanifest_kind = \"tt-distribution\"\n\n"+
			"[core]\ntt = \"v3.0.0\"\n\n[dependencies]\nfoo = \".\"\n"), 0o644))

	err = runBinaryWorkspace(t, dir)
	require.ErrorContains(t, err, "set [core].path, or give --core DIR")

	err = runBinaryWorkspace(t, dir, "--remove", "--force")
	require.ErrorContains(t, err, "[force remove] were all set")

	require.NoError(t, runBinaryWorkspace(t, dir, "--core", core, "--dry-run"))
	assert.NoFileExists(t, filepath.Join(dir, "go.work"))

	require.NoError(t, runBinaryWorkspace(t, dir, "--core", core))

	data, err := os.ReadFile(filepath.Join(dir, "go.work"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "\tgithub.com/tarantool/tt/sdk/build/v3 => ")

	// Removing needs no core: the manifest names a version of it.
	require.NoError(t, runBinaryWorkspace(t, dir, "--remove"))
	assert.NoFileExists(t, filepath.Join(dir, "go.work"))
}
