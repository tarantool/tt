package steps

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/apex/log"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// CreateTemporaryAppDirectory represents create temporary application directory step.
type CreateTemporaryAppDirectory struct {
}

// Run creates temporary application directory.
func (CreateTemporaryAppDirectory) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx) error {
	var appDirectory string
	var err error

	if createCtx.AppName == "" {
		return fmt.Errorf("application name cannot be empty")
	}

	if createCtx.DestinationDir != "" {
		appDirectory = filepath.Join(createCtx.DestinationDir, createCtx.AppName)
	} else {
		appDirectory = path.Join(createCtx.WorkDir, createCtx.AppName)
	}

	if _, err = os.Stat(appDirectory); err == nil {
		if !createCtx.ForceMode {
			return fmt.Errorf("application %s already exists: %s", createCtx.AppName, appDirectory)
		}
	}

	appDirectory, err = filepath.Abs(appDirectory)
	if err != nil {
		return err
	}

	log.Infof("Creating application in %q", appDirectory)
	templateCtx.TargetAppPath = appDirectory

	templateCtx.AppPath, err = os.MkdirTemp("", createCtx.AppName+"*")
	if err != nil {
		return fmt.Errorf("failed to create temporary application directory: %s", err)
	}

	return nil
}
