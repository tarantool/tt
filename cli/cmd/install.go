package cmd

import (
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/install"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/version"
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
			"tarantool-ee - Tarantool enterprise edition\n",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalInstallModule, args)
			handleCmdErr(cmd, err)
		},
		Example: `
# Install latest Tarantool version.

    $ tt install tarantool

# Install Tarantool 2.10.5 with limit number of simultaneous jobs for make.

    $ MAKEFLAGS="-j2" tt install tarantool` + version.CliSeparator + "2.10.5",
		ValidArgs: []string{"tt", "tarantool", "tarantool-ee"},
	}
	installCmd.Flags().BoolVarP(&force, "force", "f", false,
		"don't do a dependency check before installing")
	installCmd.Flags().BoolVarP(&noclean, "no-clean", "", false,
		"don't delete temporary files")
	installCmd.Flags().BoolVarP(&reinstall, "reinstall", "", false, "reinstall program")
	installCmd.Flags().BoolVarP(&localRepo, "local-repo", "", false,
		"install from local files")
	installCmd.Flags().BoolVarP(&buildInDocker, "use-docker", "", false,
		"build tarantool in Ubuntu 18.04 docker container")
	return installCmd
}

// internalInstallModule is a default install module.
func internalInstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	installCtx := install.InstallCtx{
		Force:         force,
		Noclean:       noclean,
		Reinstall:     reinstall,
		Local:         localRepo,
		BuildInDocker: buildInDocker,
	}

	var err error
	if err = install.FillCtx(cmdCtx, &installCtx, args); err != nil {
		return err
	}

	err = install.Install(args, cliOpts.App.BinDir, filepath.Join(cliOpts.App.IncludeDir,
		"include"), installCtx, cliOpts.Repo.Install, cliOpts)
	return err
}
