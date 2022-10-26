package steps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
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
	instEnabledPath := filepath.Join(tempDir, "instances.enabled")

	createCtx.AppName = "app"
	templateCtx.TargetAppPath = appPath
	var enableInstance = CreateAppSymlink{instEnabledPath}
	// Failed symlink creation is not an error, because it only affects app enabling in
	// current environment.
	require.NoError(t, enableInstance.Run(&createCtx, &templateCtx))
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
