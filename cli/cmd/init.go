package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	init_pkg "github.com/tarantool/tt/cli/init"
)

var initCtx init_pkg.InitCtx

// NewInitCmd analyses current working directory and generates tt.yaml for existing
// application found in working dir. It there is no app in current directory, default version
// of tt.yaml will be generated.
func NewInitCmd() *cobra.Command {
	var initCmd = &cobra.Command{
		Use:   "init [flags]",
		Short: "Create tt environment config for application in current directory",
		Run:   RunModuleFunc(internalInitModule),
	}

	initCmd.Flags().BoolVarP(&initCtx.SkipConfig, "skip-config", "", false,
		`Skip loading directories info from tarantoolctl and .cartridge.yml configs`)
	initCmd.Flags().BoolVarP(&initCtx.ForceMode, "force", "f", false,
		fmt.Sprintf(`Force re-write existing %s`, configure.ConfigName))

	return initCmd
}

// internalInitModule is a default init module.
func internalInitModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	initCtx.TarantoolExecutable = cmdCtx.Cli.TarantoolCli.Executable
	init_pkg.FillCtx(&initCtx)
	return init_pkg.Run(&initCtx)
}
