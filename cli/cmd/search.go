package cmd

import (
	"fmt"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/search"
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
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalSearchModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
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
		log.Warnf("Available programs: \n" +
			"tt - tarantool CLI Community Edition\n" +
			"tarantool - tarantool Community Edition\n" +
			"tarantool-ee - tarantool Enterprise Edition")
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("incorrect arguments")
	}
	if local {
		err = search.SearchVersionsLocal(cmdCtx, args[0])
	} else {
		err = search.SearchVersions(cmdCtx, args[0])
	}
	return err
}
