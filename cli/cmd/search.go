package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/search"
)

var (
	local     bool
	debug     bool
	searchCtx = search.NewSearchCtx(search.NewPlatformInformer(), search.NewTntIoDoer())
)

// newSearchTtCmd creates a command to search tt.
func newSearchTtCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   search.ProgramTt.String(),
		Short: "Search for available tt versions",
		RunE:  RunModuleFuncE(internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}

	return tntCmd
}

// newSearchTarantoolCmd creates a command to search tarantool.
func newSearchTarantoolCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   search.ProgramCe.String(),
		Short: "Search for available tarantool community edition versions",
		RunE:  RunModuleFuncE(internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}

	return tntCmd
}

// newSearchTarantoolEeCmd creates a command to search tarantool-ee.
func newSearchTarantoolEeCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   search.ProgramEe.String(),
		Short: "Search for available tarantool enterprise edition versions",
		RunE:  RunModuleFuncE(internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}
	tntCmd.Flags().BoolVar(&debug, "debug", debug,
		"search for debug builds of tarantool-ee SDK")
	tntCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", searchCtx.ReleaseVersion,
		"specify version")
	tntCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"search for development builds of tarantool-ee SDK")

	return tntCmd
}

// newSearchTcmCmd creates a command to search tcm.
func newSearchTcmCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   search.ProgramTcm.String(),
		Short: "Search for available tarantool cluster manager versions",
		RunE:  RunModuleFuncE(internalSearchModule),
		Args:  cobra.ExactArgs(0),
	}
	tcmCmd.Flags().StringVar(&searchCtx.ReleaseVersion, "version", searchCtx.ReleaseVersion,
		"specify version")
	tcmCmd.Flags().BoolVar(&searchCtx.DevBuilds, "dev", false,
		"search for development builds of TCM")

	return tcmCmd
}

// NewSearchCmd creates search command.
func NewSearchCmd() *cobra.Command {
	searchCmd := &cobra.Command{
		Use:   "search",
		Short: "Search for available versions for the program",
		Example: `
# Remote search across all versions of Tarantool Enterprise Edition.

    $ tt search tarantool-ee

# Remote search across all 3.0 debug versions of Tarantool Enterprise Edition.

    $ tt search tarantool-ee --debug --version 3.0

# Remote search across all versions of Tarantool Cluster Manager.

	$ tt search tcm --version 1.3`,
	}
	searchCmd.Flags().BoolVarP(&local, "local-repo", "", false,
		"search in local files")

	searchCmd.AddCommand(
		newSearchTarantoolCmd(),
		newSearchTarantoolEeCmd(),
		newSearchTtCmd(),
		newSearchTcmCmd(),
	)

	return searchCmd
}

// internalSearchModule is a default search module.
func internalSearchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error

	searchCtx.Program, err = search.ParseProgram(cmdCtx.CommandName)
	if err != nil {
		return fmt.Errorf("failed to search: %w", err)
	}

	if local {
		return search.SearchVersionsLocal(searchCtx, cliOpts, cmdCtx.Cli.ConfigPath)
	}

	if debug {
		searchCtx.Filter = search.SearchDebug
	}

	return search.SearchVersions(searchCtx, cliOpts)
}
