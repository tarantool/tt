package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/checkpoint"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// catFlags contains flags for cat command.
// Initialized with default values at creation.
var catFlags = checkpoint.Opts{
	From:       0,
	To:         math.MaxUint64,
	Timestamp:  "",
	Space:      nil,
	Format:     "yaml",
	Replica:    nil,
	ShowSystem: false,
	Recursive:  false,
}

// NewCatCmd creates a new cat command.
func NewCatCmd() *cobra.Command {
	catCmd := &cobra.Command{
		Use:   "cat <FILE|DIR>...",
		Short: "Print into stdout the contents of .snap/.xlog FILE(s)",
		Run:   RunModuleFunc(internalCatModule),
		Example: "tt cat /path/to/file.snap /path/to/file.xlog /path/to/dir/ " +
			"--timestamp 2024-11-13T14:02:36.818700000+00:00\n" +
			"  tt cat /path/to/file.snap /path/to/file.xlog /path/to/dir/ " +
			"--timestamp=1731592956.818\n" +
			"  tt cat --recursive /path/to/dir1 /path/to/dir2",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("it is required to specify at least one .xlog/.snap file " +
					"or directory")
			}
			return nil
		},
	}

	catCmd.Flags().Uint64Var(&catFlags.To, "to", catFlags.To,
		"Show operations ending with the given lsn")
	catCmd.Flags().StringVar(&catFlags.Timestamp, "timestamp", catFlags.Timestamp,
		"Show operations ending with the given timestamp")
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
	catCmd.Flags().BoolVarP(&catFlags.Recursive, "recursive", "r", catFlags.Recursive,
		"Process WAL files in directories recursively")

	return catCmd
}

// internalCatModule is a default cat module.
func internalCatModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	walFiles, err := util.CollectWalFiles(args, catFlags.Recursive)
	if err != nil {
		return util.InternalError(
			"Internal error: could not collect WAL files: %s",
			version.GetVersion, err)
	}

	// List of files is passed to lua cat script via environment variable in json format.
	filesJson, err := json.Marshal(walFiles)
	if err != nil {
		return util.InternalError(
			"Internal error: problem with creating json params with files: %s",
			version.GetVersion, err)
	}

	os.Setenv("TT_CLI_CAT_FILES", string(filesJson))
	os.Setenv("TT_CLI_CAT_SHOW_SYS", strconv.FormatBool(catFlags.ShowSystem))
	os.Setenv("TT_CLI_CAT_FORMAT", string(catFlags.Format))

	// List of spaces is passed to lua cat script via environment variable in json format.
	spacesJson, err := json.Marshal(catFlags.Space)
	if err != nil {
		return util.InternalError(
			"Internal error: problem with creating json params with spaces: %s",
			version.GetVersion, err)
	}
	if string(spacesJson) != "null" {
		os.Setenv("TT_CLI_CAT_SPACES", string(spacesJson))
	}

	os.Setenv("TT_CLI_CAT_FROM", strconv.FormatUint(catFlags.From, 10))
	os.Setenv("TT_CLI_CAT_TO", strconv.FormatUint(catFlags.To, 10))

	timestamp, err := util.StringToTimestamp(catFlags.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to parse a timestamp: %s", err)
	}
	os.Setenv("TT_CLI_CAT_TIMESTAMP", timestamp)

	// List of replicas is passed to lua cat script via environment variable in json format.
	replicasJson, err := json.Marshal(catFlags.Replica)
	if err != nil {
		return util.InternalError(
			"Internal error: problem with creating json params with replicas: %s",
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
