package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/manifest/build"
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
	packageProduct string
	packageLocked  bool
)

// NewPackageCmd creates the `tt package` command group: the manifest build
// pipeline (build and fetch; pack and install are not implemented yet).
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
