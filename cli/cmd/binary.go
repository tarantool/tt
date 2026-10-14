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

const (
	binarySwitchMaxArgs = 2
	sdkLowercaseAlias   = "sdk"
	sdkUppercaseAlias   = "SDK"
)

var (
	errNotSupportedProgram        = errors.New("not supported program: ")
	errFromBuildDirectoryRequired = errors.New("--from-build requires a non-empty directory")
	errIncludeDirWithoutFromBuild = errors.New("--include-dir requires --from-build")
)

var binarySupportedPrograms = []string{
	binary.ProgramCe.String(),
	binary.ProgramEe.String(),
	binary.ProgramTt.String(),
	binary.ProgramTtEe.String(),
	binary.ProgramTcm.String(),
	binary.ProgramSDK.String(),
}

// binaryFunc implements a binary command with common options.
type binaryFunc[T any] func(binary.BinaryContext, T, *cmdcontext.CmdCtx, []string) error

// NewBinaryCmd creates the command for managing binaries and SDK installations.
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
		Long: `Manage tt and tt Enterprise, Tarantool Community and Enterprise, TCM,
and SDK bundles.

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
<root>/versions/<product>/<product>_<version>/, with Tarantool headers in include/tarantool.
Active executables are linked from <root>/bin/, and active Tarantool headers
from <root>/include/tarantool. CE and EE keep separate versions and share the active
Tarantool links.
tt and tt-ee keep separate versions and share the active tt executable.

For project installations, run "source .env/activate.sh" to activate the installed binaries.

SDK installations select Tarantool Enterprise, tt Enterprise, TCM, and headers together.`,
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
		Long: `Build tt or Tarantool from source, or install Enterprise and TCM binaries.

The installed version becomes active in the selected scope. See
"tt binary --help" for scope locations and the binary layout.

Project installations create .env/activate.sh and .env/deactivate.sh once.
Source them to use the project binaries and headers or restore your shell environment.
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
		newInstallEnterpriseCmd(base, installCtx, binary.ProgramEe),
		newInstallStandaloneCmd(base, installCtx, binary.ProgramTtEe),
		newInstallStandaloneCmd(base, installCtx, binary.ProgramTcm),
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

// newInstallEnterpriseCmd installs one product from an Enterprise SDK archive.
func newInstallEnterpriseCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx, program binary.Program,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   program.String() + " [version]",
		Short: "Install " + program.String() + " from an Enterprise SDK archive",
		Long: fmt.Sprintf(`Install %s from a Tarantool Enterprise SDK archive.

Omitting the version selects the latest stable gc64 build. Specify X, X.Y,
or X.Y.Z to select the latest matching stable gc64 build, or a complete version
name from "tt binary search" to select an exact archive, including nogc64 or RC.

Use "tt binary search %s" to list available versions.

With --local-dir <dir>, the directory must contain Tarantool Enterprise SDK
archives named tarantool-enterprise-sdk-*.tar.gz. Archives are read directly
from that directory.`, program, program),
		Example: `# Download and install the latest stable gc64 release in major version 3.
  $ tt binary install ` + program.String() + ` 3 --scope user

  # Install an Enterprise archive from a local directory for this project.
  $ tt binary install ` + program.String() + ` gc64-3.4.0-0-r60 --local-dir ./bundles`,
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: cobra.MaximumNArgs(1),
	}

	if program == binary.ProgramEe {
		tntCmd.Long += `

Standalone Tarantool Enterprise versions omit SDK revisions: 3.8.1 for 3.x and
newer, or gc64-2.11.1 / nogc64-2.11.1 for older releases. Prerelease and debug
variants remain separate (for example, 3.8.1-rc1 or debug-3.8.1). These names
also select the latest matching archive revision. An installed version is
preserved across SDK revisions unless --reinstall is specified.`
	}

	addEnterpriseInstallFlags(tntCmd, installCtx)

	return tntCmd
}

// addEnterpriseInstallFlags registers the options shared by SDK archive products.
func addEnterpriseInstallFlags(cmd *cobra.Command, installCtx *install.InstallCtx) {
	cmd.Flags().StringVar(&installCtx.LocalRepo, "local-dir", "",
		"Directory with Tarantool Enterprise SDK archives (tarantool-enterprise-sdk-*.tar.gz)")
	cmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "Install a development build")
	cmd.Flags().StringVar(&installCtx.CredentialsFile, "credentials-file", "",
		"File containing customer-zone login and password on separate lines")
}

// newInstallStandaloneCmd shares the root-executable archive UX for tt-ee and TCM.
func newInstallStandaloneCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx, program binary.Program,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   program.String() + " [version]",
		Short: "Install " + program.String() + " from an archive",
		Long: fmt.Sprintf(`Install %s from its standalone archive.

Omitting the version selects the latest stable build. Specify X, X.Y, or X.Y.Z
to select the latest matching stable build, or a complete version name to select
an exact build, including RC.
Partial names with prerelease or commit suffixes are not supported.
Use "tt binary search %s" to list available versions.

With --local-dir <dir>, the directory must contain %s-*.tar.gz archives.
The archive contains the %s executable directly at its root.`,
			program, program, program, program.Exec()),
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: cobra.MaximumNArgs(1),
	}

	addStandaloneInstallFlags(tntCmd, installCtx, program)

	if program == binary.ProgramTtEe {
		tntCmd.Long += "\n\ntt-ee search and installation are available only on Linux."
	}

	return tntCmd
}

func addStandaloneInstallFlags(cmd *cobra.Command, installCtx *install.InstallCtx,
	program binary.Program,
) {
	cmd.Flags().StringVar(&installCtx.LocalRepo, "local-dir", "",
		"Directory containing "+program.String()+" standalone archives")
	cmd.Flags().StringVar(&installCtx.CredentialsFile, "credentials-file", "",
		"File containing customer-zone login and password on separate lines")

	if program == binary.ProgramTcm {
		cmd.Flags().BoolVar(&installCtx.DevBuild, "dev", false, "Install a development build")
	}
}

// newInstallTarantoolSDKCmd uses the same archive source and version semantics as EE.
func newInstallTarantoolSDKCmd(base *binary.BinaryContext,
	installCtx *install.InstallCtx,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:     binary.ProgramSDK.String() + " [version]",
		Aliases: []string{sdkLowercaseAlias, sdkUppercaseAlias},
		Short:   "Install Tarantool Enterprise SDK",
		Long: `Install Tarantool Enterprise, tt Enterprise, and TCM together with headers.

Omitting the version selects the latest stable gc64 build. Specify X, X.Y,
or X.Y.Z to select the latest matching stable gc64 build, or a complete version
name to select an exact build, including nogc64 or RC.
Use --local-dir <dir> to install an SDK archive from a local directory.

Components have separate sdk-<bundle-version> identities. Use "tt binary switch sdk"
to select the entire SDK, or switch individual products independently.
For project installations, run "source .env/activate.sh" to activate the installed binaries.`,
		Example: `# Install the latest stable SDK for this project.
  $ tt binary install tarantool-sdk

  # Install SDK 3.7.1 from a local directory in the user scope.
  $ tt binary install sdk 3.7.1 --local-dir ./bundles --scope user`,
		RunE: binaryRunE(base, installCtx, internalInstallModule),
		Args: cobra.MaximumNArgs(1),
	}
	addEnterpriseInstallFlags(cmd, installCtx)

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
		newUninstallBundleCmd(base, uninstallCtx, binary.ProgramEe),
		newUninstallBundleCmd(base, uninstallCtx, binary.ProgramTtEe),
		newUninstallBundleCmd(base, uninstallCtx, binary.ProgramTcm),
		newUninstallTarantoolSDKCmd(base, uninstallCtx),
	)

	return uninstallCmd
}

// newUninstallTarantoolSDKCmd removes the recorded components of one SDK version.
func newUninstallTarantoolSDKCmd(base *binary.BinaryContext,
	uninstallCtx *uninstall.UninstallCtx,
) *cobra.Command {
	return &cobra.Command{
		Use:     binary.ProgramSDK.String() + " [version]",
		Aliases: []string{sdkLowercaseAlias, sdkUppercaseAlias},
		Short:   "Uninstall an SDK and its components",
		Long: `Remove all components of the selected SDK version in the selected scope.
X.Y.Z must match exactly one installed SDK; a full version selects an exact build.
Without a version, uninstall the only installed SDK.`,
		RunE:              binaryRunE(base, uninstallCtx, internalUninstallModule),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeBinaryUninstall,
	}
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

// newUninstallBundleCmd removes an individual product installed from an archive.
func newUninstallBundleCmd(base *binary.BinaryContext,
	uninstallCtx *uninstall.UninstallCtx, program binary.Program,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   program.String() + " [version]",
		Short: "Uninstall " + program.String(),
		Long: fmt.Sprintf(`Uninstall a %s version in the selected scope.

Specify X.Y.Z to select a single matching installed version, or use the full
installed version name.
If multiple builds match, the command reports their full names and requires
an unambiguous choice before removing anything.
Without a version, remove the only installed version.`, program),
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
<dir>/tt or <dir>/tarantool. For Enterprise products and TCM, use --local-dir <dir>
with SDK, tt-ee, or TCM .tar.gz archives directly in that directory.`,
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
		newSearchEnterpriseCmd(base, searchCtx, binary.ProgramEe),
		newSearchStandaloneCmd(base, searchCtx, binary.ProgramTtEe),
		newSearchEnterpriseCmd(base, searchCtx, binary.ProgramSDK),
		newSearchTtCmd(base, searchCtx),
		newSearchStandaloneCmd(base, searchCtx, binary.ProgramTcm),
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

// newSearchEnterpriseCmd shares the archive catalog while keeping product
// installation labels independent.
func newSearchEnterpriseCmd(base *binary.BinaryContext,
	searchCtx *search.SearchCtx, program binary.Program,
) *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   program.String(),
		Short: "Search available " + program.String() + " versions",
		Long: fmt.Sprintf(`Search available %s versions from SDK archives.
Installed and active labels refer to the requested product in the selected scope.

With --local-dir <dir>, the directory must contain Tarantool Enterprise SDK
archives named tarantool-enterprise-sdk-*.tar.gz. Archives are read directly
from that directory.`, program),
		RunE: binaryRunE(base, searchCtx, internalSearchModule),
		Args: cobra.ExactArgs(0),
	}
	if program == binary.ProgramSDK {
		tntCmd.Aliases = []string{sdkLowercaseAlias, sdkUppercaseAlias}
		tntCmd.Short = "Search available Tarantool Enterprise SDK versions"
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

// newSearchStandaloneCmd searches independent tt-ee and TCM archive catalogs.
func newSearchStandaloneCmd(base *binary.BinaryContext,
	searchCtx *search.SearchCtx, program binary.Program,
) *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   program.String(),
		Short: "Search available " + program.String() + " versions",
		Long: fmt.Sprintf(`Search available %s versions from standalone archives.

With --local-dir <dir>, the directory must contain %s-*.tar.gz archives.
Archives are read directly from that directory.`, program, program),
		RunE: binaryRunE(base, searchCtx, internalSearchModule),
		Args: cobra.ExactArgs(0),
	}
	tcmCmd.Flags().StringVar(&searchCtx.LocalRepo, "local-dir", "",
		"Directory containing "+program.String()+" standalone archives")
	tcmCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", "",
		"Filter by release version")
	tcmCmd.Flags().StringVar(&searchCtx.CredentialsFile, "credentials-file", "",
		"File containing customer-zone login and password on separate lines")

	if program == binary.ProgramTtEe {
		tcmCmd.Long += "\n\ntt-ee search and installation are available only on Linux."
	} else {
		tcmCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
			"Search development builds")
	}

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
and headers. tt and tt-ee share the active tt executable.

Switching tarantool-sdk (also sdk and SDK) selects all of its components and
headers together. Use sdk-<bundle-version> to select an individual SDK component.

For tarantool-ee, tt-ee, and tcm, X.Y.Z must match a single installed version in the
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
		program, err := binary.ParseProgram(args[0])
		if err != nil || !slices.Contains(binarySupportedPrograms, program.String()) {
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
