package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
)

var (
	local bool
)

// NewSearchCmd creates search command.
func NewSearchCmd() *cobra.Command {
	var searchCmd = &cobra.Command{
		Use:   "search <PROGRAM>",
		Short: "Search for available versions for the program",
		Long: "Search for available versions for the program\n\n" +
			"Available programs:\n" +
			"tt - tarantool CLI Community Edition\n" +
			"tarantool - tarantool Community Edition\n" +
			"tarantool-ee - tarantool Enterprise Edition",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalSearchModule, args)
			handleCmdErr(cmd, err)
		},
		ValidArgs: []string{"tt", "tarantool", "tarantool-ee"},
	}
	searchCmd.Flags().BoolVarP(&local, "local-repo", "", false,
		"search in local files")
	return searchCmd
}

// internalSearchModule is a default search module.
func internalSearchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	if len(args) == 0 {
		return util.NewArgError("missing program name\n\nAvailable programs:\n" +
			"tt - tarantool CLI Community Edition\n" +
			"tarantool - tarantool Community Edition\n" +
			"tarantool-ee - tarantool Enterprise Edition\n")
	}
	if len(args) != 1 {
		return util.NewArgError("incorrect arguments")
	}
	if local {
		err = search.SearchVersionsLocal(cmdCtx, cliOpts, args[0])
	} else {
		err = search.SearchVersions(cmdCtx, cliOpts, args[0])
	}
	return err
}
