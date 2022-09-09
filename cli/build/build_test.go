package build

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
)

const testDirName = "build-test-dir"

func TestFillCtx(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "app1"), 0750))

	var cmdCtx cmdcontext.CmdCtx
	cmdCtx.Cli.WorkDir = workDir
	require.NoError(t, FillCtx(&cmdCtx, []string{"app1"}))
	assert.Equal(t, cmdCtx.Build.BuildDir, filepath.Join(workDir, "app1"))
	require.NoError(t, FillCtx(&cmdCtx, []string{"./app1"}))
	assert.Equal(t, cmdCtx.Build.BuildDir, filepath.Join(workDir, "app1"))

	require.NoError(t, FillCtx(&cmdCtx, []string{}))
	assert.Equal(t, cmdCtx.Build.BuildDir, workDir)

	require.EqualError(t, FillCtx(&cmdCtx, []string{"app1", "app2"}), "too many args")
	require.EqualError(t, FillCtx(&cmdCtx, []string{"app2"}),
		fmt.Sprintf("stat %s: no such file or directory", filepath.Join(workDir, "app2")))

	cmdCtx.Cli.WorkDir = "/tmp"
	require.NoError(t, FillCtx(&cmdCtx, []string{filepath.Join(workDir, "app1")}))
	assert.Equal(t, cmdCtx.Build.BuildDir, filepath.Join(workDir, "app1"))
}

func TestFillCtxAbsoluteAppPath(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "app1"), 0750))

	var cmdCtx cmdcontext.CmdCtx
	cmdCtx.Cli.WorkDir = "/opt"
	require.NoError(t, FillCtx(&cmdCtx, []string{filepath.Join(workDir, "app1")}))
	assert.Equal(t, cmdCtx.Build.BuildDir, filepath.Join(workDir, "app1"))
}

func TestFillCtxAppPathIsFile(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "app1"), []byte("text"), 0664))

	var cmdCtx cmdcontext.CmdCtx
	cmdCtx.Cli.WorkDir = workDir
	require.EqualError(t, FillCtx(&cmdCtx, []string{"app1"}),
		fmt.Sprintf("%s is not a directory", filepath.Join(workDir, "app1")))
}

func TestFillCtxMultipleArgs(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	var cmdCtx cmdcontext.CmdCtx
	cmdCtx.Cli.WorkDir = workDir
	require.EqualError(t, FillCtx(&cmdCtx, []string{"app1", "app2"}), "too many args")
}
