package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/rocks"
)

// NewRocksCmd creates rocks command.
func NewRocksCmd() *cobra.Command {
	rocksCmd := &cobra.Command{
		Use:   "rocks",
		Short: "LuaRocks package manager",
		Long: "Run LuaRocks on the .rocks tree of the current directory. The " +
			"rock servers are the ones tt registry list shows when they come " +
			"from " + manifestrocks.EnvRegistries + " or [platform].registries " +
			"of app.manifest.toml; without either LuaRocks queries its own.",
		// Disabled all flags parsing on this commands leaf.
		// LuaRocks will handle it self.
		DisableFlagParsing: true,
		RunE:               internalRunE(internalRocksModule),
	}

	return rocksCmd
}

// internalRocksModule is a default rocks module.
func internalRocksModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	servers, err := rocksServers()
	if err != nil {
		return err
	}

	return rocks.Exec(cmdCtx, cliOpts, servers, args)
}

// rocksServers returns the rock servers tt rocks queries: the list tt registry
// takes from TT_REGISTRIES or from [platform].registries of the manifest in
// the working directory. Without either it is empty, and LuaRocks keeps its
// own servers rather than the built-in defaults of tt registry: tt rocks is
// LuaRocks, and its defaults and the user's LuaRocks config are what it has
// always queried. tt rocks parses no flags, so --registry plays no part.
func rocksServers() ([]string, error) {
	registries, err := effectiveRegistries()
	if err != nil {
		return nil, err
	}

	if len(registries) == 0 || registries[0].Source == manifestrocks.SourceDefault {
		return nil, nil
	}

	return manifestrocks.URLs(registries), nil
}
