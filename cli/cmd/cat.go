package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/checkpoint"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// catFlags contains flags for cat command.
// Initialized with default values at creation.
var catFlags = checkpoint.Opts{
	From:       0,
	To:         math.MaxUint64,
	Space:      nil,
	Format:     "yaml",
	Replica:    nil,
	ShowSystem: false,
}

// NewCatCmd creates a new cat command.
func NewCatCmd() *cobra.Command {
	var catCmd = &cobra.Command{
		Use:   "cat <FILE>...",
		Short: "Print into stdout the contents of .snap/.xlog files",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalCatModule, args)
			handleCmdErr(cmd, err)
		},
	}

	catCmd.Flags().Uint64Var(&catFlags.To, "to", catFlags.To,
		"Show operations ending with the given lsn")
	catCmd.Flags().Uint64Var(&catFlags.From, "from", catFlags.From,
		"Show operations starting from the given lsn")
	catCmd.Flags().IntSliceVar(&catFlags.Space, "space", catFlags.Space,
		"Filter the output by space number. May be passed more than once")
	catCmd.Flags().StringVar(&catFlags.Format, "format", catFlags.Format,
		"Output format yaml, json or lua")
	catCmd.Flags().IntSliceVar(&catFlags.Replica, "replica", catFlags.Replica,
		"Filter the output by replica id. May be passed more than once")
	catCmd.Flags().BoolVar(&catFlags.ShowSystem, "show-system", catFlags.ShowSystem,
		"Show the contents of system spaces")

	return catCmd
}

// internalCatModule is a default cat module.
func internalCatModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("it is required to specify at least one .xlog or .snap file")
	}

	// List of files is passed to lua cat script via environment variable in json format.
	filesJson, err := json.Marshal(args)
	if err != nil {
		util.InternalError("Internal error: problem with creating json params with files: %s",
			version.GetVersion, err)
	}

	os.Setenv("TT_CLI_CAT_FILES", string(filesJson))
	os.Setenv("TT_CLI_CAT_SHOW_SYS", strconv.FormatBool(catFlags.ShowSystem))
	os.Setenv("TT_CLI_CAT_FORMAT", string(catFlags.Format))

	// List of spaces is passed to lua cat script via environment variable in json format.
	spacesJson, err := json.Marshal(catFlags.Space)
	if err != nil {
		util.InternalError("Internal error: problem with creating json params with spaces: %s",
			version.GetVersion, err)
	}
	if string(spacesJson) != "null" {
		os.Setenv("TT_CLI_CAT_SPACES", string(spacesJson))
	}

	os.Setenv("TT_CLI_CAT_FROM", strconv.FormatUint(catFlags.From, 10))
	os.Setenv("TT_CLI_CAT_TO", strconv.FormatUint(catFlags.To, 10))

	// List of replicas is passed to lua cat script via environment variable in json format.
	replicasJson, err := json.Marshal(catFlags.Replica)
	if err != nil {
		util.InternalError("Internal error: problem with creating json params with replicas: %s",
			version.GetVersion, err)
	}
	if string(replicasJson) != "null" {
		os.Setenv("TT_CLI_CAT_REPLICAS", string(replicasJson))
	}

	log.Infof("Running cat with files: %s\n", args)
	if err := checkpoint.Cat(cmdCtx.Cli.TarantoolCli); err != nil {
		return err
	}

	return nil
}
