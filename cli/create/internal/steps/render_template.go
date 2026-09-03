package steps

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"

	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// RenderTemplate represents template render step.
type RenderTemplate struct{}

func render(templateCtx *app_template.TemplateCtx, templateFileNamePattern *regexp.Regexp,
	filePath string, fileInfo os.FileInfo,
) error {
	if !fileInfo.Mode().IsDir() {
		if matches := templateFileNamePattern.FindStringSubmatch(
			fileInfo.Name()); matches != nil {
			// File name matches template pattern. Render the file.
			resultFilePath := path.Join(path.Dir(filePath), matches[1])

			err := templateCtx.Engine.RenderFile(filePath,
				resultFilePath, templateCtx.Vars)
			if err != nil {
				return err
			}

			// Remove original template file.
			err = os.Remove(filePath)
			if err != nil {
				return fmt.Errorf("error removing %s: %w", filePath, err)
			}

			filePath = resultFilePath
		}

		// Render file name.
		newFileName, err := templateCtx.Engine.RenderText(filePath, templateCtx.Vars)
		if err != nil {
			return fmt.Errorf("failed file name processing %s: %w", filePath, err)
		}

		if newFileName != filePath {
			err = os.Rename(filePath, newFileName)
			if err != nil {
				return fmt.Errorf("error renaming %s to %s: %w", filePath, newFileName, err)
			}
		}
	}

	return nil
}

// Run renders template in application directory.
func (RenderTemplate) Run(ctx *create_ctx.CreateCtx, templateCtx *app_template.TemplateCtx) error {
	templateFileNamePattern := regexp.MustCompile(`^(.*)\.tt\.template$`)

	err := filepath.Walk(templateCtx.AppPath,
		func(filePath string, fileInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			return render(templateCtx, templateFileNamePattern, filePath, fileInfo)
		})
	if err != nil {
		return fmt.Errorf("template instantiation error: %w", err)
	}

	return nil
}
