package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/distbuild"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/buildflags"
)

// versionLabelEnv names the label that follows the version of a build, as
// it does for the magefile's builds of tt.
const versionLabelEnv = "VERSION_LABEL"

// buildTtCtx holds the flags of tt binary build tt.
type buildTtCtx struct {
	// manifest is the path to tt.manifest.toml.
	manifest string
	// output is where the binary is written.
	output string
	// work keeps the generated main module there.
	work string
	// locked builds strictly from the lock.
	locked bool
	// update resolves every version again.
	update bool
}

// newBinaryBuildCmd creates the build command.
func newBinaryBuildCmd(base *binary.BinaryContext) *cobra.Command {
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build a program from its sources",
		Long: `Build a program from its sources.

tt binary build tt builds a distribution of tt: a tt binary made of the tt
core and the compiled-in tt modules tt.manifest.toml lists.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	buildCmd.AddCommand(newBuildTtCmd(base))

	return buildCmd
}

// newBuildTtCmd creates the command that builds a distribution of tt.
func newBuildTtCmd(base *binary.BinaryContext) *cobra.Command {
	var buildCtx buildTtCtx

	ttCmd := &cobra.Command{
		Use:   binary.ProgramTt.String(),
		Short: "Build a distribution of tt from tt.manifest.toml",
		Long: `Build a distribution of tt from tt.manifest.toml: the tt core and the tt
modules - Go modules that export a constructor of tt commands - compiled into
one binary. The Go toolchain must be in PATH.

The smallest manifest builds tt itself on the core of the tt that runs the
build; every other field is optional:

  manifest_version = "0.1"
  manifest_kind = "tt-distribution"

  [package]
  name = "tt"                   # the binary

  [core]
  tt = "v3"                     # latest, a branch, a commit or a version
  # path = "../tt"              # or a local checkout of the core

  [distribution]
  title = "Tarantool CLI EE"    # how the binary presents itself
  edition = "ee"
  integrity = "rsa"             # none (default) or rsa
  builtin = true                # tt's own commands
  external_modules = true       # TT_CLI_MODULES_PATH

  [build]
  ssl = "default"               # default, openssl or openssl_static
  tags = []
  ldflags = []

  [dependencies]
  aeon = "github.com/tarantool/tt/modules/aeon"   # follows the core
  foo = "github.com/acme/tt-foo@v1.2.0"            # a module and a version
  migrations = "./plugins/migrations"              # a local module

A module of the core's repository is built at the commit of the core; any
other module without a version at its latest. A module's constructor is New
in its root package unless package and symbol say otherwise.

The build writes tt.manifest.lock and tt.manifest.sum next to the manifest:
the exact versions and the go.sum it was built from. While the manifest does
not change, the next build takes the versions from the lock; --update
resolves them again. --locked builds strictly from the lock and fails when it
or the Go modules differ.

TT_CLI_BUILD_SSL, when set, overrides [build].ssl. VERSION_LABEL follows the
version of the distribution, which is taken from git describe in the
manifest's directory. The path to the binary is printed to stdout.`,
		Example: `# Build the distribution in the current directory into ./<package.name>.
  $ tt binary build tt

  # Take the newest core and modules the manifest allows.
  $ tt binary build tt --update

  # Build exactly what the lock says, as CI does.
  $ tt binary build tt --locked -o dist/tt`,
		Args: cobra.NoArgs,
		RunE: binaryRunE(base, &buildCtx, internalBuildTtModule),
	}

	flags := ttCmd.Flags()
	flags.StringVar(&buildCtx.manifest, "manifest", "",
		"path to the manifest (default: "+manifest.DistributionFile+" in the project directory)")
	flags.StringVarP(&buildCtx.output, "output", "o", "",
		"path to the binary (default: the package name in the manifest's directory)")
	flags.StringVar(&buildCtx.work, "work", "",
		"generate the main module in DIR and keep it there")
	flags.BoolVar(&buildCtx.locked, "locked", false,
		"build strictly from "+manifest.DistributionLockFile+" and write nothing but the binary")
	flags.BoolVar(&buildCtx.update, "update", false,
		"resolve the core and the modules again, ignoring the lock")
	ttCmd.MarkFlagsMutuallyExclusive("locked", "update")
	addProjectDirFlag(ttCmd)

	return ttCmd
}

// internalBuildTtModule builds the distribution of tt the manifest
// describes and prints the path to the binary.
func internalBuildTtModule(base binary.BinaryContext, buildCtx buildTtCtx,
	_ *cmdcontext.CmdCtx, _ []string,
) error {
	dir, err := absoluteWorkingDir()
	if err != nil {
		return err
	}

	manifestPath := buildCtx.manifest
	if manifestPath == "" {
		manifestPath = manifest.DistributionFile
	}

	runningCore, _ := sdk.CoreVersion()

	result, err := distbuild.Run(context.Background(), distbuild.Options{
		Manifest:    inDir(dir, manifestPath),
		Output:      inDir(dir, buildCtx.output),
		Locked:      buildCtx.locked,
		Update:      buildCtx.update,
		WorkDir:     inDir(dir, buildCtx.work),
		SSL:         os.Getenv(buildflags.Env),
		Label:       os.Getenv(versionLabelEnv),
		GeneratedBy: "tt " + coreVersion(),
		RunningCore: runningCore,
		ShowOutput:  base.Verbose,
		Environ:     nil,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(os.Stdout, result.Output)
	if err != nil {
		return fmt.Errorf("writing the result: %w", err)
	}

	return nil
}

// inDir returns path, when relative, as a path in dir; the empty path stays
// empty.
func inDir(dir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(dir, path)
}
