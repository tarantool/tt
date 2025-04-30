package cmd

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
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
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalModulesList, args)
			util.HandleCmdErr(cmd, err)
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
			fmt.Printf("%-5s\t", m.Version)
		}
		fmt.Printf("%s - ", m.Name)

		if showPath {
			fmt.Print(m.Main)
		} else {
			fmt.Print(m.Help)
		}

		fmt.Print("\n")
	}
	return nil
}
