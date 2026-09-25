package cmd

import (
	"fmt"
	"os"
	"slices"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/modules"
)

var (
	showVersion bool
	showPath    bool
)

// newModulesListCmd creates a new `modules list` subcommand.
func newModulesListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available modules",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalModulesList, args)

			return commandError(cmd, err)
		},
	}

	cmd.Flags().BoolVarP(&showVersion, "version", "v", false, "Show modules version")
	cmd.Flags().BoolVarP(&showPath, "path", "p", false,
		"Show modules path instead of description")

	return cmd
}

// NewModulesCmd creates a new `modules` command.
func NewModulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "modules",
		Short: "Manage tt cli modules",
	}
	cmd.AddCommand(
		newModulesListCmd(),
	)

	return cmd
}

// sortExternalModules returns a sorted list of external modules.
func sortExternalModules() []string {
	keys := make([]string, 0, len(modulesInfo))
	for k := range modulesInfo {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

// internalModulesList produce list all available external modules.
func internalModulesList(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	for _, path := range sortExternalModules() {
		m := modulesInfo[path]

		if showVersion {
			_, _ = fmt.Fprintf(os.Stdout, "%-5s\t", m.Version)
		}

		_, _ = fmt.Fprintf(os.Stdout, "%s - ", m.Name)

		if showPath {
			_, _ = fmt.Fprint(os.Stdout, m.Main)
		} else {
			_, _ = fmt.Fprint(os.Stdout, m.Help)
		}

		_, _ = fmt.Fprint(os.Stdout, "\n")
	}

	return nil
}
