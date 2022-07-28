package steps

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

type RenderTemplate struct {
}

func (RenderTemplate) Run(ctx cmdcontext.CreateCtx, templateCtx *templates.TemplateCtx) error {
	templateFileNamePattern := regexp.MustCompile(`^(.*)\.tt\.template$`)
	err := filepath.Walk(templateCtx.AppPath,
		func(filePath string, fileInfo os.FileInfo, err error) error {
			if !fileInfo.Mode().IsDir() {
				if matches := templateFileNamePattern.FindStringSubmatch(fileInfo.Name()); matches != nil {
					resultFilePath := path.Join(path.Dir(filePath), matches[1])
					if err := templateCtx.Engine.RenderFile(filePath, resultFilePath, templateCtx.Vars); err != nil {
						return fmt.Errorf("Failed template rendering: %s", err)
					}
					if err = os.Remove(filePath); err != nil {
						return fmt.Errorf("Error removing %s: %s", filePath, err)
					}
				}
				newFileName, err := templateCtx.Engine.RenderText(filePath, templateCtx.Vars)
				if err != nil {
					return fmt.Errorf("Failed file name processing %s: %s", filePath, err)
				}
				if newFileName != filePath {
					if err = os.Rename(filePath, newFileName); err != nil {
						return fmt.Errorf("Error renaming %s to %s: %s", filePath, newFileName, err)
					}
				}
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("Template instantiation error: %s", err)
	}
	return nil
}
