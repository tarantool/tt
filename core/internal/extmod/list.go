package extmod

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newModulesListCmd creates a new `modules list` subcommand, which lists
// modules on out.
func newModulesListCmd(modules []*Module, out io.Writer) *cobra.Command {
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
func newModulesCmd(modules []*Module, out io.Writer) *cobra.Command {
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
// their descriptions when showPath is. Each module describes itself for the
// list (Module.Listed), and one that cannot is warned about.
func internalModulesList(out io.Writer, modules []*Module, showVersion, showPath bool) {
	for _, listed := range modules {
		module := listed.Listed()

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
