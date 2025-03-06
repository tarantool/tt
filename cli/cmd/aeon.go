package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/cobra"
	aeon "github.com/tarantool/tt/cli/aeon"
	aeoncmd "github.com/tarantool/tt/cli/aeon/cmd"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/console"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
)

const (
	aeonHistoryFileName = ".aeon_history"
	aeonHistoryLines    = console.DefaultHistoryLines
)

var connectCtx = aeoncmd.ConnectCtx{
	Transport: aeoncmd.TransportPlain,
}

func newAeonConnectCmd() *cobra.Command {
	var aeonCmd = &cobra.Command{
		Use:   "connect (<URI> | <PATH INSTANCE_NAME> | <APP_NAME:INSTANCE_NAME>)",
		Short: "Connect to the aeon instance",
		Long: `Connect to the aeon instance.
		tt aeon connect http://localhost:50051
		tt aeon connect unix://<socket-path>
		tt aeon connect /path/to/config INSTANCE_NAME>`,
		Run: func(cmd *cobra.Command, args []string) {

			fmt.Println("this flag connectCtx.Ssl.CaFile", connectCtx.Ssl.CaFile)

			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalAeonConnect, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.MatchAll(cobra.RangeArgs(1, 2), aeonConnectValidateArgs),
	}

	aeonCmd.Flags().StringVar(&connectCtx.Ssl.KeyFile, "sslkeyfile", "",
		"path to a private SSL key file")
	aeonCmd.Flags().StringVar(&connectCtx.Ssl.CertFile, "sslcertfile", "",
		"path to a SSL certificate file")
	aeonCmd.Flags().StringVar(&connectCtx.Ssl.CaFile, "sslcafile", "",
		"path to a trusted certificate authorities (CA) file")
	aeonCmd.Flags().Var(&connectCtx.Transport, "transport",
		fmt.Sprintf("allowed %s", aeoncmd.ListValidTransports()))
	aeonCmd.RegisterFlagCompletionFunc("transport", aeonTransportCompletion)

	return aeonCmd
}

func aeonTransportCompletion(cmd *cobra.Command, args []string, toComplete string) (
	[]string, cobra.ShellCompDirective) {
	suggest := make([]string, 0, len(aeoncmd.ValidTransport))
	for k, v := range aeoncmd.ValidTransport {
		suggest = append(suggest, string(k)+"\t"+v)
	}
	return suggest, cobra.ShellCompDirectiveDefault
}

// NewAeonCmd() create new aeon command.
func NewAeonCmd() *cobra.Command {
	var aeonCmd = &cobra.Command{
		Use:   "aeon",
		Short: "Manage aeon application",
	}
	aeonCmd.AddCommand(
		newAeonConnectCmd(),
	)
	return aeonCmd
}

func aeonConnectValidateArgs(cmd *cobra.Command, args []string) error {
	switch {
	case len(args) == 1 && util.IsURL(args[0]):
		url, err := util.RemoveScheme(args[0])
		if err != nil {
			return err
		}
		connectCtx.Network, connectCtx.Address = libconnect.ParseBaseURI(url)
	case len(args) == 1 && !util.IsURL(args[0]):
		configPath, _, _, err := parseAppStr(&cmdCtx, args[0])
		if err != nil {
			return err
		}

		_, instName, _ := strings.Cut(args[0], string(running.InstanceDelimiter))

		if err := readConfigFilePath(configPath, instName); err != nil {
			return err
		}
	case len(args) == 2 && util.IsRegularFile(args[0]):
		if err := readConfigFilePath(args[0], args[1]); err != nil {
			return err
		}
	default:
		return fmt.Errorf("failed to recognize a connect destination, see the command examples")
	}

	if !cmd.Flags().Changed("transport") && (connectCtx.Ssl.KeyFile != "" ||
		connectCtx.Ssl.CertFile != "" || connectCtx.Ssl.CaFile != "") {
		connectCtx.Transport = aeoncmd.TransportSsl
	}

	checkFile := func(path string) bool {
		return path == "" || util.IsRegularFile(path)
	}

	if connectCtx.Transport != aeoncmd.TransportPlain {
		if cmd.Flags().Changed("sslkeyfile") != cmd.Flags().Changed("sslcertfile") {
			return errors.New("files Key and Cert must be specified both")
		}
		fmt.Println("connectCtx.Ssl.KeyFile", connectCtx.Ssl.KeyFile)

		if !checkFile(connectCtx.Ssl.KeyFile) {
			return fmt.Errorf("not valid path to a private SSL key file=%q",
				connectCtx.Ssl.KeyFile)
		}
		if !checkFile(connectCtx.Ssl.CertFile) {
			return fmt.Errorf("not valid path to an SSL certificate file=%q",
				connectCtx.Ssl.CertFile)
		}
		if !checkFile(connectCtx.Ssl.CaFile) {
			return fmt.Errorf("not valid path to trusted certificate authorities (CA) file=%q",
				connectCtx.Ssl.CaFile)
		}
	}
	return nil
}

func aeonHistoryFile() (console.History, error) {
	dir, err := util.GetHomeDir()
	if err != nil {
		return console.History{}, fmt.Errorf("failed to get home directory: %w", err)
	}
	file := filepath.Join(dir, aeonHistoryFileName)
	return console.NewHistory(file, aeonHistoryLines)
}

func internalAeonConnect(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	hist, err := aeonHistoryFile()
	if err != nil {
		return fmt.Errorf("can't open history file: %w", err)
	}
	handler, err := aeon.NewAeonHandler(connectCtx)
	if err != nil {
		return err
	}
	opts := console.ConsoleOpts{
		Handler: handler,
		History: &hist,
		Format:  console.FormatAsTable(),
	}
	c, err := console.NewConsole(opts)
	if err != nil {
		return fmt.Errorf("can't create aeon console: %w", err)
	}
	err = c.Run()
	if err != nil {
		return fmt.Errorf("can't start aeon console: %w", err)
	}
	return nil
}

func readConfigFilePath(configPath string, instance string) error {
	f, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	pb := cluster.NewYamlCollector(f)
	config, err := pb.Collect()
	if err != nil {
		return err
	}

	clusterConfig, err := cluster.MakeClusterConfig(config)
	if err != nil {
		return err
	}

	result := cluster.Instantiate(clusterConfig, instance)

	// Get SSL connection.
	dataSsl := []string{"roles_cfg", "aeon.grpc", "advertise"}
	data, err := result.Get(dataSsl)
	if err != nil {
		return err
	}

	var advertise aeoncmd.Advertise
	err = mapstructure.Decode(data, &advertise)
	if err != nil {
		return err
	}

	if advertise.Uri == "" {
		return errors.New("invalid connection url")
	}

	cleanedURL, err := util.RemoveScheme(advertise.Uri)
	if err != nil {
		return err
	}

	connectCtx.Network, connectCtx.Address = libconnect.ParseBaseURI(cleanedURL)

	if (advertise.Params.Transport != "ssl") && (advertise.Params.Transport != "plain") {
		return errors.New("transport must be ssl or plain")
	}

	if advertise.Params.Transport == "ssl" {
		connectCtx.Transport = aeoncmd.TransportSsl
		configDir := filepath.Dir(configPath)

		if connectCtx.Ssl.CaFile == "" {
			connectCtx.Ssl.CaFile = util.JoinPaths(configDir, advertise.Params.CaFile)
		}

		if connectCtx.Ssl.KeyFile == "" {
			connectCtx.Ssl.KeyFile = util.JoinPaths(configDir, advertise.Params.KeyFile)
		}

		if connectCtx.Ssl.CertFile == "" {
			connectCtx.Ssl.CertFile = util.JoinPaths(configDir, advertise.Params.CertFile)
		}
	}

	return nil
}
