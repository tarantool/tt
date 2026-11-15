package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/distbuild"
	"github.com/tarantool/tt/v3/internal/binary"
)

// workspaceCtx holds the flags of tt binary workspace.
type workspaceCtx struct {
	// manifest is the path to tt.manifest.toml.
	manifest string
	// core overrides [core] of the manifest.
	core string
	// remove deletes the go.work files instead of writing them.
	remove bool
	// force overwrites a go.work tt did not write.
	force bool
	// dryRun prints what would be written or removed.
	dryRun bool
}

// newBinaryWorkspaceCmd creates the command that writes a Go workspace for
// the local modules of a distribution.
func newBinaryWorkspaceCmd(base *binary.BinaryContext) *cobra.Command {
	var workCtx workspaceCtx

	workspaceCmd := &cobra.Command{
		Use:   "workspace",
		Short: "Write a go.work for the local modules on a checkout of the core",
		Long: `Write a Go workspace, go.work, to develop the local tt modules of the
distribution tt.manifest.toml describes together with a local checkout of its
core: go test, go build and gopls in a module then build it on the SDK of
the checkout, whatever version of the SDK its go.mod requires - a commit
that is not pushed yet as well.

The core is [core].path of the manifest, or --core DIR (or TT_DIST_CORE), as
tt binary build tt takes it; a core given by a version is refused, as the
module cache it comes from has no change of yours. The workspace uses every
module of [dependencies] given by a path, and replaces with a directory,
whatever their version:
  - the core and every Go module of its repository its go.mod replaces with
    a directory of its own (sdk/..., modules/...);
  - every Go module under sdk/ of the core, so that a new SDK module is in
    the workspace at once;
  - every module [replace] replaces with a directory; a replacement with a
    module version is left out, with a warning.
The go directive is the newest of the go.mod files of them all.

The go command looks for go.work from the current directory up, so a
go.work is written beside the manifest for the modules in the manifest's
directory, and in the directory of every other module: one file for a
module whose repository has its manifest at the root (path = "."), or a
distribution with its modules under it, and one in each module that lives
beside its distribution (path = "../tt-foo"). Each file uses every local
module of the distribution. The paths in it are relative to it.

A go.work tt writes starts with a comment that says so. tt rewrites such a
file and refuses to overwrite any other unless --force; --remove deletes
the files tt wrote, with their go.work.sum, and refuses, deleting nothing,
when one of them is not tt's. --remove needs no local core: the manifest
may name a version of the core again by then. The paths of the files are printed to
stdout; --dry-run prints them with what would be written, and changes
nothing.

tt binary build tt does not read go.work: it builds with GOWORK=off on the
core of the manifest, and leaves go.work and go.work.sum out of the content
hash of a local module. go mod tidy does not read it either. Keep the files
out of git: they name directories of your machine.`,
		Example: `# Write go.work for the modules of ./tt.manifest.toml on its [core].path.
  $ tt binary workspace

  # The same on another checkout of the core, without editing the manifest.
  $ tt binary workspace --core ../tt

  # Show what would be written.
  $ tt binary workspace --dry-run

  # Delete the files when the core change is pushed.
  $ tt binary workspace --remove`,
		Args: cobra.NoArgs,
		RunE: binaryRunE(base, &workCtx, internalBinaryWorkspace),
	}

	flags := workspaceCmd.Flags()
	flags.StringVar(&workCtx.manifest, "manifest", "",
		"path to the manifest (default: "+manifest.DistributionFile+" in the project directory)")
	flags.StringVar(&workCtx.core, "core", "",
		"take this local checkout of the core instead of [core].path (env "+distCoreEnv+")")
	flags.BoolVar(&workCtx.remove, "remove", false,
		"delete the go.work files tt wrote, and their go.work.sum")
	flags.BoolVarP(&workCtx.force, "force", "f", false,
		"overwrite a go.work tt did not write")
	flags.BoolVar(&workCtx.dryRun, "dry-run", false,
		"print what would be written or removed, and change nothing")
	workspaceCmd.MarkFlagsMutuallyExclusive("remove", "force")
	addProjectDirFlag(workspaceCmd)

	return workspaceCmd
}

// internalBinaryWorkspace writes, or removes, the go.work files of the
// distribution and prints their paths.
func internalBinaryWorkspace(_ binary.BinaryContext, workCtx workspaceCtx,
	_ *cmdcontext.CmdCtx, _ []string,
) error {
	dir, err := absoluteWorkingDir()
	if err != nil {
		return err
	}

	manifestPath := workCtx.manifest
	if manifestPath == "" {
		manifestPath = manifest.DistributionFile
	}

	core := workCtx.core
	if core == "" {
		core = os.Getenv(distCoreEnv)
	}

	result, err := distbuild.Workspace(context.Background(), distbuild.WorkspaceOptions{
		Manifest: inDir(dir, manifestPath),
		Core:     core,
		CoreDir:  dir,
		Remove:   workCtx.remove,
		Force:    workCtx.force,
		DryRun:   workCtx.dryRun,
		Environ:  nil,
	})
	if err != nil {
		return err
	}

	for idx, file := range result.Files {
		err = printWorkspaceFile(idx, file, workCtx.dryRun)
		if err != nil {
			return fmt.Errorf("writing the result: %w", err)
		}
	}

	return nil
}

// printWorkspaceFile prints the path of the idx-th file, and, for a dry
// run that writes it, what it would hold, a blank line between two files.
func printWorkspaceFile(idx int, file distbuild.WorkspaceFile, dryRun bool) error {
	if !dryRun || file.Content == nil {
		_, err := fmt.Fprintln(os.Stdout, file.Path)

		return err //nolint:wrapcheck // The caller wraps it.
	}

	if idx > 0 {
		_, err := fmt.Fprintln(os.Stdout)
		if err != nil {
			return err //nolint:wrapcheck // The caller wraps it.
		}
	}

	_, err := fmt.Fprintf(os.Stdout, "%s:\n%s", file.Path, file.Content)

	return err //nolint:wrapcheck // The caller wraps it.
}
