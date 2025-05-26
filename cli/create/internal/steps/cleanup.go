package steps

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// Cleanup represents application directory cleanup step.
type Cleanup struct{}

// Run removes all files/directories, except files in the include list.
func (hook Cleanup) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	if !templateCtx.IsManifestPresent {
		log.Debug("No manifest. Skipping clean up step.")
		return nil
	}

	var err error
	filesToKeepCount := len(templateCtx.Manifest.Include)
	if filesToKeepCount == 0 {
		return nil
	}

	filesToKeep := make(map[string]bool, filesToKeepCount)
	for _, fileName := range templateCtx.Manifest.Include {
		// File name may contain template vars.
		if fileName, err = templateCtx.Engine.RenderText(fileName, templateCtx.Vars); err != nil {
			return fmt.Errorf("file name rendering error: %s", err)
		}
		fullPath := filepath.Join(templateCtx.AppPath, fileName)
		filesToKeep[fullPath] = true
	}

	// Directories are not removed in FS tree walk callback.
	dirsToRemove := make([]string, 0)
	err = filepath.Walk(templateCtx.AppPath,
		func(filePath string, fileInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			found := filesToKeep[filePath]
			if !found {
				if fileInfo.IsDir() {
					if filePath != templateCtx.AppPath {
						dirsToRemove = append(dirsToRemove, filePath)
					}
				} else if fileInfo.Mode().IsRegular() {
					log.Debugf("Removing %s", filePath)
					if err := os.Remove(filePath); err != nil {
						log.Errorf("failed to remove %s: %s", filePath, err)
					}
				}
			}

			return nil
		})
	if err != nil {
		return fmt.Errorf("cleanup failed: %s", err)
	}

	// Remove empty directories.
	for _, dir := range dirsToRemove {
		log.Debugf("Removing %s", dir)
		if err = os.Remove(dir); err != nil {
			log.Debugf("Directory %s is not empty.", dir)
		}
	}

	return nil
}
