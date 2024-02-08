package pack

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

type PackageType string

const (
	Tgz    = "tgz"
	Rpm    = "rpm"
	Deb    = "deb"
	Docker = "docker"
)

// initAppsInfo collects environment applications info, set related pack context fields.
func initAppsInfo(cliOpts *config.CliOpts, cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx) error {
	// Collect applications info.
	var err error
	appList := []string{}
	if packCtx.AppList == nil {
		appList, err = util.CollectAppList(cmdCtx.Cli.ConfigDir, cliOpts.Env.InstancesEnabled,
			true)
		if err != nil {
			return err
		}
	} else {
		for _, appName := range packCtx.AppList {
			if util.IsApp(filepath.Join(cliOpts.Env.InstancesEnabled, appName)) {
				appList = append(appList, appName)
			} else {
				log.Warnf("Skip packing of '%s': specified name is not an application.", appName)
			}
		}
	}

	if len(appList) == 0 {
		err = fmt.Errorf("there are no apps found in instance_enabled directory")
		return err
	}
	packCtx.AppList = appList
	packCtx.AppsInfo, err = running.CollectInstancesForApps(packCtx.AppList, cliOpts,
		cmdCtx.Cli.ConfigDir, cmdCtx.Integrity)
	if err != nil {
		return fmt.Errorf("failed to collect applications info: %s", err)
	}
	return nil
}

// getPackageName return result environment name for the package.
func getPackageName(cmdCtx cmdcontext.CmdCtx) (string, error) {
	if len(cmdCtx.Cli.ConfigDir) == 0 {
		absPath, err := filepath.Abs(".")
		if err != nil {
			return "", fmt.Errorf("cannot get path of current dir: %s", err)
		}
		return filepath.Base(absPath), nil
	}
	return filepath.Base(cmdCtx.Cli.ConfigDir), nil
}

// FillCtx fills pack context.
func FillCtx(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx, cliOpts *config.CliOpts,
	args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("package type is not provided")
	}

	if (packCtx.IntegrityPrivateKey != "") && packCtx.CartridgeCompat {
		return errors.New("cannot pack with integrity checks in cartridge-compat mode")
	}

	packCtx.TarantoolIsSystem = cmdCtx.Cli.IsSystem
	packCtx.TarantoolExecutable = cmdCtx.Cli.TarantoolCli.Executable
	packCtx.Type = args[0]

	if err := initAppsInfo(cliOpts, cmdCtx, packCtx); err != nil {
		return fmt.Errorf("error collect applications info: %s", err)
	}

	// If cartridge-compat is set and package name is not set, no need to set package name here,
	// its name will be taken from the application name, which is packed in cartridge-compat mode.
	if packCtx.Name == "" && !packCtx.CartridgeCompat {
		var err error
		if packCtx.Name, err = getPackageName(*cmdCtx); err != nil {
			return fmt.Errorf("cannot get environment: %s", err)
		}
	}

	return nil
}
