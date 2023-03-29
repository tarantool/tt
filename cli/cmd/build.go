package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/build"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
)

var (
	specFile string
	inDocker bool
)

// NewBuildCmd builds an application.
func NewBuildCmd() *cobra.Command {
	var buildCmd = &cobra.Command{
		Use:   "build [PATH] [flags]",
		Short: `Build an application in specified PATH (default ".")`,
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalBuildModule, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.MaximumNArgs(1),
	}

	buildCmd.Flags().StringVarP(&specFile, "spec", "", "", "Rockspec file to use for building")

	return buildCmd
}

// internalBuildModule is a default build module.
func internalBuildModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if err := checkConfig(cmdCtx); err != nil {
		return err
	}

	var buildCtx build.BuildCtx
	if err := build.FillCtx(&buildCtx, args); err != nil {
		return err
	}

	buildCtx.SpecFile = specFile

	return build.Run(cmdCtx, cliOpts, &buildCtx)
}
