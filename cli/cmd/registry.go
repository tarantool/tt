package cmd

import (
	"os"

	"github.com/apex/log"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/manifest/registry"
	manifestrocks "github.com/tarantool/tt/cli/manifest/rocks"
)

// registryFormat is -o of `tt registry list`.
var registryFormat string

// NewRegistryCmd creates the `tt registry` command group: the rock servers the
// package commands resolve against.
func NewRegistryCmd() *cobra.Command {
	registryCmd := &cobra.Command{
		Use:   "registry",
		Short: "Inspect the rock servers packages are resolved against",
		Long: "Report the rock servers tt package commands query. The list " +
			"comes from --registry, then " + manifestrocks.EnvRegistries + ", " +
			"then [platform].registries in app.manifest.toml, and falls back " +
			"to the built-in defaults.",
	}

	registryCmd.AddCommand(newRegistryListCmd())

	return registryCmd
}

// newRegistryListCmd wires `tt registry list`.
func newRegistryListCmd() *cobra.Command {
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Show the rock servers in the order they are queried",
		Long: "Print the effective rock-server list in resolution order, each " +
			"server with the layer it was configured in. The layers replace one " +
			"another rather than merging: the outermost one that names anything " +
			"is the whole list, because resolution takes the first server that " +
			"has a rock and an appended server would change which that is. Run " +
			"in a project directory to see what its manifest configures.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runRegistryList(); err != nil {
				log.Error(err.Error())
				os.Exit(registry.ExitCode(err))
			}
		},
	}

	listCmd.Flags().StringVarP(&registryFormat, "format", "o", "",
		"output format: table, json or yaml (default: table on a terminal, yaml otherwise)")
	addRegistryFlag(listCmd)

	return listCmd
}

// runRegistryList renders the effective server list.
func runRegistryList() error {
	format, err := registry.ParseFormat(registryFormat, isatty.IsTerminal(os.Stdout.Fd()))
	if err != nil {
		return err
	}

	registries, err := effectiveRegistries()
	if err != nil {
		return err
	}

	return registry.RenderList(os.Stdout, registries, format)
}
