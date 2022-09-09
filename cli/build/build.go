package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/cmdcontext"
)

// FillCtx fills build context.
func FillCtx(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("too many args")
	} else if len(args) == 1 {
		appPath := args[0]
		if !filepath.IsAbs(appPath) {
			var err error
			appPath, err = filepath.Abs(filepath.Join(cmdCtx.Cli.WorkDir, appPath))
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
		cmdCtx.Build.BuildDir = appPath
	} else {
		cmdCtx.Build.BuildDir = cmdCtx.Cli.WorkDir
	}

	return nil
}

// Run builds an application.
func Run(cmdCtx *cmdcontext.CmdCtx) error {
	return buildLocal(cmdCtx)
}
