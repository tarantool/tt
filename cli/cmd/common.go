package cmd

import (
	"errors"
	"os"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/util"
)

// handleCmdErr handles an error returned by command implementation.
// If received error is of an ArgError type, usage help is printed.
func handleCmdErr(cmd *cobra.Command, err error) {
	if err != nil {
		var argError *util.ArgError
		if errors.As(err, &argError) {
			log.Error(argError.Error())
			cmd.Usage()
			os.Exit(1)
		}
		log.Fatalf(err.Error())
	}
}

// errNoConfig is returned if environment config file tt.yaml not found.
var errNoConfig = errors.New(configure.ConfigName +
	" not found, you need to create tt environment config with 'tt init'" +
	" or provide exact config location with --cfg option")

// isConfigExist returns `true` if environment config file tt.yaml exist.
func isConfigExist(cmdCtx *cmdcontext.CmdCtx) bool {
	return cmdCtx.Cli.ConfigPath != ""
}
