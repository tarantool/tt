package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	aeon "github.com/tarantool/tt/cli/aeon"
	aeon_cmd "github.com/tarantool/tt/cli/aeon/cmd"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/console"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
	libconnect "github.com/tarantool/tt/lib/connect"
)

var connectCtx = aeon_cmd.ConnectCtx{
	Transport: aeon_cmd.TransportPlain,
}

func newAeonConnectCmd() *cobra.Command {
	var aeonCmd = &cobra.Command{
		Use:   "connect URI",
		Short: "Connect to the aeon instance",
		Long: "Connect to the aeon instance.\n\n" +
			libconnect.EnvCredentialsHelp + "\n\n" +
			`tt aeon connect user:pass@localhost:3013`,
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
	}
	aeonCmd.Flags().StringVar(&connectCtx.Ssl.KeyFile, "sslkeyfile", "",
		"path to a private SSL key file")
	aeonCmd.Flags().StringVar(&connectCtx.Ssl.CertFile, "sslcertfile", "",
		"path to a SSL certificate file")
	aeonCmd.Flags().StringVar(&connectCtx.Ssl.CaFile, "sslcafile", "",
		"path to a trusted certificate authorities (CA) file")

	aeonCmd.Flags().Var(&connectCtx.Transport, "transport",
		fmt.Sprintf("allowed %s", aeon_cmd.ListValidTransports()))
	aeonCmd.RegisterFlagCompletionFunc("transport", aeonTransportCompletion)

	return aeonCmd
}

func aeonTransportCompletion(cmd *cobra.Command, args []string, toComplete string) (
	[]string, cobra.ShellCompDirective) {
	suggest := make([]string, 0, len(aeon_cmd.ValidTransport))
	for k, v := range aeon_cmd.ValidTransport {
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
		connectCtx.Transport = aeon_cmd.TransportSsl
	}

	checkFile := func(path string) bool {
		return path == "" || util.IsRegularFile(path)
	}

	if connectCtx.Transport != aeon_cmd.TransportPlain {
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

func internalAeonConnect(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	opts := console.ConsoleOpts{
		Handler: aeon.NewAeonHandler(connectCtx),
		Format:  console.DefaultConsoleFormat(),
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
