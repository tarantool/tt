package cmd

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/install"
	"github.com/tarantool/tt/cli/modules"
)

var (
	Reinstall bool
	Force     bool
	Verbose   bool
	Noclean   bool
	Local     bool
)

func getInstallFlags() install.InstallationFlag {
	return install.InstallationFlag{
		Reinstall: Reinstall,
		Force:     Force,
		Verbose:   Verbose,
		Noclean:   Noclean,
		Local:     Local,
	}
}

// NewInstallCmd creates install command.
func NewInstallCmd() *cobra.Command {
	var installCmd = &cobra.Command{
		Use:   "install <PROGRAM> [flags]",
		Short: "Install tarantool/tt",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalInstallModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}
	installCmd.Flags().BoolVarP(&Verbose, "verbose", "V", false, "print log to stderr")
	installCmd.Flags().BoolVarP(&Force, "force", "f", false, "force requirements errors")
	installCmd.Flags().BoolVarP(&Noclean, "no-clean", "", false,
		"don't delete temporary files")
	installCmd.Flags().BoolVarP(&Reinstall, "reinstall", "", false, "reinstall program")
	installCmd.Flags().BoolVarP(&Local, "local", "", false,
		"install from local files")
	return installCmd
}

// internalInstallModule is a default install module.
func internalInstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}
	flags := getInstallFlags()
	if _, err := os.Stat(cmdCtx.Cli.ConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("There is no tarantool.yaml found, please create one")
	}
	err = install.Install(args, cliOpts.App.BinDir, cliOpts.App.IncludeDir, flags,
		cliOpts.Repo.Install, cliOpts)
	return err
}
