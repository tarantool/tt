package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/search"
)

var (
	local     bool
	debug     bool
	searchCtx = search.SearchCtx{
		Filter: search.SearchRelease,
	}
)

// newSearchTtCmd creates a command to search tt.
func newSearchTtCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   "tt",
		Short: "Search for available tt versions",
		Run: func(cmd *cobra.Command, args []string) {
			searchCtx.ProgramName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalSearchModule, args)
			handleCmdErr(cmd, err)
		},
	}

	return tntCmd
}

// newSearchTarantoolCmd creates a command to search tarantool.
func newSearchTarantoolCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   "tarantool",
		Short: "Search for available tarantool community edition versions",
		Run: func(cmd *cobra.Command, args []string) {
			searchCtx.ProgramName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalSearchModule, args)
			handleCmdErr(cmd, err)
		},
	}

	return tntCmd
}

// newSearchTarantoolEeCmd creates a command to search tarantool-ee.
func newSearchTarantoolEeCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   "tarantool-ee",
		Short: "Search for available tarantool enterprise edition versions",
		Run: func(cmd *cobra.Command, args []string) {
			searchCtx.ProgramName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalSearchModule, args)
			handleCmdErr(cmd, err)
		},
	}
	tntCmd.Flags().BoolVar(&debug, "debug", debug,
		"search for debug builds of tarantool-ee SDK")
	tntCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", searchCtx.ReleaseVersion,
		"specify version")
	tntCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"search for development builds of tarantool-ee SDK")

	return tntCmd
}

// NewSearchCmd creates search command.
func NewSearchCmd() *cobra.Command {
	var searchCmd = &cobra.Command{
		Use:   "search",
		Short: "Search for available versions for the program",
		Example: `
# Remote search across all versions of Tarantool Enterprise Edition.

    $ tt search tarantool-ee

# Remote search across all 2.11 debug versions of Tarantool Enterprise Edition.

    $ tt search tarantool-ee --debug --version 2.11`,
	}
	searchCmd.Flags().BoolVarP(&local, "local-repo", "", false,
		"search in local files")

	searchCmd.AddCommand(
		newSearchTarantoolCmd(),
		newSearchTarantoolEeCmd(),
		newSearchTtCmd(),
	)

	return searchCmd
}

// internalSearchModule is a default search module.
func internalSearchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	if local {
		err = search.SearchVersionsLocal(cmdCtx, cliOpts, searchCtx.ProgramName)
	} else {
		if debug {
			searchCtx.Filter = search.SearchDebug
		}
		err = search.SearchVersions(cmdCtx, searchCtx, cliOpts, searchCtx.ProgramName)
	}
	return err
}
