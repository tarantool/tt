package steps

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/create/builtin_templates"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/util"
)

// CopyAppTemplate represents template -> app directory copy step.
type CopyAppTemplate struct {
}

// Returns a file perm for path from fileModes. Or default perm if data is missing in modes map.
func getFileMode(path string, dirEntry fs.DirEntry, fileModes map[string]int) fs.FileMode {
	const defaultDirPerm = 0755  // drwxr-xr-x
	const defaultFilePerm = 0660 // -rw-rw----
	if dirEntry.IsDir() {
		return defaultDirPerm
	}

	fileMode, found := fileModes[path]
	if !found {
		log.Warnf("No file mode info for '%s'", path)
		return fs.FileMode(defaultFilePerm)
	}

	return fs.FileMode(fileMode)
}

// copyEmbedFs copies src file system tree to dst.
func copyEmbedFs(srcFs fs.FS, dst string, fileModes map[string]int) error {
	err := fs.WalkDir(srcFs, ".", func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fileMode := getFileMode(path, dirEntry, fileModes)
		if dirEntry.IsDir() {
			destDirPath := filepath.Join(dst, path)
			if err := util.CreateDirectory(destDirPath, fileMode); err != nil {
				return err
			}
		} else {
			inFile, err := srcFs.Open(path)
			if err != nil {
				return err
			}
			defer inFile.Close()

			outFile, err := os.OpenFile(filepath.Join(dst, path), os.O_CREATE|os.O_WRONLY,
				fileMode)
			if err != nil {
				return err
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, inFile); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// Run copies/extracts application template to target application directory.
func (CopyAppTemplate) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx) error {
	templateName := createCtx.TemplateName

	// Search for template in template paths.
	for _, templatesLocation := range createCtx.TemplateSearchPaths {
		templatePath := path.Join(templatesLocation, templateName)

		if util.IsDir(templatePath) {
			log.Infof("Using template from %s", templatePath)
			if err := copy.Copy(templatePath, templateCtx.AppPath); err != nil {
				return fmt.Errorf("Template copying failed: %s", err)
			}
			return nil
		}

		archivesToCheck := [2]string{
			path.Join(templatesLocation, templateName+".tgz"),
			path.Join(templatesLocation, templateName+".tar.gz"),
		}
		for _, archivePath := range archivesToCheck {
			if util.IsRegularFile(archivePath) {
				log.Infof("Using template from %s", archivePath)
				return util.ExtractTarGz(archivePath, templateCtx.AppPath)
			}
		}
	}

	// Search for a template in built-in templates.
	templateDirs, err := builtin_templates.TemplatesFs.ReadDir("templates")
	if err != nil {
		return err
	}

	for _, templateDir := range templateDirs {
		if templateName == templateDir.Name() {
			log.Infof("Using built-in '%s' template.", templateName)
			templateFs, err := fs.Sub(builtin_templates.TemplatesFs,
				filepath.Join("templates", templateDir.Name()))
			if err != nil {
				return err
			}
			fileModes, found := builtin_templates.FileModes[templateName]
			if !found {
				log.Warn("File permissions data is not found for '%s' template. " +
					"Using default permissions.")
			}
			return copyEmbedFs(templateFs, templateCtx.AppPath, fileModes)
		}
	}

	return fmt.Errorf("Template '%s' is not found", templateName)
}
