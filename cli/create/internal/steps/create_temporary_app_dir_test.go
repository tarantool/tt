package steps_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
	"github.com/tarantool/tt/v3/cli/create/internal/steps"
)

func TestCreateTmpAppDirBasic(t *testing.T) {
	var createCtx create_ctx.CreateCtx

	templateCtx := app_template.NewTemplateContext()

	workDir := t.TempDir()

	createCtx.AppName = "app1"
	createCtx.WorkDir = workDir

	createAppDir := steps.CreateTemporaryAppDirectory{}
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))

	defer func() {
		_ = os.RemoveAll(templateCtx.AppPath)
	}()

	require.Equal(t, templateCtx.TargetAppPath, filepath.Join(workDir, createCtx.AppName))
	require.DirExists(t, templateCtx.AppPath)
}

func TestCreateTmpAppDirMissingAppName(t *testing.T) {
	var createCtx create_ctx.CreateCtx

	templateCtx := app_template.NewTemplateContext()

	createAppDir := steps.CreateTemporaryAppDirectory{}
	workDir := t.TempDir()

	createCtx.WorkDir = workDir
	require.EqualError(t, createAppDir.Run(&createCtx, &templateCtx),
		"application name cannot be empty")

	// Set template name.
	createCtx.AppName = "sample"
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))

	defer func() {
		_ = os.RemoveAll(templateCtx.AppPath)
	}()

	require.Equal(t, templateCtx.TargetAppPath, filepath.Join(workDir, createCtx.AppName))
	require.DirExists(t, templateCtx.AppPath)
}

func TestCreateTmpAppDirDestinationSet(t *testing.T) {
	var createCtx create_ctx.CreateCtx

	templateCtx := app_template.NewTemplateContext()

	createAppDir := steps.CreateTemporaryAppDirectory{}
	workDir := t.TempDir()

	createCtx.AppName = "app1"
	createCtx.DestinationDir = workDir
	require.NoError(t, createAppDir.Run(&createCtx, &templateCtx))

	defer func() {
		_ = os.RemoveAll(templateCtx.AppPath)
	}()

	require.Equal(t, templateCtx.TargetAppPath, filepath.Join(workDir, "app1"))
}
