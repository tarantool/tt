package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
)

var (
	local     bool
	debug     bool
	searchCtx = search.SearchCtx{
		Filter: search.SearchRelease,
	}
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
		Example: `
# Remote search across all versions of Tarantool Enterprise Edition.

    $ tt search tarantool-ee

# Remote search across all 2.11 debug versions of Tarantool Enterprise Edition.

    $ tt search tarantool-ee --debug --version 2.11`,
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalSearchModule, args)
			handleCmdErr(cmd, err)
		},
		ValidArgs: []string{"tt", "tarantool", "tarantool-ee"},
	}
	searchCmd.Flags().BoolVarP(&local, "local-repo", "", false,
		"search in local files")
	searchCmd.Flags().BoolVar(&debug, "debug", debug,
		"search for debug builds of tarantool-ee SDK")
	searchCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", searchCtx.ReleaseVersion,
		"specify version")
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
		if debug || (len(searchCtx.ReleaseVersion) > 0) {
			log.Warnf("--debug and --version options can only be used to" +
				" search for tarantool-ee packages.")
		}
		err = search.SearchVersionsLocal(cmdCtx, cliOpts, args[0])
	} else {
		if debug {
			searchCtx.Filter = search.SearchDebug
		}
		err = search.SearchVersions(cmdCtx, searchCtx, cliOpts, args[0])
	}
	return err
}
