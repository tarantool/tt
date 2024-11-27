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
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
	libconnect "github.com/tarantool/tt/lib/connect"
)

// playFlags contains flags for play command.
// Initialized with default values at creation.
var playFlags = checkpoint.Opts{
	From:       0,
	To:         math.MaxUint64,
	Timestamp:  "",
	Space:      nil,
	Replica:    nil,
	ShowSystem: false,
}

var (
	// playUsername contains username flag.
	playUsername string
	// playPassword contains password flag.
	playPassword string
)

// NewPlayCmd creates a new play command.
func NewPlayCmd() *cobra.Command {
	var playCmd = &cobra.Command{
		Use:   "play <URI> <FILE>...",
		Short: "Play the contents of .snap/.xlog FILE(s) to another Tarantool instance",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalPlayModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Example: "tt play uri /path/to/file.snap /path/to/file.xlog /path/to/dir/ " +
			"--timestamp 2024-11-13T14:02:36.818700000+00:00\n" +
			"  tt play uri /path/to/file.snap /path/to/file.xlog /path/to/dir/ " +
			"--timestamp=1731592956.818",
	}

	playCmd.Flags().StringVarP(&playUsername, "username", "u", "", "username")
	playCmd.Flags().StringVarP(&playPassword, "password", "p", "", "password")
	playCmd.Flags().Uint64Var(&playFlags.To, "to", playFlags.To,
		"Show operations ending with the given lsn")
	playCmd.Flags().StringVar(&playFlags.Timestamp, "timestamp", playFlags.Timestamp,
		"Show operations ending with the given timestamp")
	playCmd.Flags().Uint64Var(&playFlags.From, "from", playFlags.From,
		"Show operations starting from the given lsn")
	playCmd.Flags().IntSliceVar(&playFlags.Space, "space", playFlags.Space,
		"Filter the output by space number. May be passed more than once")
	playCmd.Flags().IntSliceVar(&playFlags.Replica, "replica", playFlags.Replica,
		"Filter the output by replica id. May be passed more than once")
	playCmd.Flags().BoolVar(&playFlags.ShowSystem, "show-system", playFlags.ShowSystem,
		"Show the contents of system spaces")

	return playCmd
}

// internalPlayModule is a default play module.
func internalPlayModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) < 2 {
		return errors.New("it is required to specify an URI and at least one .xlog/.snap file " +
			"or directory")
	}

	walFiles, err := util.CollectWALFiles(args[1:])
	if err != nil {
		return util.InternalError(
			"Internal error: could not collect WAL files: %s",
			version.GetVersion, err)
	}

	// Re-create args with the URI in the first index, and all founded files after.
	uriAndWalFiles := append([]string{args[0]}, walFiles...)

	// List of files and URI is passed to lua play script via environment variable in json format.
	filesAndUriJson, err := json.Marshal(uriAndWalFiles)
	if err != nil {
		return util.InternalError(
			"Internal error: problem with creating json params with files and uri: %s",
			version.GetVersion, err)
	}

	if libconnect.IsCredentialsURI(args[0]) {
		if playUsername != "" || playPassword != "" {
			return errors.New("username and password are specified with" +
				" flags and a URI")
		}
	} else {
		if playUsername == "" {
			playUsername = os.Getenv(libconnect.TarantoolUsernameEnv)
		}
		if playPassword == "" {
			playPassword = os.Getenv(libconnect.TarantoolPasswordEnv)
		}
	}

	os.Setenv("TT_CLI_PLAY_FILES_AND_URI", string(filesAndUriJson))
	if playUsername != "" {
		os.Setenv("TT_CLI_PLAY_USERNAME", playUsername)
	}
	if playPassword != "" {
		os.Setenv("TT_CLI_PLAY_PASSWORD", playPassword)
	}
	os.Setenv("TT_CLI_PLAY_SHOW_SYS", strconv.FormatBool(playFlags.ShowSystem))

	// List of spaces is passed to lua play script via environment variable in json format.
	spacesJson, err := json.Marshal(playFlags.Space)
	if err != nil {
		return util.InternalError(
			"Internal error: problem with creating json params with spaces: %s",
			version.GetVersion, err)
	}
	if string(spacesJson) != "null" {
		os.Setenv("TT_CLI_PLAY_SPACES", string(spacesJson))
	}

	os.Setenv("TT_CLI_PLAY_FROM", strconv.FormatUint(playFlags.From, 10))
	os.Setenv("TT_CLI_PLAY_TO", strconv.FormatUint(playFlags.To, 10))

	timestamp, err := util.StringToTimestamp(playFlags.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to parse a timestamp: %s", err)
	}
	os.Setenv("TT_CLI_PLAY_TIMESTAMP", timestamp)

	// List of replicas is passed to lua play script via environment variable in json format.
	replicasJson, err := json.Marshal(playFlags.Replica)
	if err != nil {
		return util.InternalError(
			"Internal error: problem with creating json params with replicas: %s",
			version.GetVersion, err)
	}
	if string(replicasJson) != "null" {
		os.Setenv("TT_CLI_PLAY_REPLICAS", string(replicasJson))
	}

	log.Infof("Running play with URI=%s and files: %s\n", args[0], args[1:])
	if err := checkpoint.Play(cmdCtx.Cli.TarantoolCli); err != nil {
		return err
	}

	return nil
}
