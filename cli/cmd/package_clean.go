package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// packageCleanDryRun is --dry-run of tt package clean.
var packageCleanDryRun bool

// newPackageCleanCmd wires `tt package clean [COMPONENT]`.
func newPackageCleanCmd() *cobra.Command {
	cleanCmd := &cobra.Command{
		Use:   "clean [COMPONENT]",
		Short: "Remove what the builds of the package made",
		Long: "Remove what tt package build made, and nothing else, so that the " +
			"next build builds everything again: _build/ - the build trees of the " +
			"components (TT_BUILD_DIR), the records of the incremental builds, the " +
			"stages, the builds of path dependencies and what tt package pack wrote " +
			"into _build/pack/ - and the rock of the package in .rocks/, by the " +
			"files its rock manifest lists. The rocks of the dependencies, the " +
			"guest packages, the manifest, the lock and the sources stay; so does " +
			"_build/tt.lock, the lock of the project.\n\n" +
			"A build backend may clean what its tool leaves beside the sources, " +
			"which tt does not remove itself: it runs first, for every component " +
			"of such a backend. A clean of the whole project then runs the clean " +
			"hook, [hooks.clean] of the manifest, when it declares one.\n\n" +
			"A component name narrows the clean to the component: its build tree, " +
			"its records and its files in the stages go, and the rock of the " +
			"package leaves .rocks/, which the next build installs again with every " +
			"component; the clean hook does not run.\n\n" +
			"--dry-run removes nothing and runs nothing, and prints what a clean " +
			"would remove and run; -V lists the files of the rock as well.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageClean(args))
		},
	}

	cleanCmd.Flags().BoolVar(&packageCleanDryRun, "dry-run", false,
		"print what a clean would remove and run, and do nothing")

	return cleanCmd
}

// runPackageClean cleans the project, or the component in args, and says
// what it removed and ran, one line each.
func runPackageClean(args []string) error {
	ctx, stop := packageContext()
	defer stop()

	projectDir, err := packageRootDir()
	if err != nil {
		return err
	}

	clean := build.CleanOptions{Components: args, DryRun: packageCleanDryRun}

	result, err := cleanProjectIn(ctx, projectDir, clean)
	if err != nil {
		return err
	}

	printClean(projectDir, result, packageCleanDryRun, cmdCtx.Cli.Verbose || packageCleanDryRun)

	return nil
}

// cleanProjectIn cleans the project in projectDir (build.Clean), with the
// options of the builds of tt package build: the Contexts of the Clean of
// the backends and of the clean hook are those of a build.
func cleanProjectIn(ctx context.Context, projectDir string, clean build.CleanOptions) (
	*build.CleanResult, error,
) {
	//nolint:contextcheck // tarantool --version is asked once, and takes no context.
	tntInfo, err := buildTarantoolInfo()
	if err != nil {
		return nil, err
	}

	return build.Clean(ctx, build.Options{
		ProjectDir: projectDir,
		Product:    "",
		Component:  "",
		Components: nil,
		Args:       nil,
		Locked:     false,
		FetchOnly:  false,
		TtVersion:  lockStamp(),
		Tarantool:  tntInfo,
		Registries: manifestrocks.Sources{
			Flag: nil, Env: nil, Manifest: nil, WorkingDir: "", ProjectDir: "",
		},
		ShowOutput: cmdCtx.Cli.Verbose,
		Rebuild:    false,
		Jobs:       0,
		Now:        time.Time{},
		Logger:     luarocksLogger(),
		Warn:       func(msg string) { log.Warn(msg) },
		Removed:    nil,
		Check:      nil,
		Backends:   buildreg.Process(),
	}, clean)
}

// printClean prints what result says a clean of the project in projectDir
// removed and ran, or would have with dryRun, the paths relative to the
// project; with files, each file of a rock removed too.
func printClean(projectDir string, result *build.CleanResult, dryRun, files bool) {
	ran, removed := "ran", "removed"
	if dryRun {
		ran, removed = "would run", "would remove"
	}

	rel := func(path string) string {
		out, err := filepath.Rel(projectDir, path)
		if err != nil {
			return path
		}

		return out
	}

	for _, name := range result.Cleaned {
		_, _ = fmt.Fprintf(os.Stdout, "%s the clean of component %s\n", ran, name)
	}

	if result.Hook {
		_, _ = fmt.Fprintf(os.Stdout, "%s the clean hook\n", ran)
	}

	for _, path := range result.Removed {
		_, _ = fmt.Fprintf(os.Stdout, "%s %s\n", removed, rel(path))
	}

	for _, rock := range result.Rocks {
		_, _ = fmt.Fprintf(os.Stdout, "%s %s %s from .rocks (%d files)\n", removed, rock.Name,
			rock.Version, len(rock.Files))

		if files {
			for _, file := range rock.Files {
				_, _ = fmt.Fprintf(os.Stdout, "  %s\n", rel(file))
			}
		}
	}

	if len(result.Cleaned) == 0 && !result.Hook && len(result.Removed) == 0 &&
		len(result.Rocks) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "nothing to clean")
	}
}
