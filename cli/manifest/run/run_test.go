package run_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/run"
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

// selectInProject lays an installation of tarantool into the project scope
// and selects it with .tarantool/bin/tarantool, as tt binary install and a
// with-deps tt package install do, returning its executable.
func selectInProject(t *testing.T, root string) string {
	t.Helper()

	installed := filepath.Join(root, ".tarantool", "versions", "tarantool",
		"tarantool_3.8.1", "tarantool")
	writeExecutable(t, installed)

	link := filepath.Join(root, ".tarantool", "bin", "tarantool")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), dirMode))
	require.NoError(t, os.Symlink(installed, link))

	return installed
}

// onPath puts a tarantool on a PATH of its own and returns it.
func onPath(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	system := filepath.Join(binDir, "tarantool")
	writeExecutable(t, system)
	t.Setenv("PATH", binDir)

	return system
}

// Not parallel, here and below: PATH and TT_USE_SYSTEM_TARANTOOL are
// process-wide.
func TestTarantoolPrefersTheProjectSelection(t *testing.T) {
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	root := projectDir(t)
	installed := selectInProject(t, root)
	onPath(t)

	exe, err := run.Tarantool(root)
	require.NoError(t, err)
	assert.Equal(t, installed, exe.Path)
	assert.Equal(t, "3.8.1", exe.Version)
}

// A _runtime/ tree in the project is no longer where the interpreter comes
// from: a with-deps install lays the bundled runtime into .tarantool/versions/
// and selects it from there.
func TestTarantoolIgnoresARuntimeTree(t *testing.T) {
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	root := projectDir(t)
	system := onPath(t)

	writeExecutable(t, filepath.Join(root, "_runtime", "tarantool", "bin", "tarantool"))

	exe, err := run.Tarantool(root)
	require.NoError(t, err)
	assert.Equal(t, system, exe.Path)
}

func TestTarantoolUseSystemIgnoresTheProjectSelection(t *testing.T) {
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "1")

	root := projectDir(t)
	system := onPath(t)

	selectInProject(t, root)

	exe, err := run.Tarantool(root)
	require.NoError(t, err)
	assert.Equal(t, system, exe.Path)
}

// The project's selection is looked up in the root itself: tt run does not
// search upwards, and neither does the choice of its interpreter.
func TestTarantoolLooksInTheRootOnly(t *testing.T) {
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	parent := projectDir(t)
	selectInProject(t, parent)

	root := filepath.Join(parent, "sub")
	require.NoError(t, os.Mkdir(root, dirMode))
	require.NoError(t, os.WriteFile(filepath.Join(root, run.ManifestFileName),
		[]byte("manifest_version = '0.1'\n"), fileMode))

	system := onPath(t)

	exe, err := run.Tarantool(root)
	require.NoError(t, err)
	assert.Equal(t, system, exe.Path)
}

func TestTarantoolReportsNoneFound(t *testing.T) {
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	root := projectDir(t)

	// An empty PATH is what makes the lookup fail deterministically; without it
	// the developer's own tarantool answers and the case never runs.
	t.Setenv("PATH", "")

	_, err := run.Tarantool(root)
	require.ErrorIs(t, err, run.ErrNoTarantool)
	assert.Contains(t, err.Error(), filepath.Join(".tarantool", "bin", "tarantool"))
	assert.Contains(t, err.Error(), "PATH")
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

// The trailing separator is load-bearing: luatest reads a bare "test" as a
// group name and refuses it, so the directory has to arrive as "test/".
func TestTestDirPrefersTestOverTests(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "test"), dirMode))
	require.NoError(t, os.Mkdir(filepath.Join(root, "tests"), dirMode))

	dir, err := run.TestDir(root, "")
	require.NoError(t, err)
	assert.Equal(t, "test"+string(filepath.Separator), dir)
}

func TestTestDirFallsBackToTests(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "tests"), dirMode))

	dir, err := run.TestDir(root, "")
	require.NoError(t, err)
	assert.Equal(t, "tests"+string(filepath.Separator), dir)
}

// A file named test/ is not a test directory, and running it would fail deep
// inside the runner rather than here.
func TestTestDirIgnoresAFileNamedTest(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "test"), []byte("x"), fileMode))

	_, err := run.TestDir(root, "")
	require.ErrorIs(t, err, run.ErrNoTestDir)
}

func TestTestDirReportsAProjectWithNoTests(t *testing.T) {
	_, err := run.TestDir(t.TempDir(), "")

	require.ErrorIs(t, err, run.ErrNoTestDir)
	assert.Contains(t, err.Error(), "test")
}

func TestTestDirNarrowsToASubPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test", "integration"), dirMode))

	dir, err := run.TestDir(root, "test/integration")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("test", "integration")+string(filepath.Separator), dir)
}

// luatest takes a single file as readily as a directory, and narrowing to one
// test file is the common case while writing it.
func TestTestDirAcceptsASingleFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "test"), dirMode))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test", "unit_test.lua"), []byte("return {}\n"), fileMode))

	dir, err := run.TestDir(root, "test/unit_test.lua")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("test", "unit_test.lua"), dir)
}

// A sub-path that does not exist is an error rather than a run of nothing,
// which the runner would report as a pass.
func TestTestDirRefusesAMissingSubPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "test"), dirMode))

	_, err := run.TestDir(root, "test/nowhere")
	require.ErrorIs(t, err, run.ErrNoTestDir)
}

// A path climbing out of the project would run tests the project's own .rocks/
// tree does not describe.
func TestTestDirRefusesAnEscapingSubPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), dirMode))
	require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(root), "elsewhere"), dirMode))

	_, err := run.TestDir(root, "../elsewhere")
	require.ErrorIs(t, err, run.ErrNoTestDir)
}

// An absolute path inside the project is relativized rather than refused: it is
// what shell completion produces.
func TestTestDirAcceptsAnAbsolutePathInsideTheProject(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test", "unit"), dirMode))

	dir, err := run.TestDir(root, filepath.Join(root, "test", "unit"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("test", "unit")+string(filepath.Separator), dir)
}

// writeInstalledApp lays out an installed version of an application at dir:
// the manifest and a lock that bundles tarantool at version bundled.
func writeInstalledApp(t *testing.T, dir, bundled string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, dirMode))
	require.NoError(t, os.WriteFile(filepath.Join(dir, run.ManifestFileName),
		[]byte("manifest_version = \"0.1\"\n\n[package]\nname = \"app\"\n\n"+
			"[platform]\ntarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\n"), fileMode))

	lock, err := manifest.Lock{
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.lock"), lock, fileMode))
}

// installedVersion lays out an installed version of an application in the
// scope root, with the apps/ link to it, and returns both.
func installedVersion(t *testing.T, root, bundled string) (string, string) {
	t.Helper()

	version := filepath.Join(root, "versions", "app", "app_1.0.0")
	writeInstalledApp(t, version, bundled)

	link := filepath.Join(root, "apps", "app")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), dirMode))
	require.NoError(t, os.Symlink(filepath.Join("..", "versions", "app", "app_1.0.0"), link))

	return version, link
}

// An installed version is no project: it runs on the tarantool its lock names
// in its own scope rather than on PATH, reached either as the version or
// through its apps/ link.
func TestTarantoolOfAnInstalledVersion(t *testing.T) {
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	for _, envName := range []string{"TT_USER_ROOT", "TT_SYSTEM_ROOT"} {
		t.Run(envName, func(t *testing.T) {
			t.Setenv("TT_USER_ROOT", t.TempDir())
			t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

			root := t.TempDir()
			t.Setenv(envName, root)
			onPath(t)

			locked := filepath.Join(root, "versions", "tarantool", "tarantool_3.8.1", "tarantool")
			writeExecutable(t, locked)

			version, link := installedVersion(t, root, "3.8.1")

			for _, dir := range []string{version, link} {
				exe, err := run.Tarantool(dir)
				require.NoError(t, err, dir)
				assert.Equal(t, locked, exe.Path, dir)
			}
		})
	}
}

// The tarantool of an installed version is the locked one or none: a missing
// installation is reported with its install hint, not replaced by PATH.
func TestTarantoolOfAnInstalledVersionDoesNotFallBackToPath(t *testing.T) {
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	root := t.TempDir()
	t.Setenv("TT_USER_ROOT", root)
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())
	onPath(t)

	version, _ := installedVersion(t, root, "3.8.1")

	_, err := run.Tarantool(version)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tt binary install tarantool 3.8.1 --scope user")
}
