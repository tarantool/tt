package build

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
)

func TestRunHooks(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	require.NoError(t, copy.Copy("testdata/runhooks", workDir))
	buildCtx := BuildCtx{BuildDir: workDir}

	require.NoError(t, runBuildHook(&buildCtx, getPreBuildScripts()))
	assert.FileExists(t, filepath.Join(workDir, "tt-pre-build-invoked"))
	assert.NoFileExists(t, filepath.Join(workDir, "cartridge-pre-build-invoked"))

	require.NoError(t, runBuildHook(&buildCtx, getPostBuildScripts()))
	assert.FileExists(t, filepath.Join(workDir, "tt-post-build-invoked"))
	assert.NoFileExists(t, filepath.Join(workDir, "cartridge-post-build-invoked"))

	assert.NoError(t, os.Remove(filepath.Join(workDir, "tt-pre-build-invoked")))
	assert.NoError(t, os.Remove(filepath.Join(workDir, "tt-post-build-invoked")))
	assert.NoError(t, os.Remove(filepath.Join(workDir, "tt.pre-build")))
	assert.NoError(t, os.Remove(filepath.Join(workDir, "tt.post-build")))

	require.NoError(t, runBuildHook(&buildCtx, getPreBuildScripts()))
	assert.FileExists(t, filepath.Join(workDir, "cartridge-pre-build-invoked"))
	assert.NoFileExists(t, filepath.Join(workDir, "tt-pre-build-invoked"))

	require.NoError(t, runBuildHook(&buildCtx, getPostBuildScripts()))
	assert.FileExists(t, filepath.Join(workDir, "cartridge-post-build-invoked"))
	assert.NoFileExists(t, filepath.Join(workDir, "tt-post-build-invoked"))
}

func TestLocalBuild(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	require.NoError(t, copy.Copy("testdata/app1", workDir))
	var cmdCtx cmdcontext.CmdCtx
	configure.Cli(&cmdCtx)
	buildCtx := BuildCtx{BuildDir: workDir}

	require.NoError(t, buildLocal(&cmdCtx, &buildCtx))
	require.NoDirExists(t, filepath.Join(workDir, ".rocks", "share", "tarantool", "metrics"))
	require.DirExists(t, filepath.Join(workDir, ".rocks", "share", "tarantool", "rocks"))
	require.FileExists(t, filepath.Join(workDir, ".rocks", "share", "tarantool", "checks.lua"))
}

func TestLocalBuildSpecFileSet(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	require.NoError(t, copy.Copy("testdata/app1", workDir))
	var cmdCtx cmdcontext.CmdCtx
	configure.Cli(&cmdCtx)
	buildCtx := BuildCtx{BuildDir: workDir, SpecFile: "app1-scm-1.rockspec"}

	require.NoError(t, buildLocal(&cmdCtx, &buildCtx))
	require.DirExists(t, filepath.Join(workDir, ".rocks", "share", "tarantool", "rocks"))
	require.DirExists(t, filepath.Join(workDir, ".rocks", "share", "tarantool", "metrics"))
	require.FileExists(t, filepath.Join(workDir, ".rocks", "share", "tarantool", "checks.lua"))
}
