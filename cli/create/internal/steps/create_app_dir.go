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

func (CreateAppDirectory) Run(ctx cmdcontext.CreateCtx, templateCtx *templates.TemplateCtx) error {
	appDirectory := path.Join(ctx.InstancesDir, ctx.AppName)
	log.Infof("Creating application in %s", appDirectory)

	if _, err := os.Stat(appDirectory); err == nil {
		return fmt.Errorf("Application %s already exists: %s", ctx.AppName, appDirectory)
	}

	if err := os.Mkdir(appDirectory, os.FileMode(0755)); err != nil {
		return fmt.Errorf("Error create application dir: %s", err)
	}

	templateCtx.AppPath = appDirectory

	return nil
}
