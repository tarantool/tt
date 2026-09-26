package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/env"
)

// NewEnvCmd creates env command.
func NewEnvCmd() *cobra.Command {
	envCmd := &cobra.Command{
		Use:   "env",
		Short: "Add current environment binaries location to the PATH variable",
		Long: "Add current environment binaries location to the PATH variable.\n" +
			"Also sets TARANTOOL_DIR variable.",
		RunE: internalRunE(internalEnvModule),
	}

	return envCmd
}

// internalEnvModule is a default env module.
func internalEnvModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	_, err := fmt.Fprint(os.Stdout, env.CreateEnvString(cliOpts))
	if err != nil {
		return fmt.Errorf("failed to print the environment: %w", err)
	}

	return nil
}
