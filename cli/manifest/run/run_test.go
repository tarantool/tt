package run_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/run"
)

// Permissions the fixtures write with. The selection reads the executable bit,
// so a plain file must not carry one and a stub must.
const (
	dirMode    = 0o750
	fileMode   = 0o600
	scriptMode = 0o700
)

// writeExecutable writes an executable stub at path, creating its directories.
func writeExecutable(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirMode))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), scriptMode))
}

// projectDir returns a fresh directory holding a manifest.
func projectDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, run.ManifestFileName)
	require.NoError(t, os.WriteFile(manifestPath, []byte("manifest_version = '0.1'\n"), fileMode))

	return dir
}

func TestProjectRootAcceptsADirectoryHoldingAManifest(t *testing.T) {
	dir := projectDir(t)

	root, err := run.ProjectRoot(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, root)
}

func TestProjectRootRefusesADirectoryWithoutOne(t *testing.T) {
	_, err := run.ProjectRoot(t.TempDir())

	require.ErrorIs(t, err, run.ErrNoManifest)
	assert.Contains(t, err.Error(), run.ManifestFileName)
}

// A directory named app.manifest.toml is not a manifest; treating it as one
// would send the command on to a Tarantool that cannot read it.
func TestProjectRootRefusesAManifestThatIsADirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, run.ManifestFileName), dirMode))

	_, err := run.ProjectRoot(dir)
	require.ErrorIs(t, err, run.ErrNoManifest)
}

func TestSelectTarantoolPrefersTheBundledRuntime(t *testing.T) {
	root := projectDir(t)
	bundled := filepath.Join(root, "_runtime", "tarantool", "bin", "tarantool")
	writeExecutable(t, bundled)

	system := filepath.Join(t.TempDir(), "tarantool")
	writeExecutable(t, system)

	chosen, err := run.SelectTarantool(root, run.Environment{Executable: system})
	require.NoError(t, err)
	assert.Equal(t, bundled, chosen)
}

// The Enterprise SDK tree is flat, so a cache entry copied from one puts the
// interpreter a directory above the bin/ layout every other source produces.
func TestSelectTarantoolAcceptsAFlatBundledLayout(t *testing.T) {
	root := projectDir(t)
	bundled := filepath.Join(root, "_runtime", "tarantool", "tarantool")
	writeExecutable(t, bundled)

	chosen, err := run.SelectTarantool(root, run.Environment{})
	require.NoError(t, err)
	assert.Equal(t, bundled, chosen)
}

func TestSelectTarantoolUseSystemIgnoresTheBundledRuntime(t *testing.T) {
	root := projectDir(t)
	writeExecutable(t, filepath.Join(root, "_runtime", "tarantool", "bin", "tarantool"))

	system := filepath.Join(t.TempDir(), "tarantool")
	writeExecutable(t, system)

	chosen, err := run.SelectTarantool(root, run.Environment{
		Executable: system,
		UseSystem:  true,
	})
	require.NoError(t, err)
	assert.Equal(t, system, chosen)
}

func TestSelectTarantoolFallsBackToTheEnvironmentExecutable(t *testing.T) {
	root := projectDir(t)

	system := filepath.Join(t.TempDir(), "tarantool")
	writeExecutable(t, system)

	chosen, err := run.SelectTarantool(root, run.Environment{Executable: system})
	require.NoError(t, err)
	assert.Equal(t, system, chosen)
}

// A non-executable file under _runtime/ is not an interpreter; the selection
// must fall through to the host rather than exec something it cannot run.
func TestSelectTarantoolSkipsANonExecutableBundle(t *testing.T) {
	root := projectDir(t)
	bundled := filepath.Join(root, "_runtime", "tarantool", "bin", "tarantool")
	require.NoError(t, os.MkdirAll(filepath.Dir(bundled), dirMode))
	require.NoError(t, os.WriteFile(bundled, []byte("not executable"), fileMode))

	system := filepath.Join(t.TempDir(), "tarantool")
	writeExecutable(t, system)

	chosen, err := run.SelectTarantool(root, run.Environment{Executable: system})
	require.NoError(t, err)
	assert.Equal(t, system, chosen)
}

func TestSelectTarantoolNamesBothPlacesWhenNothingIsFound(t *testing.T) {
	root := projectDir(t)

	// An empty PATH is what makes the lookup fail deterministically; without it
	// the developer's own tarantool answers and the case never runs.
	t.Setenv("PATH", "")

	_, err := run.SelectTarantool(root, run.Environment{})
	require.ErrorIs(t, err, run.ErrNoTarantool)
	assert.Contains(t, err.Error(), "_runtime")
	assert.Contains(t, err.Error(), "PATH")
}

func TestSelectTarantoolFindsTheInterpreterOnPath(t *testing.T) {
	root := projectDir(t)

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "tarantool"))
	t.Setenv("PATH", binDir)

	chosen, err := run.SelectTarantool(root, run.Environment{})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(binDir, "tarantool"), chosen)
}

func TestLuatestScriptPrefersThePerRockCopy(t *testing.T) {
	root := t.TempDir()

	perRock := filepath.Join(root, ".rocks", "share", "tarantool", "rocks",
		"luatest", "1.0.1-1", "bin", "luatest")
	writeExecutable(t, perRock)

	// The deployed entry may be a /bin/sh launcher carrying an interpreter path
	// baked in at install time, which is exactly what must not be run.
	writeExecutable(t, filepath.Join(root, ".rocks", "bin", "luatest"))

	script, err := run.LuatestScript(root)
	require.NoError(t, err)
	assert.Equal(t, perRock, script)
}

func TestLuatestScriptTakesTheHighestInstalledVersion(t *testing.T) {
	root := t.TempDir()
	rocks := filepath.Join(root, ".rocks", "share", "tarantool", "rocks", "luatest")

	writeExecutable(t, filepath.Join(rocks, "0.5.7-1", "bin", "luatest"))
	writeExecutable(t, filepath.Join(rocks, "1.0.1-1", "bin", "luatest"))

	script, err := run.LuatestScript(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(rocks, "1.0.1-1", "bin", "luatest"), script)
}

func TestLuatestScriptFallsBackToTheDeployedEntry(t *testing.T) {
	root := t.TempDir()
	deployed := filepath.Join(root, ".rocks", "bin", "luatest")
	writeExecutable(t, deployed)

	script, err := run.LuatestScript(root)
	require.NoError(t, err)
	assert.Equal(t, deployed, script)
}

func TestLuatestScriptReportsAnEmptyTree(t *testing.T) {
	root := t.TempDir()

	_, err := run.LuatestScript(root)
	require.ErrorIs(t, err, run.ErrNoLuatest)
	assert.False(t, run.LuatestInstalled(root))
}

func TestLuatestInstalledSeesAnInstalledRock(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, ".rocks", "bin", "luatest"))

	assert.True(t, run.LuatestInstalled(root))
}

func TestUseSystemFromEnvReadsTheDocumentedValues(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     false,
		"false": false,
		"1":     true,
		"true":  true,
		"yes":   true,
	}

	for value, want := range cases {
		t.Setenv(run.UseSystemEnv, value)
		assert.Equal(t, want, run.UseSystemFromEnv(), "value %q", value)
	}
}
