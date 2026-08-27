package build

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFillCtx(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "app1"), 0o750))

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	defer os.Chdir(wd)
	var buildCtx BuildCtx

	workDir, _ = os.Getwd()

	appDir := filepath.Join(workDir, "app1")

	require.NoError(t, FillCtx(&buildCtx, []string{"app1"}))
	assert.Equal(t, buildCtx.BuildDir, appDir)

	require.NoError(t, FillCtx(&buildCtx, []string{"./app1"}))
	assert.Equal(t, buildCtx.BuildDir, appDir)

	require.NoError(t, FillCtx(&buildCtx, []string{}))
	assert.Equal(t, buildCtx.BuildDir, workDir)

	require.EqualError(t, FillCtx(&buildCtx, []string{"app1", "app2"}), "too many args")

	require.NoError(t, FillCtx(&buildCtx, []string{filepath.Join(workDir, "app1")}))
	assert.Equal(t, buildCtx.BuildDir, filepath.Join(workDir, "app1"))
}

func TestFillCtxAbsoluteAppPath(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "app1"), 0o750))

	var buildCtx BuildCtx
	require.NoError(t, FillCtx(&buildCtx, []string{filepath.Join(workDir, "app1")}))
	assert.Equal(t, buildCtx.BuildDir, filepath.Join(workDir, "app1"))
}

func TestFillCtxAppPathIsFile(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "app1"), []byte("text"), 0o664))

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	defer os.Chdir(wd)
	var buildCtx BuildCtx
	workDir, _ = os.Getwd()

	appDir := filepath.Join(workDir, "app1")

	require.EqualError(t, FillCtx(&buildCtx, []string{"app1"}),
		fmt.Sprintf("%s is not a directory", appDir))
}

func TestFillCtxMultipleArgs(t *testing.T) {
	var buildCtx BuildCtx
	require.EqualError(t, FillCtx(&buildCtx, []string{"app1", "app2"}), "too many args")
}
