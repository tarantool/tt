package cmd

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/install"
	"github.com/tarantool/tt/v3/internal/binary/list"
	"github.com/tarantool/tt/v3/internal/binary/search"
	switcher "github.com/tarantool/tt/v3/internal/binary/switch"
	"github.com/tarantool/tt/v3/internal/binary/uninstall"
)

const binarySwitchMaxArgs = 2

var (
	errNotSupportedProgram                              = errors.New("not supported program: ")
	errInvalidNumberOfParameters                        = errors.New("invalid number of parameters")
	errToDownloadTarantoolSDKYouNeedToSpecifyTheVersion = errors.New(
		"to download Tarantool SDK, you need to specify the version",
	)
	errFromBuildDirectoryRequired = errors.New("--from-build requires a non-empty directory")
	errIncludeDirWithoutFromBuild = errors.New("--include-dir requires --from-build")
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
		scopeName    string
		installCtx   install.InstallCtx
		uninstallCtx uninstall.UninstallCtx
		searchCtx    = search.NewSearchCtx(search.NewPlatformInformer(), search.NewTntIoDoer())
		listCtx      list.ListCtx
		switchCtx    switcher.SwitchCtx
	)

	binaryCmd := &cobra.Command{
		Use:   "binary",
		Short: "Manage binaries and SDK bundles",
		Long: `Manage tt, Tarantool Community and Enterprise editions, TCM, and SDK bundles.

Use --scope to choose where binaries are managed:
  project (default): .tarantool/ in the current working directory.
  user:
    Linux: $XDG_DATA_HOME/tarantool when XDG_DATA_HOME is an absolute path;
      otherwise ~/.local/share/tarantool.
    macOS: ~/Library/Application Support/tarantool.
  system:
    Linux: /opt/tarantool.
    macOS: /Library/Application Support/tarantool.

Each scope stores executables directly in
<root>/versions/<product>/<product>_<version>/, with Tarantool headers in inc/tarantool.
Active executables are linked from <root>/bin/, and active Tarantool headers
from <root>/inc/tarantool. CE and EE keep separate versions and share the active
Tarantool links. Add <root>/bin to PATH to run the active versions.

SDK downloads use --destination as their output directory.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var err error

			base.Scope, err = binary.ParseScope(scopeName)
			if err != nil {
				return commandError(cmd, util.NewArgError(err.Error()))
			}

			return nil
		},
	}

	binaryCmd.PersistentFlags().StringVar(&scopeName, "scope", binary.ScopeProject.String(),
		"Scope: project, user or system")
	binaryCmd.PersistentFlags().BoolVarP(&base.Verbose, "verbose", "V", false,
		"Show diagnostic output and build commands")

	binaryCmd.AddCommand(
		newBinaryInstallCmd(&base, &installCtx),
		newBinaryUninstallCmd(&base, &uninstallCtx),
		newBinarySearchCmd(&base, &searchCtx),
		newBinaryListCmd(&base, &listCtx),
		newBinarySwitchCmd(&base, &switchCtx),
	)

	return binaryCmd
}

// newBinaryInstallCmd creates install command.
func newBinaryInstallCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx,
) *cobra.Command {
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install a program",
		Long: `Build tt or Tarantool from source, or install EE and TCM binaries.

The installed version becomes active in the selected scope. See
"tt binary --help" for scope locations and the binary layout.
`,
		Example: `# Build the latest Tarantool release for this project.
  $ tt binary install tarantool

  # Build tt for the current user.
  $ tt binary install tt --scope user

  # Install a local Enterprise archive system-wide.
  $ sudo tt binary install tarantool-ee gc64-3.4.0-0-r60 \
      --scope system --local-dir ./bundles`,
	}
	installCmd.PersistentFlags().BoolVarP(&installCtx.Force, "force", "f", false,
		"Skip dependency checks")
	installCmd.PersistentFlags().BoolVar(&installCtx.KeepTemp, "no-clean", false,
		"Keep temporary files")
	installCmd.PersistentFlags().BoolVar(&installCtx.Reinstall, "reinstall", false,
		"Reinstall an existing version")

	tarantoolCmd := newInstallTarantoolCmd(base, installCtx)
	installCmd.AddCommand(
		newInstallTtCmd(base, installCtx),
		tarantoolCmd,
		newInstallTarantoolEeCmd(base, installCtx),
		newInstallTcmCmd(base, installCtx),
		newInstallTarantoolSDKCmd(base, installCtx),
	)
	tarantoolCmd.MarkFlagsMutuallyExclusive("from-build", "local-repo")

	return installCmd
}

// newInstallTtCmd creates a command to install tt.
func newInstallTtCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTt.String() + " [version|commit|pull-request]",
		Short: "Install tt",
		Long: `Build and install tt from a release, Git commit, master, or pull request.

Omitting the version selects the latest release. Use pr/<number> for a pull
request.

With --local-repo <dir>, <dir>/tt must be a local Git clone of the tt source
repository.`,
		Example: `# Build the latest tt release for the current user.
  $ tt binary install tt --scope user

  # Build tt from a pull request for this project.
  $ tt binary install tt pr/534

  # Build tt from a local source repository.
  $ tt binary install tt master --local-repo ./distfiles`,
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().StringVar(&installCtx.LocalRepo, "local-repo", "",
		"Directory containing the tt Git source repository in tt/")

	return tntCmd
}

// newInstallTarantoolCmd creates a command to install tarantool.
func newInstallTarantoolCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String() + " [version|commit|pull-request]",
		Short: "Install Tarantool Community Edition",
		Long: `Build and install Tarantool Community Edition from a release, Git commit,
master, or pull request.

Omitting the version selects the latest release.
Use pr/<number> for a pull request. With --local-repo <dir>, <dir>/tarantool
must be a local Git clone of the Tarantool source repository.

Use --from-build <dir> to link an existing Community or Enterprise build into
the selected scope without copying or rebuilding it. Use --include-dir to
select the headers directory. --from-build accepts no version argument.`,
		Example: `# Build and install Tarantool from the local repository.
  $ tt binary install tarantool --local-repo ./distfiles

  # Link an existing Tarantool build into the project scope.
  $ tt binary install tarantool \
      --from-build ~/src/tarantool/build

  # Link a build with custom headers into the user scope.
  $ tt binary install tarantool --scope user \
      --from-build ~/src/tarantool/build \
      --include-dir ~/src/tarantool/include

  # Build Tarantool with two parallel jobs.
  $ MAKEFLAGS="-j2" tt binary install tarantool 3.4.0`,
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("from-build") {
				if installCtx.FromBuild == "" {
					return errFromBuildDirectoryRequired
				}

				return cobra.ExactArgs(0)(cmd, args)
			}

			if cmd.Flags().Changed("include-dir") {
				return errIncludeDirWithoutFromBuild
			}

			return cobra.MaximumNArgs(1)(cmd, args)
		},
	}

	tntCmd.Flags().BoolVar(&installCtx.BuildInDocker, "use-docker", false,
		"Build Tarantool in an Ubuntu Docker container")
	tntCmd.Flags().BoolVar(&installCtx.Dynamic, "dynamic", false,
		"Build Tarantool with dynamic linking")
	tntCmd.Flags().StringVar(&installCtx.LocalRepo, "local-repo", "",
		"Directory containing the Tarantool Git source repository in tarantool/")
	tntCmd.Flags().StringVar(&installCtx.FromBuild, "from-build", "",
		"Link Tarantool from an existing build directory")
	tntCmd.Flags().StringVar(&installCtx.IncDir, "include-dir", "",
		"Path to the Tarantool headers directory (requires --from-build)")
	tntCmd.MarkFlagsMutuallyExclusive("from-build", "use-docker")
	tntCmd.MarkFlagsMutuallyExclusive("from-build", "dynamic")

	return tntCmd
}

// newInstallTarantoolEeCmd creates a command to install tarantool-ee.
func newInstallTarantoolEeCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramEe.String() + " [version]",
		Short: "Install Tarantool Enterprise Edition",
		Long: `Install Tarantool Enterprise Edition from an SDK archive.

Omitting the version selects the latest stable gc64 build. Specify X, X.Y,
or X.Y.Z to select the latest matching stable gc64 build, or a complete version
name to select an exact build, including nogc64 or RC. Partial names with build
or prerelease suffixes are not supported.

Use "tt binary search tarantool-ee" to list available versions.

With --local-dir <dir>, the directory must contain Tarantool Enterprise SDK
archives named tarantool-enterprise-sdk-*.tar.gz. Archives are read directly
from that directory.`,
		Example: `# Download and install the latest stable gc64 release in major version 3.
  $ tt binary install tarantool-ee 3 --scope user

  # Install an Enterprise archive from a local directory for this project.
  $ tt binary install tarantool-ee gc64-3.4.0-0-r60 --local-dir ./bundles`,
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().StringVar(&installCtx.LocalRepo, "local-dir", "",
		"Directory with Tarantool Enterprise SDK archives (tarantool-enterprise-sdk-*.tar.gz)")
	tntCmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "Install a development build")
	tntCmd.Flags().StringVar(&installCtx.CredentialsFile, "credentials-file", "",
		"File containing customer-zone login and password on separate lines")

	return tntCmd
}

func newInstallTcmCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String() + " [version]",
		Short: "Install Tarantool Cluster Manager",
		Long: `Install Tarantool Cluster Manager from an archive.

Omitting the version selects the latest stable build. Specify X, X.Y, or X.Y.Z
to select the latest matching stable build, or a complete version name to select
an exact build, including RC.
Partial names with prerelease or commit suffixes are not supported.
Use "tt binary search tcm" to list available versions.

With --local-dir <dir>, the directory must contain TCM archives named
tcm-*.tar.gz. Archives are read directly from that directory.`,
		Example: `# Download and install the latest stable TCM release in minor version 1.3.
  $ tt binary install tcm 1.3 --scope user

  # Install TCM from a local archive for this project.
  $ tt binary install tcm 1.3.1-0-g074b5ffa --local-dir ./bundles`,
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: cobra.MaximumNArgs(1),
	}

	tntCmd.Flags().StringVar(&installCtx.LocalRepo, "local-dir", "",
		"Directory containing TCM archives (tcm-*.tar.gz)")
	tntCmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "Install a development build")
	tntCmd.Flags().StringVar(&installCtx.CredentialsFile, "credentials-file", "",
		"File containing customer-zone login and password on separate lines")

	return tntCmd
}

// newInstallTarantoolSDKCmd creates a command that downloads and saves the Tarantool SDK.
func newInstallTarantoolSDKCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:     binary.ProgramSDK.String() + " <version>",
		Aliases: []string{"sdk", "SDK"},
		Short:   "Download a Tarantool SDK bundle",
		Long: `Download a specific Tarantool SDK archive to --destination (the current
directory by default). The output directory is independent of --scope.
Use --dev to download a development build.`,
		Example: `# Download an SDK bundle to the current directory.
  $ tt binary install tarantool-sdk gc64-3.0.0-0-gf58f7d82a-r23

  # Download a development SDK bundle to /tmp.
  $ tt binary install tarantool-sdk gc64-3.0.0-beta1-2-gcbb569b4c-r612 \
      --dev --destination /tmp`,
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errToDownloadTarantoolSDKYouNeedToSpecifyTheVersion
			} else if len(args) > 1 {
				return errInvalidNumberOfParameters
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "Download a development build")
	cmd.Flags().StringVar(&installCtx.Destination, "destination", "",
		"Directory to save SDK bundles (default: current directory)")
	cmd.Flags().StringVar(&installCtx.CredentialsFile, "credentials-file", "",
		"File containing customer-zone login and password on separate lines")

	return cmd
}

// internalInstallModule is a default install module.
func internalInstallModule(base binary.BinaryContext, installCtx install.InstallCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	var err error

	installCtx.BinaryContext = base

	err = install.FillCtx(cmdCtx, &installCtx, args)
	if err != nil {
		return err
	}

	err = install.Install(installCtx)

	return err
}

// newBinaryUninstallCmd creates uninstall command.
func newBinaryUninstallCmd(base *binary.BinaryContext,
	uninstallCtx *uninstall.UninstallCtx,
) *cobra.Command {
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall a program",
		Long: `Uninstall a program version from the selected scope.

Without a version, remove the only installed version of that product. For
Tarantool, an active local build is unlinked without deleting its source.
Removing an active version selects the latest eligible remaining version
within the same scope. Parent directories and other scopes are not searched.`,
		Example: `# Uninstall a Tarantool version.
  $ tt binary uninstall tarantool 3.0.0 --scope project`,
	}

	uninstallCmd.AddCommand(
		newUninstallTtCmd(base, uninstallCtx),
		newUninstallTarantoolCmd(base, uninstallCtx),
		newUninstallTarantoolEeCmd(base, uninstallCtx),
		newUninstallTcmCmd(base, uninstallCtx),
	)

	return uninstallCmd
}

// newUninstallTtCmd creates a command to uninstall tt.
func newUninstallTtCmd(base *binary.BinaryContext,
	uninstallCtx *uninstall.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:               "tt [version]",
		Short:             "Uninstall tt",
		RunE:              binaryRunE(base, uninstallCtx, internalUninstallModule),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeBinaryUninstall,
	}

	return tntCmd
}

// newUninstallTarantoolCmd creates a command to uninstall tarantool.
func newUninstallTarantoolCmd(base *binary.BinaryContext,
	uninstallCtx *uninstall.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String() + " [version]",
		Short: "Uninstall Tarantool Community Edition",
		Long: `Uninstall a Tarantool version.
Without a version, unlink the active local build or uninstall the only installed version.`,
		RunE:              binaryRunE(base, uninstallCtx, internalUninstallModule),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeBinaryUninstall,
	}

	return tntCmd
}

// newUninstallTarantoolEeCmd creates a command to uninstall tarantool-ee.
func newUninstallTarantoolEeCmd(base *binary.BinaryContext,
	uninstallCtx *uninstall.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramEe.String() + " [version]",
		Short: "Uninstall Tarantool Enterprise Edition",
		Long: `Uninstall a Tarantool Enterprise Edition version in the selected scope.

Specify X.Y.Z to select a single matching installed version, or use the full
installed version name.
If multiple builds match, the command reports their full names and requires
an unambiguous choice before removing anything.
Without a version, remove the only installed version.`,
		RunE:              binaryRunE(base, uninstallCtx, internalUninstallModule),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeBinaryUninstall,
	}

	return tntCmd
}

// newUninstallTcmCmd creates a command to uninstall tcm.
func newUninstallTcmCmd(base *binary.BinaryContext,
	uninstallCtx *uninstall.UninstallCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String() + " [version]",
		Short: "Uninstall Tarantool Cluster Manager",
		Long: `Uninstall a Tarantool Cluster Manager version in the selected scope.
Specify X.Y.Z to select a single matching installed version, or use the full
installed version name. Short versions X and X.Y are not expanded.
If multiple builds match, the command reports their full names and requires
an unambiguous choice before removing anything.
Without a version, remove the only installed version.`,
		RunE:              binaryRunE(base, uninstallCtx, internalUninstallModule),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeBinaryUninstall,
	}

	return tntCmd
}

// completeBinaryUninstall reads scope directly because completion skips the pre-run hook.
func completeBinaryUninstall(cmd *cobra.Command, args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	scopeName, err := cmd.Flags().GetString("scope")
	if err != nil {
		return nil, cobra.ShellCompDirectiveError | cobra.ShellCompDirectiveNoFileComp
	}

	scope, err := binary.ParseScope(scopeName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError | cobra.ShellCompDirectiveNoFileComp
	}

	return uninstall.GetList(scope, cmd.Name()), cobra.ShellCompDirectiveNoFileComp
}

// internalUninstallModule is a default uninstall module.
func internalUninstallModule(base binary.BinaryContext, uninstallCtx uninstall.UninstallCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
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

	return uninstall.UninstallProgram(uninstallCtx)
}

// newBinarySearchCmd creates search command.
func newBinarySearchCmd(base *binary.BinaryContext,
	searchCtx *search.SearchCtx,
) *cobra.Command {
	searchCmd := &cobra.Command{
		Use:   "search",
		Short: "Search available program versions",
		Long: `Search available program versions and mark installations in the selected scope.
Only that scope supplies [installed] and [active] labels.

For tt and Tarantool, use --local-repo <dir> with Git source repositories in
<dir>/tt or <dir>/tarantool. For EE and TCM, use --local-dir <dir> with SDK or
TCM .tar.gz archives directly in that directory.`,
		Example: `# Search Tarantool Enterprise Edition releases.
  $ tt binary search tarantool-ee

  # Search Tarantool Enterprise Edition 3.0 debug builds.
  $ tt binary search tarantool-ee --debug --version 3.0

  # Search Tarantool Cluster Manager 1.3 releases.
  $ tt binary search tcm --version 1.3

  # Search local sources and mark versions installed for the current user.
  $ tt binary search tt --scope user --local-repo ./sources`,
	}

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
	searchCtx *search.SearchCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramCe.String(),
		Short: "Search available Tarantool Community Edition versions",
		Long: `Search available Tarantool Community Edition versions.

With --local-repo <dir>, <dir>/tarantool must be a local Git clone of the
Tarantool source repository. Versions are read from its Git tags.`,
		RunE: binaryRunE(base, searchCtx, internalSearchModule),
		Args: cobra.ExactArgs(0),
	}

	tntCmd.Flags().StringVar(&searchCtx.LocalRepo, "local-repo", "",
		"Directory containing the Tarantool Git source repository in tarantool/")

	return tntCmd
}

// newSearchTarantoolEeCmd creates a command to search tarantool-ee.
func newSearchTarantoolEeCmd(base *binary.BinaryContext,
	searchCtx *search.SearchCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:     binary.ProgramEe.String(),
		Aliases: []string{binary.ProgramSDK.String(), "sdk", "SDK"},
		Short:   "Search available Tarantool Enterprise Edition versions",
		Long: `Search available Tarantool Enterprise Edition versions.

tarantool-sdk, sdk, and SDK are aliases for this search.

With --local-dir <dir>, the directory must contain Tarantool Enterprise SDK
archives named tarantool-enterprise-sdk-*.tar.gz. Archives are read directly
from that directory.`,
		RunE: binaryRunE(base, searchCtx, internalSearchModule),
		Args: cobra.ExactArgs(0),
	}
	tntCmd.Flags().StringVar(&searchCtx.LocalRepo, "local-dir", "",
		"Directory with Tarantool Enterprise SDK archives (tarantool-enterprise-sdk-*.tar.gz)")
	tntCmd.Flags().BoolVar(&searchCtx.Debug, "debug", false,
		"Search debug builds")
	tntCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", "",
		"Filter by release version")
	tntCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"Search development builds")
	tntCmd.Flags().StringVar(&searchCtx.CredentialsFile, "credentials-file", "",
		"File containing customer-zone login and password on separate lines")

	return tntCmd
}

// newSearchTtCmd creates a command to search tt.
func newSearchTtCmd(base *binary.BinaryContext,
	searchCtx *search.SearchCtx,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   binary.ProgramTt.String(),
		Short: "Search available tt versions",
		Long: `Search available tt versions.

With --local-repo <dir>, <dir>/tt must be a local Git clone of the tt source
repository. Versions are read from its Git tags.`,
		RunE: binaryRunE(base, searchCtx, internalSearchModule),
		Args: cobra.ExactArgs(0),
	}

	tntCmd.Flags().StringVar(&searchCtx.LocalRepo, "local-repo", "",
		"Directory containing the tt Git source repository in tt/")

	return tntCmd
}

// newSearchTcmCmd creates a command to search tcm.
func newSearchTcmCmd(base *binary.BinaryContext,
	searchCtx *search.SearchCtx,
) *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   binary.ProgramTcm.String(),
		Short: "Search available Tarantool Cluster Manager versions",
		Long: `Search available Tarantool Cluster Manager versions.

With --local-dir <dir>, the directory must contain TCM archives named
tcm-*.tar.gz. Archives are read directly from that directory.`,
		RunE: binaryRunE(base, searchCtx, internalSearchModule),
		Args: cobra.ExactArgs(0),
	}
	tcmCmd.Flags().StringVar(&searchCtx.LocalRepo, "local-dir", "",
		"Directory containing TCM archives (tcm-*.tar.gz)")
	tcmCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", "",
		"Filter by release version")
	tcmCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"Search development builds")
	tcmCmd.Flags().StringVar(&searchCtx.CredentialsFile, "credentials-file", "",
		"File containing customer-zone login and password on separate lines")

	return tcmCmd
}

// internalSearchModule is a default search module.
func internalSearchModule(base binary.BinaryContext, searchCtx search.SearchCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	var err error

	searchCtx.BinaryContext = base

	searchCtx.Program, err = binary.ParseProgram(cmdCtx.CommandName)
	if err != nil {
		return fmt.Errorf("failed to search: %w", err)
	}

	if searchCtx.LocalRepo != "" {
		return search.SearchVersionsLocal(searchCtx)
	}

	if searchCtx.Debug {
		searchCtx.Filter = search.SearchDebug
	} else {
		searchCtx.Filter = search.SearchRelease
	}

	return search.SearchVersions(searchCtx)
}

// newBinaryListCmd creates a command to list installed binaries.
func newBinaryListCmd(base *binary.BinaryContext,
	listCtx *list.ListCtx,
) *cobra.Command {
	listCmd := &cobra.Command{
		Use:   listCmdName,
		Short: "List installed binaries and versions",
		Long: `List installed versions and active binaries in the selected scope.
The project scope uses only .tarantool in the current directory.`,
		Args: cobra.NoArgs,
		RunE: binaryRunE(base, listCtx, internalListModule),
	}

	return listCmd
}

// internalListModule is a list module.
func internalListModule(base binary.BinaryContext, listCtx list.ListCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	listCtx.BinaryContext = base

	return list.ListBinaries(listCtx)
}

// newBinarySwitchCmd creates a command to switch installed binaries.
func newBinarySwitchCmd(base *binary.BinaryContext,
	switchCtx *switcher.SwitchCtx,
) *cobra.Command {
	switchCmd := &cobra.Command{
		Use:   "switch [program] [version]",
		Short: "Switch the active binary",
		Long: `Switch to an installed version in the selected scope.
Without arguments, choose the program and version interactively. With only
a program, choose its version. CE and EE share the active Tarantool binary
and headers.

For tarantool-ee and tcm, X.Y.Z must match a single installed version in the
selected scope.
If multiple builds match, the command reports their full names and requires
an unambiguous choice before changing the active version.
Use a full installed version name to select a specific build.`,
		Example: `# Choose a program and version interactively.
  $ tt binary switch --scope project

  # Choose a Tarantool version interactively.
  $ tt binary switch tarantool --scope project

  # Switch to a specific Tarantool version.
  $ tt binary switch tarantool 3.0.0 --scope project`,
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
func internalSwitchModule(base binary.BinaryContext, switchCtx switcher.SwitchCtx,
	cmdCtx *cmdcontext.CmdCtx, args []string,
) error {
	var err error

	switchCtx.BinaryContext = base
	switchCtx.ExactVersion = false

	switch len(args) {
	case 0:
		switchCtx.Program, err = switcher.ChooseProgram(binarySupportedPrograms)
	default:
		switchCtx.Program, err = binary.ParseProgram(args[0])
	}

	if err != nil {
		return fmt.Errorf("failed to switch module: %w", err)
	}

	switch len(args) {
	case binarySwitchMaxArgs:
		switchCtx.Version = args[1]
	default:
		switchCtx.Version, err = switcher.ChooseVersion(base.Scope, switchCtx.Program)
		if err != nil {
			return err
		}

		switchCtx.ExactVersion = true
	}

	return switcher.Switch(&switchCtx)
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
