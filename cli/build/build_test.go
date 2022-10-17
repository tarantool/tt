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

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	defer os.Chdir(wd)
	var buildCtx cmdcontext.BuildCtx
	require.NoError(t, FillCtx(&buildCtx, []string{"app1"}))
	assert.Equal(t, buildCtx.BuildDir, filepath.Join(workDir, "app1"))
	require.NoError(t, FillCtx(&buildCtx, []string{"./app1"}))
	assert.Equal(t, buildCtx.BuildDir, filepath.Join(workDir, "app1"))

	require.NoError(t, FillCtx(&buildCtx, []string{}))
	assert.Equal(t, buildCtx.BuildDir, workDir)

	require.EqualError(t, FillCtx(&buildCtx, []string{"app1", "app2"}), "too many args")
	require.EqualError(t, FillCtx(&buildCtx, []string{"app2"}),
		fmt.Sprintf("stat %s: no such file or directory", filepath.Join(workDir, "app2")))

	require.NoError(t, FillCtx(&buildCtx, []string{filepath.Join(workDir, "app1")}))
	assert.Equal(t, buildCtx.BuildDir, filepath.Join(workDir, "app1"))
}

func TestFillCtxAbsoluteAppPath(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "app1"), 0750))

	var buildCtx cmdcontext.BuildCtx
	require.NoError(t, FillCtx(&buildCtx, []string{filepath.Join(workDir, "app1")}))
	assert.Equal(t, buildCtx.BuildDir, filepath.Join(workDir, "app1"))
}

func TestFillCtxAppPathIsFile(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "app1"), []byte("text"), 0664))

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	defer os.Chdir(wd)
	var buildCtx cmdcontext.BuildCtx
	require.EqualError(t, FillCtx(&buildCtx, []string{"app1"}),
		fmt.Sprintf("%s is not a directory", filepath.Join(workDir, "app1")))
}

func TestFillCtxMultipleArgs(t *testing.T) {
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	var buildCtx cmdcontext.BuildCtx
	require.EqualError(t, FillCtx(&buildCtx, []string{"app1", "app2"}), "too many args")
}
