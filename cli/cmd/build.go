package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/build"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/running"
)

var (
	specFile string
	inDocker bool
)

// NewBuildCmd builds an application.
func NewBuildCmd() *cobra.Command {
	buildCmd := &cobra.Command{
		Use:   "build [<PATH> | <APP_NAME>] [flags]",
		Short: `Build an application (default ".")`,
		Run:   RunModuleFunc(internalBuildModule),
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			var runningCtx running.RunningCtx
			err := running.FillCtx(cliOpts, &cmdCtx, &runningCtx, nil, running.ConfigLoadSkip)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return running.ExtractAppNames(runningCtx.Instances),
				cobra.ShellCompDirectiveNoFileComp
		},
	}

	buildCmd.Flags().StringVarP(&specFile, "spec", "", "", "Rockspec file to use for building")

	return buildCmd
}

// internalBuildModule is a default build module.
func internalBuildModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var buildCtx build.BuildCtx
	if err := build.FillCtx(&buildCtx, cliOpts, args); err != nil {
		return err
	}

	buildCtx.SpecFile = specFile

	return build.Run(cmdCtx, cliOpts, &buildCtx)
}
