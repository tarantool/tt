package steps

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

const subdirName = "subdir"

func checkForExistence(t *testing.T, path string, perm os.FileMode) {
	stat, err := os.Stat(path)
	if err != nil {
		t.Errorf("Failed getting stat of %s: %s", path, err)
	}
	if stat.Mode().Perm() != perm {
		t.Errorf("%s permissions mismatch: actual %o - expected %o",
			path, stat.Mode().Perm(), perm)
	}
}

func createArchive(buf io.Writer, files ...string) error {
	gzipWriter := gzip.NewWriter(buf)
	defer gzipWriter.Close()
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for _, fileName := range files {
		err := addToArchive(tarWriter, fileName)
		if err != nil {
			return fmt.Errorf("Error adding %s to archive: %s", fileName, err)
		}
	}

	return nil
}

func addToArchive(tarWriter *tar.Writer, fileName string) error {
	file, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer file.Close()

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
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	createCtx.TemplateName = "src"

	dstDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Template dir is not created: %s", err)
	}
	defer os.RemoveAll(dstDir)

	workDir2, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Template dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir2)

	srcDir := filepath.Join(workDir2, createCtx.TemplateName)
	if err = os.Mkdir(srcDir, defaultPermissions); err != nil {
		t.Fatalf("Template dir is not created: %s", err)
	}

	subDirPath := filepath.Join(srcDir, subdirName)
	if err := os.Mkdir(subDirPath, defaultPermissions); err != nil {
		t.Fatalf("Failed to create subdir: %s", err)
	}

	if err = os.WriteFile(filepath.Join(subDirPath, "file1.txt"),
		[]byte("text"), 0644); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}
	if err = os.WriteFile(filepath.Join(srcDir, "file2.txt"), []byte("text"), 0640); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}

	createCtx.Paths = []string{dstDir, workDir2}
	templateCtx.AppPath = filepath.Join(dstDir, "app1")

	// CopyAppTemplate must copy "src" template from workdir2 to workdir1 using "app1" as dst name.
	copyAppTemplate := CopyAppTemplate{}
	if err = copyAppTemplate.Run(createCtx, &templateCtx); err != nil {
		t.Errorf("Error copying template: %s", err)
	}

	checkForExistence(t, templateCtx.AppPath, 0755)
	checkForExistence(t, filepath.Join(templateCtx.AppPath, subdirName), 0755)
	checkForExistence(t, filepath.Join(templateCtx.AppPath, subdirName, "file1.txt"), 0644)
	checkForExistence(t, filepath.Join(templateCtx.AppPath, "file2.txt"), 0640)
}

func TestExtractTemplateArchive(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()

	dstDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Template dir is not created: %s", err)
	}
	defer os.RemoveAll(dstDir)

	workDir, err := ioutil.TempDir("", testWorkDirName)
	t.Log(workDir)
	if err != nil {
		t.Fatalf("Template dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	srcDir := filepath.Join(workDir, "src")
	if err = os.Mkdir(srcDir, 0755); err != nil {
		t.Fatalf("Template dir is not created: %s", err)
	}

	if err = os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("text"), 0644); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}

	createCtx.Paths = []string{workDir}
	templateCtx.AppPath = filepath.Join(dstDir, "app1")
	createCtx.TemplateName = "tmpl"

	archivePath := filepath.Join(workDir, "tmpl.tgz")
	archiveOut, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Error creating archive: %s", err)
	}
	defer archiveOut.Close()

	err = createArchive(archiveOut, filepath.Join(srcDir, "file1.txt"))
	if err != nil {
		t.Fatalf("Error writing archive: %s", err)
	}

	copyAppTemplate := CopyAppTemplate{}
	if err = copyAppTemplate.Run(createCtx, &templateCtx); err != nil {
		t.Errorf("Error extracting template: %s", err)
	}

	checkForExistence(t, templateCtx.AppPath, 0755)
	checkForExistence(t, filepath.Join(templateCtx.AppPath, "file1.txt"), 0644)
}
