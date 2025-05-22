package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	cc "github.com/tarantool/cartridge-cli/cli/commands"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// generateRunDirForCartridge is a helper function for cartridge compatibility. It generates
// run directory path using instances enabled, run dir and app name info.
func generateRunDirForCartridge(env config.TtEnvOpts, configDir, runDir, appName string) (
	string, error,
) {
	if filepath.IsAbs(runDir) {
		return util.JoinAbspath(runDir, appName)
	}

	if runDir == "" {
		return "", fmt.Errorf("empty run directory path")
	}

	if env.InstancesEnabled == "." {
		if util.IsApp(configDir) {
			return util.JoinAbspath(configDir, runDir)
		}
		return util.JoinAbspath(configDir, appName, runDir)
	}
	return util.JoinAbspath(env.InstancesEnabled, appName, runDir)
}

// NewCartridgeCmd chains commands from cartridge-cli to our corba tree.
func NewCartridgeCmd() *cobra.Command {
	cartridgeCmd := &cobra.Command{
		Use:   "cartridge",
		Short: "Manage cartridge application",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.ParseFlags(args); err != nil {
				return err
			}
			appName := ""
			if nameOpt := cmd.Flag("name"); nameOpt != nil {
				appName = nameOpt.Value.String()
			} else {
				return nil
			}

			if cliOpts.Env != nil {
				os.Setenv("TT_INST_ENABLED", cliOpts.Env.InstancesEnabled)
				if runDir, err := generateRunDirForCartridge(
					*cliOpts.Env, cmdCtx.Cli.ConfigDir, cliOpts.App.RunDir, appName); err != nil {
					log.Warnf("cannot set run directory path for cartridge: %w", err)
				} else {
					os.Setenv("TT_RUN_DIR", runDir)
				}
			} else {
				return fmt.Errorf("config 'env' section is uninitialized")
			}
			return nil
		},
	}

	cartCliCmds := []*cobra.Command{
		cc.CartridgeCliAdmin,
		cc.CartridgeCliBench,
		cc.CartridgeCliFailover,
		cc.CartridgeCliRepair,
		cc.CartridgeCliReplica,
	}

	for _, cmd := range cartCliCmds {
		cartridgeCmd.AddCommand(cmd)
	}

	return cartridgeCmd
}
