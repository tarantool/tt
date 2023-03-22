package steps

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/util"
)

func TestEnableInstance(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "app")
	require.NoError(t, os.Mkdir(appPath, 0750))
	instEnabledPath := filepath.Join(tempDir, "instances.enabled")
	require.NoError(t, os.Mkdir(instEnabledPath, 0750))

	createCtx.AppName = "app"
	templateCtx.TargetAppPath = appPath
	var enableInstance = CreateAppSymlink{instEnabledPath}
	require.NoError(t, enableInstance.Run(&createCtx, &templateCtx))
	assert.FileExists(t, filepath.Join(instEnabledPath, "app"))
	targetPath, err := os.Readlink(filepath.Join(instEnabledPath, "app"))
	require.NoError(t, err)
	require.Equal(t, "../app", targetPath)
}

func TestEnableInstanceMissingTarget(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "app")
	instEnabledPath := filepath.Join(tempDir, "instances.enabled")
	require.NoError(t, os.Mkdir(instEnabledPath, 0750))

	createCtx.AppName = "app"
	templateCtx.TargetAppPath = appPath
	var enableInstance = CreateAppSymlink{instEnabledPath}
	require.NoError(t, enableInstance.Run(&createCtx, &templateCtx))
	assert.FileExists(t, filepath.Join(instEnabledPath, "app"))
	targetPath, err := os.Readlink(filepath.Join(instEnabledPath, "app"))
	require.NoError(t, err)
	require.Equal(t, "../app", targetPath)
}

func TestEnableInstanceMissingInstanceEnabledDir(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "app")
	require.NoError(t, util.CreateDirectory(appPath, fs.FileMode(0750)))
	instEnabledPath := filepath.Join(tempDir, "instances.enabled")

	createCtx.AppName = "app"
	templateCtx.TargetAppPath = appPath
	var enableInstance = CreateAppSymlink{instEnabledPath}
	require.NoError(t, enableInstance.Run(&createCtx, &templateCtx))
	// Check instances enabled directory is created.
	require.DirExists(t, instEnabledPath)
	require.FileExists(t, filepath.Join(instEnabledPath, createCtx.AppName))
	targetPath, err := os.Readlink(filepath.Join(instEnabledPath, createCtx.AppName))
	require.NoError(t, err)
	require.Equal(t, "../app", targetPath)
}

func TestEnableInstanceCurrentDirApp(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "app")
	require.NoError(t, os.Mkdir(appPath, 0750))
	instEnabledPath := filepath.Join(tempDir, ".")

	createCtx.AppName = "app"
	templateCtx.TargetAppPath = appPath
	var enableInstance = CreateAppSymlink{instEnabledPath}
	require.NoError(t, enableInstance.Run(&createCtx, &templateCtx))
	assert.NoFileExists(t, filepath.Join(instEnabledPath, "app"))
}
