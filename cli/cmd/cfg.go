package cmd

import (
	"github.com/spf13/cobra"
)

// NewCfgCmd creates a new cfg command.
func NewCfgCmd() *cobra.Command {
	cfgCmd := &cobra.Command{
		Use:                   "cfg <command> [command flags]",
		DisableFlagParsing:    true,
		DisableFlagsInUseLine: true,
		Short:                 "Environment configuration management utility",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
		Example: `# Print tt environment configuration:

	$ tt cfg dump`,
	}
	cfgCmd.AddCommand(
		NewDumpCmd(),
	)

	return cfgCmd
}
