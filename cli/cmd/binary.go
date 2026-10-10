package cmd

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errNotSupportedProgram = errors.New("not supported program: ")
)

var binarySupportedPrograms = []string{
	binary.ProgramCe.String(),
	binary.ProgramEe.String(),
	binary.ProgramTt.String(),
	binary.ProgramTcm.String(),
}

const binarySwitchMaxArgs = 2

// NewBinaryCmd creates the command for managing binaries and downloading SDK bundles.
func NewBinaryCmd() *cobra.Command {
	binaryCmd := &cobra.Command{
		Use:   "binary",
		Short: "Manage binaries",
	}

	switchCmd := &cobra.Command{
		Use:   "switch [program] [version]",
		Short: "Switch to installed binary",
		Example: `# Switch without any arguments.

	$ tt binary switch

You will need to choose program and version using arrow keys in your console.

# Switch without version.

	$ tt binary switch tarantool

You will need to choose version using arrow keys in your console.

# Switch with program and version.

	$ tt binary switch tarantool 3.0.0`,
		RunE: internalRunE(internalSwitchModule),
		Args: cobra.MatchAll(
			cobra.MaximumNArgs(binarySwitchMaxArgs), binarySwitchValidateArgs),
		ValidArgs: binarySupportedPrograms,
	}
	listCmd := &cobra.Command{
		Use:   listCmdName,
		Short: "Show a list of installed binaries and their versions.",
		RunE:  internalRunE(internalListModule),
	}

	binaryCmd.AddCommand(
		newBinaryInstallCmd(),
		newBinaryUninstallCmd(),
		newBinarySearchCmd(),
		listCmd,
		switchCmd,
	)

	return binaryCmd
}

// binarySwitchValidateArgs validates non-flag arguments of 'binary switch' command.
func binarySwitchValidateArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		if !slices.Contains(binarySupportedPrograms, args[0]) {
			return fmt.Errorf("%w%s", errNotSupportedProgram, args[0])
		}
	}

	return nil
}

// internalSwitchModule is a switch module.
func internalSwitchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var (
		switchCtx binary.SwitchCtx
		err       error
	)

	if len(args) > 0 {
		switchCtx.Program, err = binary.ParseProgram(args[0])
		if err != nil {
			return fmt.Errorf("failed to switch module: %w", err)
		}
	} else {
		switchCtx.Program, err = binary.ChooseProgram(binarySupportedPrograms)
		if err != nil {
			return err
		}
	}

	if len(args) > 1 {
		switchCtx.Version = args[1]
	} else {
		switchCtx.Version, err = binary.ChooseVersion(cliOpts.Env.BinDir, switchCtx.Program)
		if err != nil {
			return err
		}
	}

	switchCtx.BinDir = cliOpts.Env.BinDir
	switchCtx.IncDir = cliOpts.Env.IncludeDir

	err = binary.Switch(&switchCtx)

	return err
}

// internalListModule is a list module.
func internalListModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	return binary.ListBinaries(cmdCtx, cliOpts)
}

var installCtx binary.InstallCtx

// newInstallTtCmd creates a command to install tt.
func newInstallTtCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTt.String() + " [version|commit hash|pull-request]",
		Short: "Install tt",
		RunE:  internalRunE(internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	return tntCmd
}

// newInstallTarantoolCmd creates a command to install tarantool.
func newInstallTarantoolCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String() + " [version|commit hash|pull-request]",
		Short: "Install tarantool community edition",
		RunE:  internalRunE(internalInstallModule),
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
	tntCmd := &cobra.Command{
		Use:   binary.ProgramEe.String() + " [version]",
		Short: "Install tarantool enterprise edition",
		RunE:  internalRunE(internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "install development build")

	return tntCmd
}

func newInstallTcmCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String() + " [version]",
		Short: "Install tarantool cluster manager",
		RunE:  internalRunE(internalInstallModule),
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
			"  tt binary install tarantool-dev ~/src/tarantool/build\n" +
			"  tt binary list # shows the binary installed above",
		RunE: internalRunE(internalInstallModule),
		Args: cobra.ExactArgs(1),
	}

	tntCmd.Flags().StringVar(&installCtx.IncDir, "include-dir", "",
		"tarantool headers directory")

	return tntCmd
}

// newBinaryInstallCmd creates install command.
func newBinaryInstallCmd() *cobra.Command {
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install program",
		Example: `# Install latest Tarantool version.

    $ tt binary install tarantool

# Install specific tt pull-request.

    $ tt binary install tt pr/534

# Install Tarantool 3.0.0 with limit number of simultaneous jobs for make.

    $ MAKEFLAGS="-j2" tt binary install tarantool 3.0.0`,
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
		newBinaryInstallSDKCmd(),
	)

	return installCmd
}

// internalInstallModule is a default install module.
func internalInstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var err error

	err = binary.FillCtx(cmdCtx, &installCtx, args)
	if err != nil {
		return err
	}

	err = binary.Install(installCtx, cliOpts)

	return err
}

// newUninstallTtCmd creates a command to install tt.
func newUninstallTtCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   "tt [version]",
		Short: "Uninstall tt",
		RunE:  internalRunE(internalUninstallModule),
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return []string{}, cobra.ShellCompDirectiveNoFileComp
			}

			return binary.GetList(cliOpts, cmd.Name()),
				cobra.ShellCompDirectiveNoFileComp
		},
	}

	return tntCmd
}

// newUninstallTarantoolCmd creates a command to install tarantool.
func newUninstallTarantoolCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String() + " [version]",
		Short: "Uninstall tarantool community edition",
		RunE:  internalRunE(internalUninstallModule),
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return []string{}, cobra.ShellCompDirectiveNoFileComp
			}

			return binary.GetList(cliOpts, cmd.Name()),
				cobra.ShellCompDirectiveNoFileComp
		},
	}

	return tntCmd
}

// newUninstallTarantoolEeCmd creates a command to install tarantool-ee.
func newUninstallTarantoolEeCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramEe.String() + " [version]",
		Short: "Uninstall tarantool enterprise edition",
		RunE:  internalRunE(internalUninstallModule),
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return []string{}, cobra.ShellCompDirectiveNoFileComp
			}

			return binary.GetList(cliOpts, cmd.Name()),
				cobra.ShellCompDirectiveNoFileComp
		},
	}

	return tntCmd
}

// newUninstallTarantoolDevCmd creates a command to uninstall tarantool-dev.
func newUninstallTarantoolDevCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   "tarantool-dev",
		Short: "Uninstall tarantool-dev",
		RunE:  internalRunE(internalUninstallModule),
		Args:  cobra.ExactArgs(0),
	}

	return tntCmd
}

// newUninstallTcmCmd creates a command to install tarantool-ee.
func newUninstallTcmCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String() + " [version]",
		Short: "Uninstall tarantool cluster manager",
		RunE:  internalRunE(internalUninstallModule),
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return []string{}, cobra.ShellCompDirectiveNoFileComp
			}

			return binary.GetList(cliOpts, cmd.Name()),
				cobra.ShellCompDirectiveNoFileComp
		},
	}

	return tntCmd
}

// newBinaryUninstallCmd creates uninstall command.
func newBinaryUninstallCmd() *cobra.Command {
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstalls a program",
		Example: `# To uninstall Tarantool:

    $ tt binary uninstall tarantool <version>`,
	}

	uninstallCmd.AddCommand(
		newUninstallTtCmd(),
		newUninstallTarantoolCmd(),
		newUninstallTarantoolEeCmd(),
		newUninstallTarantoolDevCmd(),
		newUninstallTcmCmd(),
	)

	return uninstallCmd
}

// internalUninstallModule is a default uninstall module.
func internalUninstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	program, err := binary.ParseProgram(cmdCtx.CommandName)
	if err != nil {
		return fmt.Errorf("failed to uninstall: %w", err)
	}

	programVersion := ""
	if len(args) == 1 {
		programVersion = args[0]
	}

	return binary.UninstallProgram(program, programVersion, cliOpts.Env.BinDir,
		cliOpts.Env.IncludeDir+"/include", cmdCtx)
}

var (
	local     bool
	debug     bool
	searchCtx = binary.NewSearchCtx(binary.NewPlatformInformer(), binary.NewTntIoDoer())
)

// newSearchTtCmd creates a command to search tt.
func newSearchTtCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTt.String(),
		Short: "Search for available tt versions",
		RunE:  internalRunE(internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}

	return tntCmd
}

// newSearchTarantoolCmd creates a command to search tarantool.
func newSearchTarantoolCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String(),
		Short: "Search for available tarantool community edition versions",
		RunE:  internalRunE(internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}

	return tntCmd
}

// newSearchTarantoolEeCmd creates a command to search tarantool-ee.
func newSearchTarantoolEeCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramEe.String(),
		Short: "Search for available tarantool enterprise edition versions",
		RunE:  internalRunE(internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}
	tntCmd.Flags().BoolVar(&debug, "debug", debug,
		"search for debug builds of tarantool-ee SDK")
	tntCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", searchCtx.ReleaseVersion,
		"specify version")
	tntCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"search for development builds of tarantool-ee SDK")

	return tntCmd
}

// newSearchTcmCmd creates a command to search tcm.
func newSearchTcmCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String(),
		Short: "Search for available tarantool cluster manager versions",
		RunE:  internalRunE(internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}
	tcmCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", searchCtx.ReleaseVersion,
		"specify version")
	tcmCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"search for development builds of TCM")

	return tcmCmd
}

// newBinarySearchCmd creates search command.
func newBinarySearchCmd() *cobra.Command {
	searchCmd := &cobra.Command{
		Use:   "search",
		Short: "Search for available versions for the program",
		Example: `# Remote search across all versions of Tarantool Enterprise Edition.

    $ tt binary search tarantool-ee

# Remote search across all 3.0 debug versions of Tarantool Enterprise Edition.

    $ tt binary search tarantool-ee --debug --version 3.0

# Remote search across all versions of Tarantool Cluster Manager.

	$ tt binary search tcm --version 1.3`,
	}
	searchCmd.Flags().BoolVarP(&local, "local-repo", "", false,
		"search in local files")

	searchCmd.AddCommand(
		newSearchTarantoolCmd(),
		newSearchTarantoolEeCmd(),
		newSearchTtCmd(),
		newSearchTcmCmd(),
	)

	return searchCmd
}

// internalSearchModule is a default search module.
func internalSearchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error

	searchCtx.Program, err = binary.ParseProgram(cmdCtx.CommandName)
	if err != nil {
		return fmt.Errorf("failed to search: %w", err)
	}

	if local {
		return binary.SearchVersionsLocal(searchCtx, cliOpts, cmdCtx.Cli.ConfigPath)
	}

	if debug {
		searchCtx.Filter = binary.SearchDebug
	}

	return binary.SearchVersions(searchCtx, cliOpts)
}

var (
	errInvalidNumberOfParameters                        = errors.New("invalid number of parameters")
	errToDownloadTarantoolSDKYouNeedToSpecifyTheVersion = errors.New(
		"to download Tarantool SDK, you need to specify the version",
	)
)

var downloadCtx binary.DownloadCtx

// newBinaryInstallSDKCmd creates a command that downloads and saves the Tarantool SDK.
func newBinaryInstallSDKCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "SDK <VERSION>",
		Short: `Download Tarantool SDK`,
		Example: `# Download Tarantool SDK to the current working directory.
	$ tt binary install SDK gc64-3.0.0-0-gf58f7d82a-r23
# Download Tarantool SDK development build to the /tmp directory.
	$ tt binary install SDK gc64-3.0.0-beta1-2-gcbb569b4c-r612 \
	    --dev --directory-prefix /tmp`,
		RunE: internalRunE(internalDownloadModule),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errToDownloadTarantoolSDKYouNeedToSpecifyTheVersion
			} else if len(args) > 1 {
				return errInvalidNumberOfParameters
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&downloadCtx.DevBuild, "dev", false, "download development build")
	cmd.Flags().StringVar(&downloadCtx.DirectoryPrefix,
		"directory-prefix", downloadCtx.DirectoryPrefix,
		`directory prefix to save SDK. The default is "." (the current directory)`)

	return cmd
}

func internalDownloadModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	downloadCtx.Version = args[0]
	return binary.DownloadSDK(cmdCtx, downloadCtx, cliOpts)
}
