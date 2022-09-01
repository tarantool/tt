package steps

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
)

// CreateAppDirectory represents create application directory step.
type CreateAppDirectory struct {
}

// Run creates target application directory.
func (CreateAppDirectory) Run(createCtx *cmdcontext.CreateCtx, templateCtx *TemplateCtx) error {
	var appDirectory string
	if createCtx.AppName == "" {
		return fmt.Errorf("Application name cannot be empty")
	}

	if createCtx.DestinationDir != "" {
		appDirectory = filepath.Join(createCtx.DestinationDir, createCtx.AppName)
	} else {
		appDirectory = path.Join(createCtx.WorkDir, createCtx.AppName)
	}

	if _, err := os.Stat(appDirectory); err == nil {
		if !createCtx.ForceMode {
			return fmt.Errorf("Application %s already exists: %s", createCtx.AppName, appDirectory)
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
