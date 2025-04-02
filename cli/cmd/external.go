package cmd

import (
	"strings"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// configureExternalCmd configures external commands.
func configureExternalCmd(rootCmd *cobra.Command,
	modulesInfo *modules.ModulesInfo, forceInternal bool, args []string) {
	configureExistsCmd(rootCmd, modulesInfo, forceInternal)
	configureNonExistentCmd(rootCmd, modulesInfo, args)
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
func configureNonExistentCmd(rootCmd *cobra.Command,
	modulesInfo *modules.ModulesInfo, args []string) {
	// Since the user can pass flags, to determine the name of
	// an external command we have to take the first non-flag argument.
	externalCmd := args[0]
	for _, name := range args {
		if !strings.HasPrefix(name, "-") && name != "help" {
			externalCmd = name
			break
		}
	}

	// We avoid overwriting existing commands - we should add a command only
	// if it doesn't have an internal implementation in Tarantool CLI.
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == externalCmd {
			return
		}
	}

	helpCmd := util.GetHelpCommand(rootCmd)
	externalCmdPath := rootCmd.Name() + " " + externalCmd
	if _, found := (*modulesInfo)[externalCmdPath]; found {
		rootCmd.AddCommand(newExternalCommand(modulesInfo, externalCmd,
			externalCmdPath, nil))
		helpCmd.AddCommand(newExternalCommand(modulesInfo, externalCmd, externalCmdPath,
			[]string{"--help"}))
	}
}

// newExternalCommand returns a pointer to a new external
// command that will call modules.RunCmd.
func newExternalCommand(modulesInfo *modules.ModulesInfo,
	cmdName, cmdPath string, addArgs []string) *cobra.Command {
	cmd := &cobra.Command{
		Use: cmdName,
		Run: func(cmd *cobra.Command, args []string) {
			if addArgs != nil {
				args = append(args, addArgs...)
			}

			cmdCtx.Cli.ForceInternal = false
			if err := modules.RunCmd(&cmdCtx, cmdPath, modulesInfo, nil, args); err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	cmd.DisableFlagParsing = true
	return cmd
}
