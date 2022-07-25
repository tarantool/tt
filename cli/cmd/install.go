package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/install"
	"github.com/tarantool/tt/cli/modules"
)

var (
	installReinstall bool
	installForce     bool
	installVerbose   bool
	installNoclean   bool
)

func getInstallFlags() *install.FlagInstall {
	installFlags := install.FlagInstall{
		InstallReinstall: installReinstall,
		InstallForce:     installForce,
		InstallVerbose:   installVerbose,
		InstallNoclean:   installNoclean,
	}
	return &installFlags
}

// NewInstallCmd creates install command.
func NewInstallCmd() *cobra.Command {
	var installCmd = &cobra.Command{
		Use:   "install [OPTIONS] what",
		Short: "install tarantool/tt",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalInstallModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}
	installCmd.Flags().BoolVarP(&installVerbose, "verbose", "V", false, "print log to stderr")
	installCmd.Flags().BoolVarP(&installForce, "force", "f", false, "force requirements errors")
	installCmd.Flags().BoolVarP(&installNoclean, "no-clean-tmp", "", false,
		"don't remove tmp files")
	installCmd.Flags().BoolVarP(&installReinstall, "reinstall", "", false, "reinstall programm")
	return installCmd
}

// internalInstallModule is a default install module.
func internalInstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	flags := getInstallFlags()
	err = install.Install(args, cliOpts.App.BinDir, flags)
	return err
}
