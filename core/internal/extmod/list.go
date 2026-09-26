package extmod

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newModulesListCmd creates a new `modules list` subcommand, which lists
// modules on out.
func newModulesListCmd(modules []Manifest, out io.Writer) *cobra.Command {
	var showVersion, showPath bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available modules",
		RunE: func(*cobra.Command, []string) error {
			internalModulesList(out, modules, showVersion, showPath)

			return nil
		},
	}

	cmd.Flags().BoolVarP(&showVersion, "version", "v", false, "Show modules version")
	cmd.Flags().BoolVarP(&showPath, "path", "p", false,
		"Show modules path instead of description")

	return cmd
}

// newModulesCmd creates a new `modules` command, which lists modules on out.
func newModulesCmd(modules []Manifest, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "modules",
		Short: "Manage tt cli modules",
	}
	cmd.AddCommand(
		newModulesListCmd(modules, out),
	)

	return cmd
}

// internalModulesList produce list all available external modules, with
// their versions when showVersion is set and their executables instead of
// their descriptions when showPath is.
func internalModulesList(out io.Writer, modules []Manifest, showVersion, showPath bool) {
	for _, module := range modules {
		if showVersion {
			_, _ = fmt.Fprintf(out, "%-5s\t", module.Version)
		}

		_, _ = fmt.Fprintf(out, "%s - ", module.Name)

		if showPath {
			_, _ = fmt.Fprint(out, module.Main)
		} else {
			_, _ = fmt.Fprint(out, module.Help)
		}

		_, _ = fmt.Fprint(out, "\n")
	}
}
