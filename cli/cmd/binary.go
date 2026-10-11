package cmd

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/binary"
)

const binarySwitchMaxArgs = 2

var (
	errNotSupportedProgram                              = errors.New("not supported program: ")
	errInvalidNumberOfParameters                        = errors.New("invalid number of parameters")
	errToDownloadTarantoolSDKYouNeedToSpecifyTheVersion = errors.New(
		"to download Tarantool SDK, you need to specify the version",
	)
)

var binarySupportedPrograms = []string{
	binary.ProgramCe.String(),
	binary.ProgramEe.String(),
	binary.ProgramTt.String(),
	binary.ProgramTcm.String(),
}

// binaryFunc implements a binary command with common options.
type binaryFunc[T any] func(binary.BinaryContext, T, *cmdcontext.CmdCtx, []string) error

// NewBinaryCmd creates the command for managing binaries and downloading SDK bundles.
func NewBinaryCmd() *cobra.Command {
	var (
		base         binary.BinaryContext
		installCtx   binary.InstallCtx
		downloadCtx  binary.DownloadCtx
		uninstallCtx binary.UninstallCtx
		searchCtx    = binary.NewSearchCtx(binary.NewPlatformInformer(), binary.NewTntIoDoer())
		listCtx      binary.ListCtx
		switchCtx    binary.SwitchCtx
	)

	binaryCmd := &cobra.Command{
		Use:   "binary",
		Short: "Manage binaries and SDK bundles",
	}

	binaryCmd.PersistentFlags().BoolVarP(&base.Verbose, "verbose", "V", false,
		"Show diagnostic output and build commands")

	binaryCmd.AddCommand(
		newBinaryInstallCmd(&base, &installCtx, &downloadCtx),
		newBinaryUninstallCmd(&base, &uninstallCtx),
		newBinarySearchCmd(&base, &searchCtx),
		newBinaryListCmd(&base, &listCtx),
		newBinarySwitchCmd(&base, &switchCtx),
	)

	return binaryCmd
}

// newBinaryInstallCmd creates install command.
func newBinaryInstallCmd(base *binary.BinaryContext,
	installCtx *binary.InstallCtx, downloadCtx *binary.DownloadCtx,
) *cobra.Command {
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install a program",
		Example: `# Install the latest Tarantool version.
  $ tt binary install tarantool

  # Install tt from a pull request.
  $ tt binary install tt pr/534

  # Build Tarantool with two parallel jobs.
  $ MAKEFLAGS="-j2" tt binary install tarantool 3.0.0`,
	}
	installCmd.PersistentFlags().BoolVarP(&installCtx.Force, "force", "f", false,
		"Skip dependency checks")
	installCmd.PersistentFlags().BoolVar(&installCtx.KeepTemp, "no-clean", false,
		"Keep temporary files")
	installCmd.PersistentFlags().BoolVar(&installCtx.Reinstall, "reinstall", false,
		"Reinstall an existing version")
	installCmd.PersistentFlags().BoolVar(&installCtx.Local, "local-repo", false,
		"Install from the local repository")

	installCmd.AddCommand(
		newInstallTtCmd(base, installCtx),
		newInstallTarantoolCmd(base, installCtx),
		newInstallTarantoolEeCmd(base, installCtx),
		newInstallTarantoolDevCmd(base, installCtx),
		newInstallTcmCmd(base, installCtx),
		newBinaryInstallSDKCmd(base, downloadCtx),
	)

	return installCmd
}

// newInstallTtCmd creates a command to install tt.
func newInstallTtCmd(base *binary.BinaryContext,
	installCtx *binary.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTt.String() + " [version|commit|pull-request]",
		Short: "Install tt",
		RunE:  binaryRunE(base, installCtx, internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	return tntCmd
}

// newInstallTarantoolCmd creates a command to install tarantool.
func newInstallTarantoolCmd(base *binary.BinaryContext,
	installCtx *binary.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String() + " [version|commit|pull-request]",
		Short: "Install Tarantool Community Edition",
		RunE:  binaryRunE(base, installCtx, internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().BoolVar(&installCtx.BuildInDocker, "use-docker", false,
		"Build Tarantool in an Ubuntu 18.04 Docker container")
	tntCmd.Flags().BoolVar(&installCtx.Dynamic, "dynamic", false,
		"Build Tarantool with dynamic linking")

	return tntCmd
}

// newInstallTarantoolEeCmd creates a command to install tarantool-ee.
func newInstallTarantoolEeCmd(base *binary.BinaryContext,
	installCtx *binary.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramEe.String() + " [version]",
		Short: "Install Tarantool Enterprise Edition",
		RunE:  binaryRunE(base, installCtx, internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "Install a development build")

	return tntCmd
}

// newInstallTarantoolDevCmd creates a command to install tarantool
// from the local build directory.
func newInstallTarantoolDevCmd(base *binary.BinaryContext,
	installCtx *binary.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   "tarantool-dev <directory>",
		Short: "Install Tarantool from a local build directory",
		Example: `# Build and install Tarantool from a local source tree.
  $ make -j16 -C ~/src/tarantool/build
  $ tt binary install tarantool-dev ~/src/tarantool/build
  $ tt binary list`,
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: cobra.ExactArgs(1),
	}

	tntCmd.Flags().StringVar(&installCtx.IncDir, "include-dir", "",
		"Path to the Tarantool headers directory")

	return tntCmd
}

func newInstallTcmCmd(base *binary.BinaryContext,
	installCtx *binary.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String() + " [version]",
		Short: "Install Tarantool Cluster Manager",
		RunE:  binaryRunE(base, installCtx, internalInstallModule),
		Args:  cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "Install a development build")

	return tntCmd
}

// newBinaryInstallSDKCmd creates a command that downloads and saves the Tarantool SDK.
func newBinaryInstallSDKCmd(base *binary.BinaryContext,
	downloadCtx *binary.DownloadCtx,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sdk <version>",
		Aliases: []string{"SDK"},
		Short:   "Download a Tarantool SDK bundle",
		Example: `# Download an SDK bundle to the current directory.
  $ tt binary install sdk gc64-3.0.0-0-gf58f7d82a-r23

  # Download a development SDK bundle to /tmp.
  $ tt binary install sdk gc64-3.0.0-beta1-2-gcbb569b4c-r612 \
      --dev --directory-prefix /tmp`,
		RunE: binaryRunE(base, downloadCtx, internalDownloadModule),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errToDownloadTarantoolSDKYouNeedToSpecifyTheVersion
			} else if len(args) > 1 {
				return errInvalidNumberOfParameters
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&downloadCtx.DevBuild, "dev", false, "Download a development build")
	cmd.Flags().StringVar(&downloadCtx.DirectoryPrefix,
		"directory-prefix", "",
		`Directory to save the SDK (default: current directory)`)

	return cmd
}

// internalInstallModule is a default install module.
func internalInstallModule(base binary.BinaryContext, installCtx binary.InstallCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var err error

	installCtx.BinaryContext = base

	err = binary.FillCtx(cmdCtx, &installCtx, args)
	if err != nil {
		return err
	}

	err = binary.Install(installCtx, cliOpts)

	return err
}

func internalDownloadModule(base binary.BinaryContext, downloadCtx binary.DownloadCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	downloadCtx.BinaryContext = base
	downloadCtx.Program = binary.ProgramEe
	downloadCtx.Version = args[0]

	return binary.DownloadSDK(cmdCtx, downloadCtx, cliOpts)
}

// newBinaryUninstallCmd creates uninstall command.
func newBinaryUninstallCmd(base *binary.BinaryContext,
	uninstallCtx *binary.UninstallCtx,
) *cobra.Command {
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall a program",
		Example: `# Uninstall a Tarantool version.
  $ tt binary uninstall tarantool 3.0.0`,
	}

	uninstallCmd.AddCommand(
		newUninstallTtCmd(base, uninstallCtx),
		newUninstallTarantoolCmd(base, uninstallCtx),
		newUninstallTarantoolEeCmd(base, uninstallCtx),
		newUninstallTarantoolDevCmd(base, uninstallCtx),
		newUninstallTcmCmd(base, uninstallCtx),
	)

	return uninstallCmd
}

// newUninstallTtCmd creates a command to uninstall tt.
func newUninstallTtCmd(base *binary.BinaryContext,
	uninstallCtx *binary.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   "tt [version]",
		Short: "Uninstall tt",
		RunE:  binaryRunE(base, uninstallCtx, internalUninstallModule),
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

// newUninstallTarantoolCmd creates a command to uninstall tarantool.
func newUninstallTarantoolCmd(base *binary.BinaryContext,
	uninstallCtx *binary.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String() + " [version]",
		Short: "Uninstall Tarantool Community Edition",
		RunE:  binaryRunE(base, uninstallCtx, internalUninstallModule),
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

// newUninstallTarantoolEeCmd creates a command to uninstall tarantool-ee.
func newUninstallTarantoolEeCmd(base *binary.BinaryContext,
	uninstallCtx *binary.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramEe.String() + " [version]",
		Short: "Uninstall Tarantool Enterprise Edition",
		RunE:  binaryRunE(base, uninstallCtx, internalUninstallModule),
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
func newUninstallTarantoolDevCmd(base *binary.BinaryContext,
	uninstallCtx *binary.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   "tarantool-dev",
		Short: "Uninstall Tarantool from a local build directory",
		RunE:  binaryRunE(base, uninstallCtx, internalUninstallModule),
		Args:  cobra.ExactArgs(0),
	}

	return tntCmd
}

// newUninstallTcmCmd creates a command to uninstall tcm.
func newUninstallTcmCmd(base *binary.BinaryContext,
	uninstallCtx *binary.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String() + " [version]",
		Short: "Uninstall Tarantool Cluster Manager",
		RunE:  binaryRunE(base, uninstallCtx, internalUninstallModule),
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

// internalUninstallModule is a default uninstall module.
func internalUninstallModule(base binary.BinaryContext, uninstallCtx binary.UninstallCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
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

	uninstallCtx.BinaryContext = base
	uninstallCtx.Program = program
	uninstallCtx.Version = programVersion
	uninstallCtx.BinDir = cliOpts.Env.BinDir
	uninstallCtx.IncDir = cliOpts.Env.IncludeDir + "/include"

	return binary.UninstallProgram(uninstallCtx)
}

// newBinarySearchCmd creates search command.
func newBinarySearchCmd(base *binary.BinaryContext,
	searchCtx *binary.SearchCtx,
) *cobra.Command {
	searchCmd := &cobra.Command{
		Use:   "search",
		Short: "Search available program versions",
		Example: `# Search Tarantool Enterprise Edition releases.
  $ tt binary search tarantool-ee

  # Search Tarantool Enterprise Edition 3.0 debug builds.
  $ tt binary search tarantool-ee --debug --version 3.0

  # Search Tarantool Cluster Manager 1.3 releases.
  $ tt binary search tcm --version 1.3`,
	}
	searchCmd.PersistentFlags().BoolVar(&searchCtx.Local, "local-repo", false,
		"Search the local repository")

	searchCmd.AddCommand(
		newSearchTarantoolCmd(base, searchCtx),
		newSearchTarantoolEeCmd(base, searchCtx),
		newSearchTtCmd(base, searchCtx),
		newSearchTcmCmd(base, searchCtx),
	)

	return searchCmd
}

// newSearchTarantoolCmd creates a command to search tarantool.
func newSearchTarantoolCmd(base *binary.BinaryContext,
	searchCtx *binary.SearchCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String(),
		Short: "Search available Tarantool Community Edition versions",
		RunE:  binaryRunE(base, searchCtx, internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}

	return tntCmd
}

// newSearchTarantoolEeCmd creates a command to search tarantool-ee.
func newSearchTarantoolEeCmd(base *binary.BinaryContext,
	searchCtx *binary.SearchCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramEe.String(),
		Short: "Search available Tarantool Enterprise Edition versions",
		RunE:  binaryRunE(base, searchCtx, internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}
	tntCmd.Flags().BoolVar(&searchCtx.Debug, "debug", false,
		"Search debug builds")
	tntCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", "",
		"Filter by release version")
	tntCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"Search development builds")

	return tntCmd
}

// newSearchTtCmd creates a command to search tt.
func newSearchTtCmd(base *binary.BinaryContext,
	searchCtx *binary.SearchCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTt.String(),
		Short: "Search available tt versions",
		RunE:  binaryRunE(base, searchCtx, internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}

	return tntCmd
}

// newSearchTcmCmd creates a command to search tcm.
func newSearchTcmCmd(base *binary.BinaryContext,
	searchCtx *binary.SearchCtx,
) *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String(),
		Short: "Search available Tarantool Cluster Manager versions",
		RunE:  binaryRunE(base, searchCtx, internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}
	tcmCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", "",
		"Filter by release version")
	tcmCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"Search development builds")

	return tcmCmd
}

// internalSearchModule is a default search module.
func internalSearchModule(base binary.BinaryContext, searchCtx binary.SearchCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	var err error

	searchCtx.BinaryContext = base

	searchCtx.Program, err = binary.ParseProgram(cmdCtx.CommandName)
	if err != nil {
		return fmt.Errorf("failed to search: %w", err)
	}

	if searchCtx.Local {
		return binary.SearchVersionsLocal(searchCtx, cliOpts, cmdCtx.Cli.ConfigPath)
	}

	if searchCtx.Debug {
		searchCtx.Filter = binary.SearchDebug
	} else {
		searchCtx.Filter = binary.SearchRelease
	}

	return binary.SearchVersions(searchCtx, cliOpts)
}

// newBinaryListCmd creates a command to list installed binaries.
func newBinaryListCmd(base *binary.BinaryContext,
	listCtx *binary.ListCtx,
) *cobra.Command {
	listCmd := &cobra.Command{
		Use:   listCmdName,
		Short: "List installed binaries and versions",
		Args:  cobra.NoArgs,
		RunE:  binaryRunE(base, listCtx, internalListModule),
	}

	return listCmd
}

// internalListModule is a list module.
func internalListModule(base binary.BinaryContext, listCtx binary.ListCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	listCtx.BinaryContext = base
	listCtx.BinDir = cliOpts.Env.BinDir

	return binary.ListBinaries(listCtx)
}

// newBinarySwitchCmd creates a command to switch installed binaries.
func newBinarySwitchCmd(base *binary.BinaryContext,
	switchCtx *binary.SwitchCtx,
) *cobra.Command {
	switchCmd := &cobra.Command{
		Use:   "switch [program] [version]",
		Short: "Switch the active binary",
		Example: `# Choose a program and version interactively.
  $ tt binary switch

  # Choose a Tarantool version interactively.
  $ tt binary switch tarantool

  # Switch to a specific Tarantool version.
  $ tt binary switch tarantool 3.0.0`,
		RunE: binaryRunE(base, switchCtx, internalSwitchModule),
		Args: cobra.MatchAll(
			cobra.MaximumNArgs(binarySwitchMaxArgs), binarySwitchValidateArgs),
		ValidArgs: binarySupportedPrograms,
	}

	return switchCmd
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
func internalSwitchModule(base binary.BinaryContext, switchCtx binary.SwitchCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var err error

	switchCtx.BinaryContext = base

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

// binaryRunE applies the flags inherited from binary to the whole operation.
func binaryRunE[T any](base *binary.BinaryContext, operationCtx *T, run binaryFunc[T],
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		options := *base
		if !cmd.Flags().Changed("verbose") {
			options.Verbose = cmdCtx.Cli.Verbose
		}

		cmdCtx.CommandName = cmd.Name()
		cmdCtx.Cli.Verbose = options.Verbose

		err := setupLogging()
		if err != nil {
			return commandError(cmd, err)
		}

		err = run(options, *operationCtx, &cmdCtx, args)

		return commandError(cmd, err)
	}
}
