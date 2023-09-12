package steps

import (
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

func TestCleanUp(t *testing.T) {
	workDir := t.TempDir()

	require.Nil(t, copy.Copy("testdata/cleanup", workDir))

	filesToRemove := []string{filepath.Join(workDir, "file1.txt"),
		filepath.Join(workDir, "subdir", "file2.txt"),
	}

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.IsManifestPresent = true
	templateCtx.Manifest.Include = []string{"keep_it.txt", "{{.user_name}}.txt"}
	templateCtx.Vars = map[string]string{"user_name": "admin"}

	cleanUp := Cleanup{}
	require.Nil(t, cleanUp.Run(&createCtx, &templateCtx))

	assert.FileExists(t, filepath.Join(workDir, "keep_it.txt"))
	assert.FileExists(t, filepath.Join(workDir, "admin.txt"))
	assert.DirExists(t, workDir)
	for _, file := range filesToRemove {
		assert.NoFileExists(t, file)
	}

	// Check if sub-directory is removed.
	assert.NoDirExists(t, filepath.Join(workDir, "subdir"))
}

func TestCleanUpKeepSubdir(t *testing.T) {
	workDir := t.TempDir()

	require.Nil(t, copy.Copy("testdata/cleanup", workDir))

	filesToKeep := []string{filepath.Join(workDir, "keep_it.txt"),
		filepath.Join(workDir, "admin.txt"),
		filepath.Join(workDir, "subdir", "file2.txt"),
	}
	filesToRemove := []string{filepath.Join(workDir, "file1.txt")}

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.IsManifestPresent = true
	templateCtx.Manifest.Include = []string{
		"keep_it.txt",
		"{{.user_name}}.txt",
		"subdir/{{.name}}.txt",
	}
	templateCtx.Vars = map[string]string{
		"user_name": "admin",
		"name":      "file2",
	}

	cleanUp := Cleanup{}
	require.NoError(t, cleanUp.Run(&createCtx, &templateCtx))

	for _, file := range filesToKeep {
		assert.FileExists(t, file)
	}
	for _, file := range filesToRemove {
		assert.NoFileExists(t, file)
	}

	// Check that sub-directory is not removed.
	assert.DirExists(t, filepath.Join(workDir, "subdir"))
}
