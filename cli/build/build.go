package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/cmdcontext"
)

// FillCtx fills build context.
func FillCtx(buildCtx *cmdcontext.BuildCtx, args []string) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}

	if len(args) > 1 {
		return fmt.Errorf("too many args")
	} else if len(args) == 1 {
		appPath := args[0]
		if !filepath.IsAbs(appPath) {
			var err error
			appPath, err = filepath.Abs(filepath.Join(workingDir, appPath))
			if err != nil {
				return err
			}
		}
		fileInfo, err := os.Stat(appPath)
		if err != nil {
			return err
		}
		if !fileInfo.IsDir() {
			return fmt.Errorf("%s is not a directory", appPath)
		}
		buildCtx.BuildDir = appPath
	} else {
		buildCtx.BuildDir = workingDir
	}

	return nil
}

// Run builds an application.
func Run(cmdCtx *cmdcontext.CmdCtx, buildCtx *cmdcontext.BuildCtx) error {
	return buildLocal(cmdCtx, buildCtx)
}
