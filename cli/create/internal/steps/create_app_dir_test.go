package steps

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

const testWorkDirName = "work-dir"

func TestCreateAppDirBasic(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	createCtx.AppName = "app1"
	createCtx.WorkDir = workDir
	createAppDir := CreateAppDirectory{}
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))

	require.Equal(t, templateCtx.AppPath, filepath.Join(workDir, createCtx.AppName))
	require.DirExists(t, templateCtx.AppPath)

	// Check existing app handling.
	assert.EqualError(t, createAppDir.Run(&createCtx, &templateCtx),
		fmt.Sprintf("Application app1 already exists: %s", templateCtx.AppPath))

	// Create a file in app directory.
	tmpFileName := filepath.Join(workDir, "app1", "file")
	require.NoError(t, os.WriteFile(tmpFileName, []byte(""), 0664))

	createCtx.ForceMode = true
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))
	require.NoFileExists(t, tmpFileName)
}

func TestCreateAppDirMissingAppName(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	createAppDir := CreateAppDirectory{}
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	createCtx.WorkDir = workDir
	require.EqualError(t, createAppDir.Run(&createCtx, &templateCtx),
		"Application name cannot be empty")

	// Set template name.
	createCtx.AppName = "cartridge"
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))

	require.Equal(t, templateCtx.AppPath, filepath.Join(workDir, createCtx.AppName))
	require.DirExists(t, templateCtx.AppPath)
}

func TestCreateAppDirMissingWorkingDir(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	createAppDir := CreateAppDirectory{}
	createCtx.AppName = "app1"
	createCtx.WorkDir = testWorkDirName // Work dir does not exist.
	defer os.RemoveAll(testWorkDirName)
	require.EqualError(t, createAppDir.Run(&createCtx, &templateCtx),
		fmt.Sprintf("Error create application dir %[1]s: mkdir %[1]s: no such file or directory",
			filepath.Join(testWorkDirName, "app1")))
}

func TestCreateAppDirDestinationSet(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	createAppDir := CreateAppDirectory{}
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	createCtx.AppName = "app1"
	createCtx.DestinationDir = workDir
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))

	require.Equal(t, templateCtx.AppPath, filepath.Join(workDir, "app1"))
	require.DirExists(t, filepath.Join(workDir, "app1"))

	// Check existing app handling.
	require.EqualError(t, createAppDir.Run(&createCtx, &templateCtx),
		fmt.Sprintf("Application app1 already exists: %s", filepath.Join(workDir, "app1")))
}

func TestCreateAppDirTargetDirRemovalFailure(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	createAppDir := CreateAppDirectory{}
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "parent", "apps"), 0755))

	// Make parent dir read-only.
	require.NoError(t, os.Chmod(filepath.Join(workDir, "parent"), 0444))
	defer os.Chmod(filepath.Join(workDir, "parent"), 0755)

	dstPath := filepath.Join(workDir, "parent", "apps")
	createCtx.AppName = "app1"
	createCtx.DestinationDir = dstPath
	createCtx.ForceMode = true
	require.EqualError(t, createAppDir.Run(&createCtx, &templateCtx),
		fmt.Sprintf("Error create application dir %[1]s: mkdir %[1]s: permission denied",
			filepath.Join(dstPath, "app1")))

	// Check subdir is still there.
	os.Chmod(filepath.Join(workDir, "parent"), 0755)
	require.DirExists(t, dstPath)
}
