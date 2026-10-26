package steps

import (
	"archive/tar"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
)

const subdirName = "subdir"

func createArchive(buf io.Writer, files ...string) error {
	gzipWriter := gzip.NewWriter(buf)
	defer func() {
		_ = gzipWriter.Close()
	}()

	tarWriter := tar.NewWriter(gzipWriter)

	defer func() {
		_ = tarWriter.Close()
	}()

	for _, fileName := range files {
		err := addToArchive(tarWriter, fileName)
		if err != nil {
			return fmt.Errorf("error adding %s to archive: %w", fileName, err)
		}
	}

	return nil
}

func addToArchive(tarWriter *tar.Writer, fileName string) error {
	file, err := os.Open(fileName)
	if err != nil {
		return err
	}

	defer func() {
		_ = file.Close()
	}()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	tarHeader, err := tar.FileInfoHeader(stat, stat.Name())
	if err != nil {
		return err
	}

	err = tarWriter.WriteHeader(tarHeader)
	if err != nil {
		return err
	}

	_, err = io.Copy(tarWriter, file)
	if err != nil {
		return err
	}

	return nil
}

func TestCopyTemplateDirectory(t *testing.T) {
	dstDir := t.TempDir()
	workDir2 := t.TempDir()

	require.NoError(t, copy.Copy("testdata/copy_template", workDir2))

	var createCtx create_ctx.CreateCtx

	createCtx.TemplateName = filepath.Join(workDir2, "templates", "basic")
	createCtx.WorkDir = dstDir

	templateCtx := app_template.NewTemplateContext()

	templateCtx.AppPath = filepath.Join(dstDir, "app1")

	// CopyAppTemplate must copy "src" template from workdir2 to workdir1 using "app1" as dst name.
	copyAppTemplate := CopyAppTemplate{}
	require.NoError(t, copyAppTemplate.Run(&createCtx, &templateCtx))
	require.DirExists(t, templateCtx.AppPath)
	require.FileExists(t, filepath.Join(templateCtx.AppPath, "init.lua"))
	require.FileExists(t, filepath.Join(templateCtx.AppPath, subdirName, "file.txt"))
}

func TestCopyTemplateDirectoryRelative(t *testing.T) {
	dstDir := t.TempDir()
	workDir2 := t.TempDir()

	require.NoError(t, copy.Copy("testdata/copy_template", workDir2))

	var createCtx create_ctx.CreateCtx

	// A relative path is taken from the working directory of tt.
	createCtx.TemplateName = "./templates/basic"
	createCtx.WorkDir = workDir2

	templateCtx := app_template.NewTemplateContext()

	templateCtx.AppPath = filepath.Join(dstDir, "app1")

	// CopyAppTemplate must copy "src" template from workdir2 to workdir1 using "app1" as dst name.
	copyAppTemplate := CopyAppTemplate{}
	require.NoError(t, copyAppTemplate.Run(&createCtx, &templateCtx))
	require.DirExists(t, templateCtx.AppPath)
	require.FileExists(t, filepath.Join(templateCtx.AppPath, "init.lua"))
	require.FileExists(t, filepath.Join(templateCtx.AppPath, subdirName, "file.txt"))
}

// TestCopyTemplateByName checks that a name without a separator is a built-in
// template even when a directory of that name is in the working directory: a
// template of the user's own is given by its path.
func TestCopyTemplateByName(t *testing.T) {
	workDir := t.TempDir()

	require.NoError(t, copy.Copy("testdata/copy_template/templates", workDir))

	createCtx := create_ctx.CreateCtx{TemplateName: "basic", WorkDir: workDir}
	templateCtx := app_template.NewTemplateContext()

	templateCtx.AppPath = filepath.Join(t.TempDir(), "app1")

	err := CopyAppTemplate{}.Run(&createCtx, &templateCtx)
	require.EqualError(t, err, "template 'basic' is not found: "+
		"a template of your own is given by its path, such as ./basic")
	assert.NoDirExists(t, templateCtx.AppPath)
}

func TestExtractTemplateArchive(t *testing.T) {
	for _, templateName := range []string{"tmpl.tgz", "tmpl"} {
		t.Run(templateName, func(t *testing.T) {
			var createCtx create_ctx.CreateCtx

			templateCtx := app_template.NewTemplateContext()

			dstDir := t.TempDir()
			workDir := t.TempDir()

			srcDir := filepath.Join(workDir, "src")
			require.NoError(t, os.Mkdir(srcDir, 0o755))

			require.NoError(t, os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("text"),
				0o644))

			templateCtx.AppPath = filepath.Join(dstDir, "app1")
			// An archive may be named without its extension.
			createCtx.TemplateName = filepath.Join(workDir, templateName)

			archivePath := filepath.Join(workDir, "tmpl.tgz")
			archiveOut, err := os.Create(archivePath)
			require.NoError(t, err)

			defer func() {
				_ = archiveOut.Close()
			}()

			require.NoError(t, createArchive(archiveOut, filepath.Join(srcDir, "file1.txt")))

			copyAppTemplate := CopyAppTemplate{}
			require.NoError(t, copyAppTemplate.Run(&createCtx, &templateCtx))
			assert.DirExists(t, templateCtx.AppPath)
			assert.FileExists(t, filepath.Join(templateCtx.AppPath, "file1.txt"))
		})
	}
}

// TestCopyTemplateMissingPath checks that a path that names neither a
// directory nor an archive is reported by its absolute path.
func TestCopyTemplateMissingPath(t *testing.T) {
	workDir := t.TempDir()

	// A regular file without an archive extension is no template either.
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "notes"), nil, 0o644))

	for _, templateName := range []string{"./missing", "./notes"} {
		createCtx := create_ctx.CreateCtx{TemplateName: templateName, WorkDir: workDir}
		templateCtx := app_template.NewTemplateContext()

		templateCtx.AppPath = filepath.Join(t.TempDir(), "app1")

		err := CopyAppTemplate{}.Run(&createCtx, &templateCtx)
		require.EqualError(t, err, fmt.Sprintf("template '%s' is not found",
			filepath.Join(workDir, templateName)))
	}
}

//go:embed testdata/copy_template/templates/basic
var templateTestFs embed.FS

func TestCopyEmbeddedFs(t *testing.T) {
	tmpDir := t.TempDir()
	templateFs, err := fs.Sub(templateTestFs, "testdata/copy_template/templates/basic")
	require.NoError(t, err)
	require.NoError(t, copyEmbedFs(templateFs, tmpDir))
	assert.FileExists(t, filepath.Join(tmpDir, "init.lua"))
	assert.FileExists(t, filepath.Join(tmpDir, "subdir", "file.txt"))
	assert.FileExists(t, filepath.Join(tmpDir, "echo.sh"))

	stat, err := os.Stat(filepath.Join(tmpDir, "echo.sh"))
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o644), stat.Mode().Perm())
}
