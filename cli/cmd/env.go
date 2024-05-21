package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/env"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// NewEnvCmd creates env command.
func NewEnvCmd() *cobra.Command {
	var envCmd = &cobra.Command{
		Use:   "env",
		Short: "Add current environment binaries location to the PATH variable",
		Long: "Add current environment binaries location to the PATH variable.\n" +
			"Also sets TARANTOOL_DIR variable.",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalEnvModule, args)
			util.HandleCmdErr(cmd, err)
		},
	}

	return envCmd
}

// internalEnvModule is a default env module.
func internalEnvModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	_, err = fmt.Print(env.CreateEnvString(cliOpts))
	return err
}
