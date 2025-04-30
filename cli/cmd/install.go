package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/install"
	"github.com/tarantool/tt/cli/search"
)

var installCtx install.InstallCtx

// newInstallTtCmd creates a command to install tt.
func newInstallTtCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   search.ProgramTt.String() + " [version|commit hash|pull-request]",
		Short: "Install tt",
		Run:   RunModuleFunc(internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	return tntCmd
}

// newInstallTarantoolCmd creates a command to install tarantool.
func newInstallTarantoolCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   search.ProgramCe.String() + " [version|commit hash|pull-request]",
		Short: "Install tarantool community edition",
		Run:   RunModuleFunc(internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().BoolVarP(&installCtx.BuildInDocker, "use-docker", "", false,
		"build tarantool in Ubuntu 18.04 docker container")
	tntCmd.Flags().BoolVarP(&installCtx.Dynamic, "dynamic", "", false,
		"use dynamic linking for building tarantool")

	return tntCmd
}

// newInstallTarantoolEeCmd creates a command to install tarantool-ee.
func newInstallTarantoolEeCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   search.ProgramEe.String() + " [version]",
		Short: "Install tarantool enterprise edition",
		Run:   RunModuleFunc(internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "install development build")

	return tntCmd
}

func newInstallTcmCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   search.ProgramTcm.String() + " [version]",
		Short: "Install tarantool cluster manager",
		Run:   RunModuleFunc(internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "install development build")

	return tntCmd
}

// newInstallTarantoolDevCmd creates a command to install tarantool
// from the local build directory.
func newInstallTarantoolDevCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   "tarantool-dev <DIRECTORY>",
		Short: "Install tarantool from the local build directory",
		Example: "Assume, tarantool build directory is ~/src/tarantool/build\n" +
			"  Consider the following use case:\n\n" +
			"  make -j16 -C ~/src/tarantool/build\n" +
			"  tt install tarantool-dev ~/src/tarantool/build\n" +
			"  tt run # runs the binary compiled above",
		Run:  RunModuleFunc(internalInstallModule),
		Args: cobra.ExactArgs(1),
	}

	tntCmd.Flags().StringVar(&installCtx.IncDir, "include-dir", "",
		"tarantool headers directory")
	return tntCmd
}

// NewInstallCmd creates install command.
func NewInstallCmd() *cobra.Command {
	var installCmd = &cobra.Command{
		Use:   "install",
		Short: "Install program",
		Example: `
# Install latest Tarantool version.

    $ tt install tarantool

# Install specific tt pull-request.

    $ tt install tt pr/534

# Install Tarantool 2.10.5 with limit number of simultaneous jobs for make.

    $ MAKEFLAGS="-j2" tt install tarantool 2.10.5`,
	}
	installCmd.Flags().BoolVarP(&installCtx.Force, "force", "f", false,
		"don't do a dependency check before installing")
	installCmd.Flags().BoolVarP(&installCtx.KeepTemp, "no-clean", "", false,
		"don't delete temporary files")
	installCmd.Flags().BoolVarP(&installCtx.Reinstall, "reinstall", "", false, "reinstall program")
	installCmd.Flags().BoolVarP(&installCtx.Local, "local-repo", "", false,
		"install from local files")

	installCmd.AddCommand(
		newInstallTtCmd(),
		newInstallTarantoolCmd(),
		newInstallTarantoolEeCmd(),
		newInstallTarantoolDevCmd(),
		newInstallTcmCmd(),
	)

	return installCmd
}

// internalInstallModule is a default install module.
func internalInstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var err error
	if err = install.FillCtx(cmdCtx, &installCtx, args); err != nil {
		return err
	}

	err = install.Install(installCtx, cliOpts)
	return err
}
