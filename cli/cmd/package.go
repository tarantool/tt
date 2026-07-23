package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/manifest/build"
	"github.com/tarantool/tt/cli/manifest/install"
	"github.com/tarantool/tt/cli/manifest/pack"
	manifestrocks "github.com/tarantool/tt/cli/manifest/rocks"
	oldrocks "github.com/tarantool/tt/cli/rocks"
	"github.com/tarantool/tt/cli/util"
	ttversion "github.com/tarantool/tt/cli/version"
)

// errNoTarantool reports that no tarantool executable was found; the manifest
// build needs it to configure the rocks adapter (and to compile c/lua-c
// components).
var errNoTarantool = errors.New("tarantool executable not found in PATH")

// Shared flags for the package subcommands.
var (
	packageProduct     string
	packageLocked      bool
	packageWithoutDeps bool
)

// Flags specific to `tt package install`.
var (
	packageScope   string
	packageUpgrade bool
	packageForce   bool
	packageYes     bool
)

// NewPackageCmd creates the `tt package` command group: the manifest build
// pipeline (build, fetch and pack; install is not implemented yet).
func NewPackageCmd() *cobra.Command {
	packageCmd := &cobra.Command{
		Use:   "package",
		Short: "Manage tt manifest packages",
		Long: "Build, fetch and pack Tarantool packages described by " +
			"app.manifest.toml.",
	}

	packageCmd.AddCommand(
		newPackageBuildCmd(),
		newPackageFetchCmd(),
		newPackagePackCmd(),
		newPackageInstallCmd(),
	)

	return packageCmd
}

// newPackageInstallCmd wires `tt package install ARCHIVE...`.
func newPackageInstallCmd() *cobra.Command {
	installCmd := &cobra.Command{
		Use:   "install ARCHIVE...",
		Short: "Install a .tt package archive into a scope",
		Long: "Unpack a package archive into the chosen scope and bring the tree " +
			"to a runnable state. A with-deps archive in the project scope is a " +
			"plain offline extraction; otherwise the package is unpacked and its " +
			"dependencies are refetched from the registry using the lock's pins. " +
			"Several packages installed into one project share a .rocks/ tree, so a " +
			"dependency they lock at different versions is reconciled to one both " +
			"accept, or the install fails with an explanation.",
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := runPackageInstall(args); err != nil {
				log.Error(err.Error())
				os.Exit(install.ExitCode(err))
			}
		},
	}

	installCmd.Flags().StringVar(&packageScope, "scope", "project",
		"install scope: project, user or system")
	installCmd.Flags().BoolVar(&packageLocked, "locked", false,
		"fail if the archive lock does not match its manifest")
	installCmd.Flags().BoolVar(&packageUpgrade, "upgrade", false,
		"install over an existing package only when the archive version is higher")
	installCmd.Flags().BoolVar(&packageForce, "force", false,
		"reinstall over an existing package (destructive)")
	installCmd.Flags().BoolVarP(&packageYes, "yes", "y", false,
		"skip the confirmation prompt when reconciling shared dependencies")

	return installCmd
}

// runPackageInstall assembles install.Options from the environment and installs
// every archive. Tarantool facts are gathered best-effort: an offline with-deps
// install needs none, so a missing tarantool is only fatal when a dependency
// must actually be refetched.
func runPackageInstall(archives []string) error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}

	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		return err
	}

	// Missing tarantool is tolerated here; refetch surfaces its own error only
	// if the install actually reaches the registry.
	tntInfo, _ := tarantoolInfo()

	result, err := install.Run(context.Background(), install.Options{
		ProjectDir: projectDir,
		Scope:      install.Scope(packageScope),
		Archives:   archives,
		Locked:     packageLocked,
		Upgrade:    packageUpgrade,
		Force:      packageForce,
		Yes:        packageYes,
		Tarantool:  tntInfo,
		Warn:       func(msg string) { log.Warn(msg) },
		Confirm: func(prompt string) bool {
			ok, askErr := util.AskConfirm(os.Stdin, prompt)

			return askErr == nil && ok
		},
	})
	if err != nil {
		return err
	}

	for _, one := range result.Installed {
		if one.Skipped {
			continue
		}

		fmt.Printf("installed %s %s into %s scope\n", one.Package, one.Version, one.Scope)
	}

	return nil
}

// newPackageBuildCmd wires `tt package build [COMPONENT]`.
func newPackageBuildCmd() *cobra.Command {
	buildCmd := &cobra.Command{
		Use:   "build [COMPONENT]",
		Short: "Build a manifest package into .rocks/",
		Long: "Resolve dependencies, materialize .rocks/, run component build " +
			"backends and generate version.lua. With no argument every component " +
			"of the product is built; a component name narrows the build to one.",
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runPackageCmd(args, false)
		},
	}

	buildCmd.Flags().StringVar(&packageProduct, "product", "",
		"product to build (default: the product marked default)")
	buildCmd.Flags().BoolVar(&packageLocked, "locked", false,
		"fail if the lock is out of date instead of re-resolving it")

	return buildCmd
}

// newPackageFetchCmd wires `tt package fetch`.
func newPackageFetchCmd() *cobra.Command {
	fetchCmd := &cobra.Command{
		Use:   "fetch",
		Short: "Materialize .rocks/ from the lock without building",
		Long: "Fetch and build the pinned dependency closure into .rocks/ " +
			"strictly from the lock, without re-resolving or running component " +
			"build backends.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runPackageCmd(nil, true)
		},
	}

	fetchCmd.Flags().StringVar(&packageProduct, "product", "",
		"product to fetch (default: the product marked default)")

	return fetchCmd
}

// newPackagePackCmd wires `tt package pack`.
func newPackagePackCmd() *cobra.Command {
	packCmd := &cobra.Command{
		Use:   "pack",
		Short: "Build a manifest package and pack it into a .tt archive",
		Long: "Run the full build and assemble its result into a reproducible " +
			".tt archive. By default the archive is self-contained: it carries " +
			"_runtime/ (Tarantool, tt and TCM when [platform].tcm is set) and the " +
			"whole dependency closure in .rocks/, so installing it needs no " +
			"network. --without-deps drops both. A separate tt package build " +
			"beforehand is not needed. The archive path is printed to stdout.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runPackagePack(); err != nil {
				log.Error(err.Error())
				os.Exit(pack.ExitCode(err))
			}
		},
	}

	packCmd.Flags().StringVar(&packageProduct, "product", "",
		"product to pack (default: the product marked default)")
	packCmd.Flags().BoolVar(&packageLocked, "locked", false,
		"fail if the lock is out of date instead of re-resolving it")
	packCmd.Flags().BoolVar(&packageWithoutDeps, "without-deps", false,
		"pack without _runtime/ and without foreign dependencies in .rocks/")

	return packCmd
}

// runPackagePack assembles pack.Options from the environment, packs, and prints
// the resulting archive path to stdout so it can be piped.
func runPackagePack() error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}

	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		return err
	}

	tntInfo, err := tarantoolInfo()
	if err != nil {
		return err
	}

	ctx := context.Background()

	result, err := pack.Run(ctx, pack.Options{
		ProjectDir:  projectDir,
		Product:     packageProduct,
		Locked:      packageLocked,
		WithoutDeps: packageWithoutDeps,
		Build: build.Options{
			TtVersion:  "tt " + ttversion.GetVersion(true, false),
			Tarantool:  tntInfo,
			ShowOutput: cmdCtx.Cli.Verbose,
		},
		Runtime: runtimeRequest(ctx, tntInfo),
		Warn:    func(msg string) { log.Warn(msg) },
	})
	if err != nil {
		return err
	}

	fmt.Println(result.Path)

	return nil
}

// runtimeRequest describes where pack may take the _runtime/ components from:
// the runtime cache first, the active tt environment as a fallback.
//
// TcmCli carries no version and nothing probes the binary, so the active TCM is
// deliberately not offered as a fallback: passing the path without a version
// would make pack report "the active tcm does not satisfy it" having never
// looked at its version. With [platform].tcm set, the cache is the only source.
func runtimeRequest(ctx context.Context, tntInfo manifestrocks.TarantoolInfo) pack.RuntimeOptions {
	selfPath, err := os.Executable()
	if err != nil {
		// Without a path to ourselves the tt component can still come from the
		// cache; pack reports a proper error if neither source has a match.
		selfPath = ""
	}

	return pack.RuntimeOptions{
		CacheDir:               runtimeCacheDir(),
		ActiveTarantool:        tntInfo.Executable,
		ActiveTarantoolVersion: tntInfo.Version,
		ActiveTarantoolFlavor:  pack.DetectTarantoolFlavor(ctx, tntInfo.Executable),
		ActiveTt:               selfPath,
		// The short form is the bare "x.y.z"; the long form is a human-readable
		// sentence that no version constraint can match.
		ActiveTtVersion: ttversion.GetVersion(true, false),
		// tt publishes no CE/EE marker through its version, so the flavor stays
		// undetermined and only satisfies the [ce] default.
		ActiveTtFlavor: "",
	}
}

// runtimeCacheDir returns the runtime cache root, <user cache>/tt/runtimes.
// The exact path is not yet finalized and nothing populates it automatically
// yet; until then this is the directory a user populates by hand.
func runtimeCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(base, "tt", "runtimes")
}

// runPackageCmd runs a package build/fetch and maps failures to exit codes:
// 1 for a state error (stale --locked, version.lua collision), 2 for a
// component build backend failure.
func runPackageCmd(args []string, fetchOnly bool) {
	if err := runPackage(args, fetchOnly); err != nil {
		log.Error(err.Error())
		os.Exit(build.ExitCode(err))
	}
}

// runPackage assembles build.Options from the environment and drives the build.
func runPackage(args []string, fetchOnly bool) error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}

	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		return err
	}

	tntInfo, err := tarantoolInfo()
	if err != nil {
		return err
	}

	component := ""
	if len(args) == 1 {
		component = args[0]
	}

	return build.Run(context.Background(), build.Options{
		ProjectDir: projectDir,
		Product:    packageProduct,
		Component:  component,
		Locked:     packageLocked,
		FetchOnly:  fetchOnly,
		TtVersion:  "tt " + ttversion.GetVersion(true, false),
		Tarantool:  tntInfo,
		ShowOutput: cmdCtx.Cli.Verbose,
		Warn:       func(msg string) { log.Warn(msg) },
	})
}

// tarantoolInfo gathers the Tarantool facts the rocks adapter needs from the
// configured CLI context: the executable path, its version and its install
// prefix.
func tarantoolInfo() (manifestrocks.TarantoolInfo, error) {
	if cmdCtx.Cli.TarantoolCli.Executable == "" {
		return manifestrocks.TarantoolInfo{}, errNoTarantool
	}

	ver, err := cmdCtx.Cli.TarantoolCli.GetVersion()
	if err != nil {
		return manifestrocks.TarantoolInfo{}, err
	}

	prefix, err := oldrocks.GetTarantoolPrefix(&cmdCtx.Cli, cliOpts)
	if err != nil {
		return manifestrocks.TarantoolInfo{}, err
	}

	return manifestrocks.TarantoolInfo{
		Executable: cmdCtx.Cli.TarantoolCli.Executable,
		Prefix:     prefix,
		Version:    ver.Str,
	}, nil
}
