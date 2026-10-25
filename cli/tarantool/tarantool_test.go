package tarantool_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
)

// Permissions the fixtures write with.
const (
	dirMode  = 0o750
	fileMode = 0o600
	execMode = 0o700
)

// installation names an installation of a tarantool product.
type installation struct {
	program binary.Program
	id      string
}

// ce and ee name Community and Enterprise installations.
func ce(id string) installation { return installation{program: binary.ProgramCe, id: id} }
func ee(id string) installation { return installation{program: binary.ProgramEe, id: id} }

// writeExecutable writes an executable stub at path, creating its directories.
func writeExecutable(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirMode))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), execMode))
}

// install lays out a fake installation under root the way tt binary does and
// returns its executable.
func install(t *testing.T, root string, inst installation) string {
	t.Helper()

	path := filepath.Join(binary.InstallationPath(root, inst.program, inst.id),
		inst.program.Exec())
	writeExecutable(t, path)

	return path
}

// activate points the active tarantool link of root at target, as
// tt binary switch does.
func activate(t *testing.T, root, target string) {
	t.Helper()

	link := filepath.Join(root, binary.BinDirName, binary.ProgramCe.Exec())
	require.NoError(t, os.MkdirAll(filepath.Dir(link), dirMode))
	require.NoError(t, os.Symlink(target, link))
}

// writeLock writes a lock bundling tarantool bundled, none when empty, into
// dir.
func writeLock(t *testing.T, dir, bundled string) {
	t.Helper()

	data, err := manifest.Lock{
		LockVersion:      manifest.LockVersion,
		ManifestVersion:  manifest.ManifestVersion,
		GeneratedBy:      "tt 3.0.0",
		ManifestHash:     "sha256:0",
		BundledTarantool: bundled,
		BundledTt:        "",
		BundledTcm:       "",
		Products:         nil,
		DevDependencies:  nil,
	}.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, dirMode))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.lock"), data, fileMode))
}

// pathTarantool makes PATH a directory holding only a tarantool and returns
// it.
func pathTarantool(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "tarantool")
	writeExecutable(t, path)
	t.Setenv("PATH", dir)

	return path
}

func TestResolveUnknownScope(t *testing.T) {
	_, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeUnknown, Dir: t.TempDir()})
	require.ErrorIs(t, err, binary.ErrUnknownScope)
}
