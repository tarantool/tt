package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// BuildCtx contains information for application building.
type BuildCtx struct {
	// BuildDir is an application directory.
	BuildDir string
	// SpecFile is a rockspec file to be used for build.
	SpecFile string
}

// FillCtx fills build context.
func FillCtx(buildCtx *BuildCtx, cliOpts *config.CliOpts, args []string) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}

	if len(args) > 1 {
		return fmt.Errorf("too many args")
	} else if len(args) == 1 {
		appPath := args[0]
		appName := filepath.Base(appPath)
		if !filepath.IsAbs(appPath) {
			var err error
			appPath, err = filepath.Abs(filepath.Join(workingDir, appPath))
			if err != nil {
				return err
			}
		}
		fileInfo, err := os.Stat(appPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			log.Debugf("%q does not exist. Looking for %q in instances enabled directory.",
				appPath, appName)
			appLink := filepath.Join(cliOpts.App.InstancesEnabled, appName)
			if appPath, err = util.ResolveSymlink(appLink); err != nil {
				return err
			}
			fileInfo, err = os.Stat(appPath)
			if err != nil {
				return err
			}
		}
		if !fileInfo.IsDir() {
			return fmt.Errorf("%s is not a directory", appPath)
		}
		buildCtx.BuildDir = appPath
		log.Debugf("Application %q build directory: %q", appName, buildCtx.BuildDir)
	} else {
		buildCtx.BuildDir = workingDir
	}

	return nil
}

// Run builds an application.
func Run(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, buildCtx *BuildCtx) error {
	return buildLocal(cmdCtx, cliOpts, buildCtx)
}
