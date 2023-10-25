package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/install"
	"github.com/tarantool/tt/cli/modules"
)

var installCtx install.InstallCtx

// newInstallTtCmd creates a command to install tt.
func newInstallTtCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   "tt [version|commit hash|pull-request]",
		Short: "Install tt",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			installCtx.ProgramName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalInstallModule, args)
			handleCmdErr(cmd, err)
		},
	}

	return tntCmd
}

// newInstallTarantoolCmd creates a command to install tarantool.
func newInstallTarantoolCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   "tarantool [version|commit hash|pull-request]",
		Short: "Install tarantool community edition",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			installCtx.ProgramName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalInstallModule, args)
			handleCmdErr(cmd, err)
		},
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
		Use:   "tarantool-ee [version]",
		Short: "Install tarantool enterprise edition",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			installCtx.ProgramName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalInstallModule, args)
			handleCmdErr(cmd, err)
		},
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
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			installCtx.ProgramName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalInstallModule, args)
			handleCmdErr(cmd, err)
		},
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
	installCmd.Flags().BoolVarP(&installCtx.Noclean, "no-clean", "", false,
		"don't delete temporary files")
	installCmd.Flags().BoolVarP(&installCtx.Reinstall, "reinstall", "", false, "reinstall program")
	installCmd.Flags().BoolVarP(&installCtx.Local, "local-repo", "", false,
		"install from local files")

	installCmd.AddCommand(
		newInstallTtCmd(),
		newInstallTarantoolCmd(),
		newInstallTarantoolEeCmd(),
		newInstallTarantoolDevCmd(),
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

	err = install.Install(cliOpts.Env.BinDir, cliOpts.Env.IncludeDir,
		installCtx, cliOpts.Repo.Install, cliOpts)
	return err
}
