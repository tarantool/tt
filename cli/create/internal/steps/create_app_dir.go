package steps

import (
	"fmt"
	"os"
	"path"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

type CreateAppDirectory struct {
}

// Run creates target application directory.
func (CreateAppDirectory) Run(ctx cmdcontext.CreateCtx, templateCtx *templates.TemplateCtx) error {
	var appDirectory string
	if ctx.AppName == "" {
		appDirectory = path.Join(ctx.InstancesDir, ctx.TemplateName)
	} else {
		appDirectory = path.Join(ctx.InstancesDir, ctx.AppName)
	}

	if _, err := os.Stat(appDirectory); err == nil {
		if !ctx.ForceMode {
			return fmt.Errorf("Application %s already exists: %s", ctx.AppName, appDirectory)
		}
		if err := os.RemoveAll(appDirectory); err != nil {
			return fmt.Errorf("Failed to remove %s: %s", appDirectory, err)
		}
	}

	if err := os.Mkdir(appDirectory, os.FileMode(0755)); err != nil {
		return fmt.Errorf("Error create application dir %s: %s", appDirectory, err)
	}

	log.Infof("Creating application in %s", appDirectory)
	templateCtx.AppPath = appDirectory

	return nil
}
