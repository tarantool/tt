package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/modules"
	"golang.org/x/exp/slices"
)

var (
	commandGroupExternal = &cobra.Group{ID: "External"}
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
			cmd.GroupID = "|" + commandGroupExternal.ID
		}
	}
}

// configureNonExistentCmd configures an external command that
// has no internal implementation within the Tarantool CLI.
func configureNonExistentCmd(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo) {
	hasExternalCmd := false

	// Prepare list of internal command names.
	internalCmdNames := []string{"help"}
	for _, cmd := range rootCmd.Commands() {
		internalCmdNames = append(internalCmdNames, cmd.Name())
	}

	// Add external command only if it doesn't have an internal implementation in Tarantool CLI.
	for name, manifest := range *modulesInfo {
		if !slices.Contains(internalCmdNames, name) {
			rootCmd.AddCommand(newExternalCmd(name, manifest))
			hasExternalCmd = true
		}
	}

	if hasExternalCmd {
		rootCmd.AddGroup(commandGroupExternal)
	}
}

func externalCmdHelpFunc(cmd *cobra.Command, args []string) {
	module := modulesInfo[cmd.CommandPath()]
	helpMsg, err := modules.GetExternalModuleHelp(module.Main)
	if err != nil {
		cmd.PrintErr(err)
		return
	}
	cmd.Print(helpMsg)
}

// newExternalCmd returns a pointer to a new external
// command that will call modules.RunCmd.
func newExternalCmd(cmdName string, manifest modules.Manifest) *cobra.Command {
	desc, err := modules.GetExternalModuleDescription(manifest)
	if err != nil {
		desc = "description is absent"
	}

	var cmd = &cobra.Command{
		Use:                cmdName,
		Short:              desc,
		Run:                RunModuleFunc(nil),
		DisableFlagParsing: true,
		GroupID:            commandGroupExternal.ID,
	}
	cmd.SetHelpFunc(externalCmdHelpFunc)
	return cmd
}
