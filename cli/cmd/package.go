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
	"github.com/tarantool/tt/cli/manifest/pack"
	manifestrocks "github.com/tarantool/tt/cli/manifest/rocks"
	oldrocks "github.com/tarantool/tt/cli/rocks"
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
	)

	return packageCmd
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
// RFC 0010 leaves the exact path an open item and `tt runtime install` is a v1
// command; until then this is the directory a user populates by hand.
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
