package steps

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
)

// CreateTemporaryAppDirectory represents create temporary application directory step.
type CreateTemporaryAppDirectory struct {
}

// Run creates temporary application directory.
func (CreateTemporaryAppDirectory) Run(createCtx *cmdcontext.CreateCtx,
	templateCtx *TemplateCtx) error {
	var appDirectory string
	var err error

	if createCtx.AppName == "" {
		return fmt.Errorf("Application name cannot be empty")
	}

	if createCtx.DestinationDir != "" {
		appDirectory = filepath.Join(createCtx.DestinationDir, createCtx.AppName)
	} else {
		appDirectory = path.Join(createCtx.WorkDir, createCtx.AppName)
	}

	if _, err = os.Stat(appDirectory); err == nil {
		if !createCtx.ForceMode {
			return fmt.Errorf("Application %s already exists: %s", createCtx.AppName, appDirectory)
		}
	}

	log.Infof("Creating application in %s", appDirectory)
	templateCtx.TargetAppPath = appDirectory

	templateCtx.AppPath, err = ioutil.TempDir("", createCtx.AppName+"*")
	if err != nil {
		return fmt.Errorf("Failed to create temporary application directory: %s", err)
	}

	return nil
}
