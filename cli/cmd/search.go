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
		Short: "Search available versions of tarantool/tt",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalSearchModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}
	searchCmd.Flags().BoolVarP(&local, "local", "", false,
		"search in local files")
	return searchCmd
}

// internalSearchModule is a default search module.
func internalSearchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	if len(args) == 0 {
		log.Warnf("Available programs: \n" +
			"tarantool-ee - Enterprise tarantool\n" +
			"tarantool - OpenSource tarantool\n" +
			"tt - OpenSource tarantool CLI")
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("Incorrect arguments")
	}
	if local {
		err = search.SearchVersionsLocal(cmdCtx, args[0])

	} else {
		err = search.SearchVersions(cmdCtx, args[0])
	}
	return err
}
