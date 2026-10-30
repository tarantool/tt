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

const (
	// versionLabelEnv names the label that follows the version of a build,
	// as it does for the magefile's builds of tt.
	versionLabelEnv = "VERSION_LABEL"
	// distCoreEnv overrides the core of a distribution, as --core does.
	distCoreEnv = "TT_DIST_CORE"
)

// buildTtCtx holds the flags of tt binary build tt.
type buildTtCtx struct {
	// manifest is the path to tt.manifest.toml.
	manifest string
	// output is where the binary is written.
	output string
	// work keeps the generated main module there.
	work string
	// vendor makes an offline bundle there instead of a binary.
	vendor string
	// core overrides [core] of the manifest.
	core string
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

  [replace]                     # Go modules the build takes from elsewhere
  # a local directory
  "example.com/lib" = "./libs/lib"
  # a module version, or a query
  "github.com/acme/lib/v2" = "github.com/acme/lib/v2@main"

A module of the core's repository is built at the commit of the core; any
other module without a version at its latest. A module's constructor is New
in its root package unless package and symbol say otherwise.

[replace] does what replace directives of the generated go.mod do, which a tt
module cannot do itself: Go applies the replace directives of the main module
only, so a module that needs an unpublished library, or another version of
one, cannot carry the replacement in its own go.mod. A key is a Go module path,
optionally with @version to replace that version only; a value is a directory
(it starts with ./, ../ or /; relative to the manifest, and it must hold that
module) or <module>@<version>, where the version may be a query - a branch, a
commit - that the build resolves to an exact version and the lock pins. The
core, the modules of its repository and the tt modules of [dependencies] are
not replaced here: use [core].path or --core for the core, and path or version
of the dependency for a tt module.

The build writes tt.manifest.lock and tt.manifest.sum next to the manifest:
the exact versions and the go.sum it was built from, and a hash of the files
of every local module, local core and replaced directory. While the manifest
does not change, the next build takes the versions from the lock; --update
resolves them again. --locked builds strictly from the lock and fails when it,
the Go modules or the files of a local module or a replaced directory differ.

--vendor DIR makes an offline bundle instead of the binary: the generated main
module with every module it is built from in vendor/, the local modules and the
replaced directories copied into local/, the build parameters of each TLS
implementation in build.env and build.sh, which builds the binary with nothing
but the Go toolchain: no network, no module cache, no tt. The bundle names no
directory of the machine that made it and is built once offline to check it.

--core VALUE, or TT_DIST_CORE, builds on another core than [core] says, without
editing the manifest: a local checkout (an existing directory or a path that
starts with ./, ../ or /) or a version query. Such a build is a development
one: its core is not taken from the lock, the lock is not written, and
--locked refuses it. The modules still come from the lock.

TT_CLI_BUILD_SSL, when set, overrides [build].ssl. VERSION_LABEL follows the
version of the distribution, which is taken from git describe in the
manifest's directory. The path to the binary, or to the bundle, is printed to
stdout.`,
		Example: `# Build the distribution in the current directory into ./<package.name>.
  $ tt binary build tt

  # Take the newest core and modules the manifest allows.
  $ tt binary build tt --update

  # Build exactly what the lock says, as CI does.
  $ tt binary build tt --locked -o dist/tt

  # Try the distribution on the core of a local checkout.
  $ tt binary build tt --core ../tt

  # Make an offline bundle, then build it where there is no network.
  $ tt binary build tt --locked --vendor dist/bundle
  $ sh dist/bundle/build.sh default ./tt`,
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
	flags.StringVar(&buildCtx.vendor, "vendor", "",
		"make a bundle in DIR that go build turns into the binary offline, instead of the binary")
	flags.StringVar(&buildCtx.core, "core", "",
		"build on this core instead of [core]: a local checkout or a version query "+
			"(env "+distCoreEnv+"); the lock is left as it is")
	ttCmd.MarkFlagsMutuallyExclusive("locked", "update")
	ttCmd.MarkFlagsMutuallyExclusive("locked", "core")
	ttCmd.MarkFlagsMutuallyExclusive("vendor", "output")
	ttCmd.MarkFlagsMutuallyExclusive("vendor", "work")
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

	core := buildCtx.core
	if core == "" {
		core = os.Getenv(distCoreEnv)
	}

	result, err := distbuild.Run(context.Background(), distbuild.Options{
		Manifest:    inDir(dir, manifestPath),
		Output:      inDir(dir, buildCtx.output),
		Locked:      buildCtx.locked,
		Update:      buildCtx.update,
		WorkDir:     inDir(dir, buildCtx.work),
		Core:        core,
		CoreDir:     dir,
		Vendor:      inDir(dir, buildCtx.vendor),
		SSL:         os.Getenv(buildflags.Env),
		Label:       os.Getenv(versionLabelEnv),
		GeneratedBy: lockStamp(),
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
