package pack

import (
	"fmt"
	"os"
	"path/filepath"

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
	packCtx.AppList = []string{filepath.Base(cmdCtx.Cli.ConfigDir)}
	var err error
	packCtx.AppsInfo, err = running.CollectInstancesForApps(packCtx.AppList, cliOpts,
		cmdCtx.Cli.ConfigDir, cmdCtx.Integrity, running.ConfigLoadScripts)
	if err != nil {
		return fmt.Errorf("failed to collect applications info: %s", err)
	}
	return nil
}

// setBundleName sets the name of the bundle.
func setBundleName(packCtx *PackCtx) {
	if packCtx.Name != "" {
		return
	}
	packCtx.Name = packCtx.AppList[0]
}

// FillCtx fills pack context.
func FillCtx(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx, cliOpts *config.CliOpts,
	args []string,
) error {
	if len(args) < 1 {
		return fmt.Errorf("package type is not provided")
	}

	packCtx.RpmDeb.pkgFilesInfo = make(map[string]packFileInfo)

	packCtx.TarantoolIsSystem = cmdCtx.Cli.IsSystem
	packCtx.TarantoolExecutable = cmdCtx.Cli.TarantoolCli.Executable
	packCtx.configFilePath = cmdCtx.Cli.ConfigPath
	packCtx.Type = args[0]

	if err := initAppsInfo(cliOpts, cmdCtx, packCtx); err != nil {
		return fmt.Errorf("error collect applications info: %s", err)
	}

	setBundleName(packCtx)

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
