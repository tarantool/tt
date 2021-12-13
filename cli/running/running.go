package running

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/tarantool/tt/cli/context"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// findAppFile searches of an application init file.
func findAppFile(appName string, cliOpts *modules.CliOpts) (string, error) {
	var err error
	appDir := cliOpts.App.InstancesAvailable
	if appDir == "" {
		if appDir, err = os.Getwd(); err != nil {
			return "", err
		}
	}

	var appPath string

	// We considering several scenarios:
	// 1) The application starts by `appName.lua`
	// 2) The application starts by `appName/init.lua`
	appAbsPath, err := util.JoinAbspath(appDir, appName+".lua")
	if err != nil {
		return "", err
	}
	dirAbsPath, err := util.JoinAbspath(appDir, appName)
	if err != nil {
		return "", err
	}

	// Check if one or both file and/or directory exist.
	_, fileStatErr := os.Stat(appAbsPath)
	dirInfo, dirStatErr := os.Stat(dirAbsPath)

	if !os.IsNotExist(fileStatErr) {
		if fileStatErr != nil {
			return "", fileStatErr
		}
		appPath = appAbsPath
	} else if dirStatErr == nil && dirInfo.IsDir() {
		appPath = path.Join(dirAbsPath, "init.lua")
		if _, err = os.Stat(appPath); err != nil {
			return "", err
		}
	} else {
		return "", fileStatErr
	}

	return appPath, nil
}

// cleanup removes runtime artifacts.
func cleanup(ctx *context.Ctx) {
	if _, err := os.Stat(ctx.Running.ConsoleSocket); err == nil {
		os.Remove(ctx.Running.ConsoleSocket)
	}
}

// FillCtx fills the RunningCtx context.
func FillCtx(cliOpts *modules.CliOpts, ctx *context.Ctx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("Currently, you can run only one instance at a time.")
	}

	appName := args[0]
	appPath, err := findAppFile(appName, cliOpts)
	if err != nil {
		return fmt.Errorf("Can't find an application init file: %s", err)
	}

	ctx.Running.AppPath = appPath

	runDir := cliOpts.App.RunDir
	if runDir == "" {
		if runDir, err = os.Getwd(); err != nil {
			return fmt.Errorf(`Can't get the "RunDir: %s"`, err)
		}
	}
	ctx.Running.RunDir = runDir
	ctx.Running.ConsoleSocket = filepath.Join(runDir, appName+".control")

	return nil
}

// Start an Instance.
func Start(ctx *context.Ctx) error {
	defer cleanup(ctx)

	inst, err := NewInstance(ctx.Cli.TarantoolExecutable,
		ctx.Running.AppPath, ctx.Running.ConsoleSocket, os.Environ())
	if err != nil {
		return err
	}

	wd := NewWatchdog(inst, ctx.Running.Restartable, 5*time.Second)
	wd.Start()

	return nil
}
