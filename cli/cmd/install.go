package cmd

import (
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/install"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/search"
)

var (
	reinstall     bool
	force         bool
	noclean       bool
	localRepo     bool
	buildInDocker bool
)

// NewInstallCmd creates install command.
func NewInstallCmd() *cobra.Command {
	var installCmd = &cobra.Command{
		Use:   "install <PROGRAM> [flags]",
		Short: "Install program",
		Long: "Install program\n\n" +
			"Available programs:\n" +
			"tt - Tarantool CLI\n" +
			"tarantool - Tarantool\n" +
			"tarantool-ee - Tarantool enterprise edition\n" +
			"Example: tt install tarantool | tarantool" + search.VersionCliSeparator + "version",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalInstallModule, args)
			handleCmdErr(cmd, err)
		},
		ValidArgs: []string{"tt", "tarantool", "tarantool-ee"},
	}
	installCmd.Flags().BoolVarP(&force, "force", "f", false, "force requirements errors")
	installCmd.Flags().BoolVarP(&noclean, "no-clean", "", false,
		"don't delete temporary files")
	installCmd.Flags().BoolVarP(&reinstall, "reinstall", "", false, "reinstall program")
	installCmd.Flags().BoolVarP(&localRepo, "local-repo", "", false,
		"install from local files")
	installCmd.Flags().BoolVarP(&buildInDocker, "use-docker", "", false,
		"build tarantool in Ubuntu 16.04 docker container")
	return installCmd
}

// internalInstallModule is a default install module.
func internalInstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	installCtx := install.InstallCtx{
		Force:         force,
		Noclean:       noclean,
		Reinstall:     reinstall,
		Local:         localRepo,
		BuildInDocker: buildInDocker,
	}

	if err = install.FillCtx(cmdCtx, &installCtx, args); err != nil {
		return err
	}

	err = install.Install(args, cliOpts.App.BinDir, filepath.Join(cliOpts.App.IncludeDir,
		"include"), installCtx, cliOpts.Repo.Install, cliOpts)
	return err
}
