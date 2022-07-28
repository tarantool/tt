package steps

import (
	"fmt"
	"os"
	"path"
	"time"

	"github.com/apex/log"
	"github.com/codeclysm/extract/v3"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
	"golang.org/x/net/context"
)

func isDir(filePath string) bool {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	return fileInfo.IsDir()
}

func isRegularFile(filePath string) bool {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	return fileInfo.Mode().IsRegular()
}

func extractTemplate(archivePath string, dstPath string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("Error opening %s: %s", archivePath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	if err := extract.Gz(ctx, archive, dstPath, func(s string) string { return s }); err != nil {
		return fmt.Errorf("Template archive extraction failed: %s", err)
	}
	return nil
}

type CopyAppTemplate struct {
}

func (CopyAppTemplate) Run(ctx cmdcontext.CreateCtx, templateCtx *templates.TemplateCtx) error {
	templateName := ctx.TemplateName

	for _, templatesLocation := range ctx.Paths {
		templatePath := path.Join(templatesLocation, templateName)
		if isDir(templatePath) {
			log.Infof("Using template from %s", templatePath)
			return copy.Copy(templatePath, templateCtx.AppPath)
		}

		archivesToCheck := [2]string{
			path.Join(templatesLocation, templateName+".tgz"),
			path.Join(templatesLocation, templateName+".tar.gz"),
		}
		for _, archivePath := range archivesToCheck {
			if isRegularFile(archivePath) {
				log.Infof("Using template from %s", archivePath)
				return extractTemplate(archivePath, templateCtx.AppPath)
			}
		}
	}

	return fmt.Errorf("Template %s is not found", templateName)
}
