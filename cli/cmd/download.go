package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/download"
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
		Run: TtModuleCmdRun(internalDownloadModule),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("to download Tarantool SDK, you need to specify the version")
			} else if len(args) > 1 {
				return errors.New("invalid number of parameters")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&downloadCtx.DevBuild, "dev", false, "download development build")
	cmd.Flags().StringVar(&downloadCtx.DirectoryPrefix,
		"directory-prefix", downloadCtx.DirectoryPrefix,
		`directory prefix to save SDK. The default is "." (the current directory)`)

	return cmd
}

func internalDownloadModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	downloadCtx.Version = args[0]
	return download.DownloadSDK(cmdCtx, downloadCtx, cliOpts)
}
