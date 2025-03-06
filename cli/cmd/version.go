package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

var (
	showShort  bool
	needCommit bool
)

// NewVersionCmd creates a new version command.
func NewVersionCmd() *cobra.Command {
	var versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Show Tarantool CLI version information",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalVersionModule, args)
			util.HandleCmdErr(cmd, err)
		},
	}

	versionCmd.Flags().BoolVar(&showShort, "short", false, "Show version in short format")
	versionCmd.Flags().BoolVar(&needCommit, "commit", false, "Show commit")

	return versionCmd
}

// internalVersionModule is a default (internal) version module function.
func internalVersionModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	fmt.Println(version.GetVersion(showShort, needCommit))
	return nil
}
