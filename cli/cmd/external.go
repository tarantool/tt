package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/modules"
	"golang.org/x/exp/slices"
)

// ExternalCmd configures external commands.
func configureExternalCmd(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo,
	forceInternal bool) {
	configureExistsCmd(rootCmd, modulesInfo, forceInternal)
	configureNonExistentCmd(rootCmd, modulesInfo)
}

// configureExistsCmd configures an external commands
// that have internal implementation.
func configureExistsCmd(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo,
	forceInternal bool) {
	for _, cmd := range rootCmd.Commands() {
		if _, found := (*modulesInfo)[cmd.CommandPath()]; found {
			cmd.DisableFlagParsing = !forceInternal
		}
	}
}

// configureNonExistentCmd configures an external command that
// has no internal implementation within the Tarantool CLI.
func configureNonExistentCmd(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo) {
	// We avoid overwriting existing commands - we should add a command only
	// if it doesn't have an internal implementation in Tarantool CLI.
	// So first collect list of internal command names.
	internalCmdNames := []string{"help"}
	for _, cmd := range rootCmd.Commands() {
		internalCmdNames = append(internalCmdNames, cmd.Name())
	}

	// Add external command only if it doesn't have an internal implementation in Tarantool CLI.
	for _, manifest := range *modulesInfo {
		if !slices.Contains(internalCmdNames, manifest.Name) {
			rootCmd.AddCommand(newExternalCmd(manifest))
		}
	}
}

// newExternalCmd returns a pointer to a new external
// command that will call modules.RunCmd.
func newExternalCmd(manifest modules.Manifest) *cobra.Command {
	var cmd = &cobra.Command{
		Use:                manifest.Name,
		Run:                RunModuleFunc(nil),
		DisableFlagParsing: true,
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		cmd.Print(manifest.Help)
	})
	return cmd
}
