package steps

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

const testWorkDirName = "work-dir"

func TestCreateTmpAppDirBasic(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	createCtx.AppName = "app1"
	createCtx.WorkDir = workDir
	createAppDir := CreateTemporaryAppDirectory{}
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))
	defer os.RemoveAll(templateCtx.AppPath)

	require.Equal(t, templateCtx.TargetAppPath, filepath.Join(workDir, createCtx.AppName))
	require.DirExists(t, templateCtx.AppPath)
}

func TestCreateTmpAppDirMissingAppName(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	createAppDir := CreateTemporaryAppDirectory{}
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	createCtx.WorkDir = workDir
	require.EqualError(t, createAppDir.Run(&createCtx, &templateCtx),
		"Application name cannot be empty")

	// Set template name.
	createCtx.AppName = "cartridge"
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))
	defer os.RemoveAll(templateCtx.AppPath)

	require.Equal(t, templateCtx.TargetAppPath, filepath.Join(workDir, createCtx.AppName))
	require.DirExists(t, templateCtx.AppPath)
}

func TestCreateTmpAppDirDestinationSet(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	createAppDir := CreateTemporaryAppDirectory{}
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	createCtx.AppName = "app1"
	createCtx.DestinationDir = workDir
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))
	defer os.RemoveAll(templateCtx.AppPath)

	require.Equal(t, templateCtx.TargetAppPath, filepath.Join(workDir, "app1"))
}
