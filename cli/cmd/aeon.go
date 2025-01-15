package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	aeon "github.com/tarantool/tt/cli/aeon"
	aeoncmd "github.com/tarantool/tt/cli/aeon/cmd"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/console"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
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
		Use:   "connect URI",
		Short: "Connect to the aeon instance",
		Long: `Connect to the aeon instance.
tt aeon connect localhost:50051
tt aeon connect unix://<socket-path>`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			err := aeonConnectValidateArgs(cmd, args)
			util.HandleCmdErr(cmd, err)
			return err
		},
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalAeonConnect, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
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
	connectCtx.Network, connectCtx.Address = libconnect.ParseBaseURI(args[0])

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
