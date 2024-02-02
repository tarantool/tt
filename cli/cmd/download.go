package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/download"
	"github.com/tarantool/tt/cli/modules"
)

var (
	downloadCtx download.DownloadCtx
)

// newDownloadCmd Downloads and saves the Tarantool SDK.
func NewDownloadCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "download <VERSION>",
		Short: `Download Tarantool SDK`,
		Example: `
# Download Tarantool SDK to the current working directory.
	$ tt download gc64-3.0.0-0-gf58f7d82a-r23
# Download Tarantool SDK development build to the /tmp directory.
	$ tt download gc64-3.0.0-beta1-2-gcbb569b4c-r612 --dev --directory-prefix /tmp`,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalDownloadModule, args)
			handleCmdErr(cmd, err)
		},
	}

	cmd.Flags().BoolVar(&downloadCtx.DevBuild, "dev", false, "download development build")
	cmd.Flags().StringVar(&downloadCtx.DirectoryPrefix,
		"directory-prefix", downloadCtx.DirectoryPrefix,
		`directory prefix to save SDK. The default is "." (the current directory)`)

	return cmd
}

func internalDownloadModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	if err = download.FillCtx(cmdCtx, &downloadCtx, args); err != nil {
		return err
	}

	return download.DownloadSDK(cmdCtx, downloadCtx, cliOpts)
}
