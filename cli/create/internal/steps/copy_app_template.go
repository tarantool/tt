package steps

import (
	"fmt"
	"path"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/util"
)

// CopyAppTemplate represents template -> app directory copy step.
type CopyAppTemplate struct {
}

// Run copies/extracts application template to target application directory.
func (CopyAppTemplate) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx) error {
	templateName := createCtx.TemplateName

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

	return fmt.Errorf("Template '%s' is not found", templateName)
}
