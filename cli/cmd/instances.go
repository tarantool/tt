package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/instances"
)

// NewInstancesCmd creates instances command.
func NewInstancesCmd() *cobra.Command {
	var instancesCmd = &cobra.Command{
		Use:   "instances",
		Short: "Show list of enabled applications",
		Run:   TtModuleCmdRun(internalInstancesModule),
		Args:  cobra.ExactArgs(0),
	}

	return instancesCmd
}

// internalInstancesModule is a default instances module.
func internalInstancesModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	err := instances.ListInstances(cmdCtx, cliOpts)

	return err
}
