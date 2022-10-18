package steps

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

func TestMoveAppDirBasic(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	srcAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(srcAppDir)

	require.NoError(t, copy.Copy("testdata/cartridge", srcAppDir))

	dstAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(dstAppDir)

	templateCtx.TargetAppPath = filepath.Join(dstAppDir, "app")
	templateCtx.AppPath = srcAppDir
	moveAppDir := MoveAppDirectory{}
	require.NoError(t, moveAppDir.Run(&createCtx, &templateCtx))
	require.FileExists(t, filepath.Join(templateCtx.TargetAppPath, "conf.lua"))
	require.FileExists(t, filepath.Join(templateCtx.TargetAppPath, "MANIFEST.yaml"))

	// Check src dir is removed.
	require.NoDirExists(t, srcAppDir)
}

func TestMoveAppDirDstDirExist(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	srcAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(srcAppDir)

	dstAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(dstAppDir)

	templateCtx.TargetAppPath = dstAppDir
	templateCtx.AppPath = srcAppDir
	moveAppDir := MoveAppDirectory{}
	require.EqualError(t, moveAppDir.Run(&createCtx, &templateCtx),
		fmt.Sprintf("'%s' already exists.", dstAppDir))
}

func TestMoveAppDirSourceDirMissing(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	dstAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(dstAppDir)

	templateCtx.TargetAppPath = filepath.Join(dstAppDir, "app")
	templateCtx.AppPath = "/non/existing/dir"
	moveAppDir := MoveAppDirectory{}
	require.EqualError(t, moveAppDir.Run(&createCtx, &templateCtx),
		fmt.Sprintf("lstat %s: no such file or directory", templateCtx.AppPath))
}

func TestMoveAppDirTargetDirRemovalFailure(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	srcAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(srcAppDir)

	require.NoError(t, copy.Copy("testdata/cartridge", srcAppDir))

	dstAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(dstAppDir)

	require.NoError(t, os.MkdirAll(filepath.Join(dstAppDir, "parent", "apps"), 0755))

	// Make parent dir read-only.
	require.NoError(t, os.Chmod(filepath.Join(dstAppDir, "parent"), 0444))
	defer os.Chmod(filepath.Join(dstAppDir, "parent"), 0755)

	templateCtx.TargetAppPath = filepath.Join(dstAppDir, "parent", "apps")
	templateCtx.AppPath = srcAppDir
	moveAppDir := MoveAppDirectory{}
	require.EqualError(t, moveAppDir.Run(&createCtx, &templateCtx),
		fmt.Sprintf("mkdir %[1]s: permission denied", templateCtx.TargetAppPath))

	// Check subdir is still there.
	os.Chmod(filepath.Join(dstAppDir, "parent"), 0755)
	require.DirExists(t, templateCtx.TargetAppPath)
}

func TestMoveAppDirEmptyTargetDir(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	srcAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(srcAppDir)

	require.NoError(t, copy.Copy("testdata/cartridge", srcAppDir))

	dstAppDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(dstAppDir)

	templateCtx.AppPath = srcAppDir
	moveAppDir := MoveAppDirectory{}
	require.NoError(t, moveAppDir.Run(&createCtx, &templateCtx))
	require.DirExists(t, srcAppDir)
	require.FileExists(t, filepath.Join(templateCtx.AppPath, "conf.lua"))
	require.NoFileExists(t, filepath.Join(templateCtx.TargetAppPath, "MANIFEST.yaml"))
}
