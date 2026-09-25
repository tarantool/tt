package cmd

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/modules"
)

// configureExternalCmd configures external commands. moduleOwner names the
// module a command comes from; it may be nil.
func configureExternalCmd(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo,
	forceInternal bool, moduleOwner func(*cobra.Command) (string, bool),
) {
	configureExistsCmd(rootCmd, modulesInfo, forceInternal, moduleOwner)
	configureNonExistentCmd(rootCmd, modulesInfo)
}

// externalModuleHelpFunc returns function that displays help for the specified external module.
func externalModuleHelpFunc(manifest modules.Manifest) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		help, err := modules.GetExternalModuleHelp(manifest.Main)
		if err != nil {
			cmd.PrintErrf("failed to get help for module %q: %s\n", manifest.Name, err)
			return
		}
		cmd.Print(help)
	}
}

// configureExistsCmd configures an external commands
// that have internal implementation.
//
// A legacy command runs through modules.RunCmd, which runs the external
// module instead unless -I is given; here it only stops parsing flags, so
// that they reach the module, and shows the module's help. A command a
// module contributed does not run through modules.RunCmd, so it is replaced
// by the external command, with a warning, unless -I is given.
func configureExistsCmd(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo,
	forceInternal bool, moduleOwner func(*cobra.Command) (string, bool),
) {
	for _, cmd := range slices.Clone(rootCmd.Commands()) {
		manifest, found := (*modulesInfo)[cmd.CommandPath()]
		if !found {
			continue
		}

		if isLegacy(cmd) {
			cmd.DisableFlagParsing = !forceInternal
			cmd.SetHelpFunc(externalModuleHelpFunc(manifest))

			continue
		}

		if forceInternal {
			continue
		}

		replaced := fmt.Sprintf("command %q", cmd.Name())
		if moduleOwner != nil {
			if owner, ok := moduleOwner(cmd); ok {
				replaced += fmt.Sprintf(" of module %q", owner)
			}
		}

		log.Warnf("External module %q (%s) replaces the %s; run tt with -I to keep it",
			manifest.Name, manifest.Main, replaced)

		rootCmd.RemoveCommand(cmd)
		rootCmd.AddCommand(newExternalCmd(manifest))
	}
}

// configureNonExistentCmd configures an external command that
// has no internal implementation within the Tarantool CLI.
func configureNonExistentCmd(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo) {
	// We avoid overwriting existing commands - we should add a command only
	// if it doesn't have an internal implementation in Tarantool CLI.
	// So first collect list of internal command names.
	internalCmdNames := make([]string, 0, 1+len(rootCmd.Commands()))
	internalCmdNames = append(internalCmdNames, "help")
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
	cmd := &cobra.Command{
		Use:                manifest.Name,
		RunE:               RunModuleFuncE(nil),
		DisableFlagParsing: true,
	}
	cmd.SetHelpFunc(externalModuleHelpFunc(manifest))
	return cmd
}
