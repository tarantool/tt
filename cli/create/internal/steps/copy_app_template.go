package steps

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/apex/log"
	"github.com/codeclysm/extract/v3"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/util"
	"golang.org/x/net/context"
)

// extractTemplate extract archivePath archive to dstPath.
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

// CopyAppTemplate represents template -> app directory copy step.
type CopyAppTemplate struct {
}

// Run copies/extracts application template to target application directory.
func (CopyAppTemplate) Run(createCtx *cmdcontext.CreateCtx, templateCtx *TemplateCtx) error {
	templateName := createCtx.TemplateName

	for _, templatesSearchPath := range createCtx.TemplateSearchPaths {
		var templatesLocation string
		if filepath.IsAbs(templatesSearchPath) {
			templatesLocation = templatesSearchPath
		} else {
			templatesLocation = filepath.Join(createCtx.ConfigLocation, templatesSearchPath)
		}
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
				return extractTemplate(archivePath, templateCtx.AppPath)
			}
		}
	}

	return fmt.Errorf("Template '%s' is not found", templateName)
}
