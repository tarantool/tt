package pack

import (
	"errors"
	"fmt"
	"os"
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
		cmdCtx.Cli.ConfigDir, cmdCtx.Integrity, running.ConfigLoadScripts)
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

// setBundleName sets the name of the bundle.
func setBundleName(packCtx *PackCtx, cliOpts *config.CliOpts) {
	if packCtx.Name != "" {
		return
	}
	packCtx.Name = "package"
	if packCtx.CartridgeCompat || cliOpts.Env.InstancesEnabled == "." {
		packCtx.Name = packCtx.AppList[0]
		return
	}
	if packCtx.configFilePath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Warnf("failed to get current working dir: %s", err)
			return
		}
		packCtx.Name = filepath.Base(cwd)
		return
	}
	packCtx.Name = filepath.Base(filepath.Dir(packCtx.configFilePath))
}

// FillCtx fills pack context.
func FillCtx(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx, cliOpts *config.CliOpts,
	args []string,
) error {
	if len(args) < 1 {
		return fmt.Errorf("package type is not provided")
	}

	packCtx.RpmDeb.pkgFilesInfo = make(map[string]packFileInfo)

	if (packCtx.IntegrityPrivateKey != "") && packCtx.CartridgeCompat {
		return errors.New("cannot pack with integrity checks in cartridge-compat mode")
	}

	packCtx.TarantoolIsSystem = cmdCtx.Cli.IsSystem
	packCtx.TarantoolExecutable = cmdCtx.Cli.TarantoolCli.Executable
	packCtx.configFilePath = cmdCtx.Cli.ConfigPath
	packCtx.Type = args[0]

	if err := initAppsInfo(cliOpts, cmdCtx, packCtx); err != nil {
		return fmt.Errorf("error collect applications info: %s", err)
	}

	setBundleName(packCtx, cliOpts)

	if packCtx.CartridgeCompat && len(packCtx.AppsInfo) > 1 {
		return fmt.Errorf("cannot pack multiple applications in cartridge compat mode")
	}

	// Initialize packignore filter.
	ignoreFilter, err := createIgnoreFilter(util.GetOsFS(), cmdCtx.Cli.ConfigDir, ignoreFile)
	if err != nil {
		return fmt.Errorf("failed to initialize packignore filter: %w", err)
	}
	packCtx.skipFunc = func(srcinfo os.FileInfo, src, dest string) (bool, error) {
		return ignoreFilter.shouldSkip(srcinfo, src), nil
	}

	return nil
}
