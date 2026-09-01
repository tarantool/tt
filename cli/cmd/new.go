package cmd

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/manifest/scaffold"
)

// NewNewCmd creates `tt new`: the skeleton app.manifest.toml a package starts
// from.
//
// It is a top-level command rather than a `tt package` subcommand because it is
// what runs before a package exists — every `tt package` command reads the file
// this one writes.
func NewNewCmd() *cobra.Command {
	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Create a skeleton app.manifest.toml in the current directory",
		Long: "Write the manifest a tt package is described by: the format " +
			"version, the package name, the Tarantool and tt version " +
			"requirements and an empty [dependencies] table for tt package add " +
			"to write into. The name comes from -n, or from the directory when " +
			"that is already a package name — lowercase letters, digits and " +
			"dashes, starting with a letter. Components and products are left as " +
			"commented examples, since their contents depend on a layout only " +
			"the author knows. An existing app.manifest.toml is never " +
			"overwritten — the command fails instead. This is not tt create, " +
			"which lays out a whole application from a template; tt new adds one " +
			"file to a directory that may already hold a project.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runNew(); err != nil {
				log.Error(err.Error())
				os.Exit(scaffold.ExitCode(err))
			}
		},
		Example: `
# Create a manifest for the project in the current directory.

    $ tt new

# Name the package explicitly, when the directory is not named for it.

    $ tt new -n my-app`,
	}

	newCmd.Flags().StringVarP(&newPackageName, "name", "n", "",
		"package name to declare; defaults to the directory name")

	return newCmd
}

// newPackageName holds the -n value.
var newPackageName string

// runNew writes the skeleton manifest and reports where it landed.
func runNew() error {
	projectDir, err := absoluteWorkingDir()
	if err != nil {
		return err
	}

	result, err := scaffold.Create(scaffold.Options{
		ProjectDir: projectDir,
		Name:       newPackageName,
	})
	if err != nil {
		return err
	}

	fmt.Printf("created %s for package %s\n", result.Path, result.Package)

	return nil
}
