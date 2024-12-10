package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	aeon "github.com/tarantool/tt/cli/aeon/cmd"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
	libconnect "github.com/tarantool/tt/lib/connect"
)

var aeonConnectCtx = aeon.ConnectCtx{
	TransportMode: aeon.TransportPlain,
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
	aeonCmd.Flags().StringVar(&aeonConnectCtx.Ssl.KeyFile, "sslkeyfile", "",
		"path to a private SSL key file")
	aeonCmd.Flags().StringVar(&aeonConnectCtx.Ssl.CertFile, "sslcertfile", "",
		"path to a SSL certificate file")
	aeonCmd.Flags().StringVar(&aeonConnectCtx.Ssl.CaFile, "sslcafile", "",
		"path to a trusted certificate authorities (CA) file")

	aeonCmd.Flags().Var(&aeonConnectCtx.TransportMode, "transport",
		fmt.Sprintf("allowed %s", aeon.ListValidTransports()))
	aeonCmd.RegisterFlagCompletionFunc("transport", aeonTransportCompletion)

	return aeonCmd
}

func aeonTransportCompletion(cmd *cobra.Command, args []string, toComplete string) (
	[]string, cobra.ShellCompDirective) {
	suggest := make([]string, 0, len(aeon.ValidTransport))
	for k, v := range aeon.ValidTransport {
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
	if !cmd.Flags().Changed("transport") && (aeonConnectCtx.Ssl.KeyFile != "" ||
		aeonConnectCtx.Ssl.CertFile != "" || aeonConnectCtx.Ssl.CaFile != "") {
		aeonConnectCtx.TransportMode = aeon.TransportSsl
	}

	if aeonConnectCtx.TransportMode != aeon.TransportPlain {
		if !util.IsRegularFile(aeonConnectCtx.Ssl.KeyFile) {
			return fmt.Errorf("not valid path to a private SSL key file=%q",
				aeonConnectCtx.Ssl.KeyFile)
		}
		if !util.IsRegularFile(aeonConnectCtx.Ssl.CertFile) {
			return fmt.Errorf("not valid path to an SSL certificate file=%q",
				aeonConnectCtx.Ssl.CertFile)
		}
		if !util.IsRegularFile(aeonConnectCtx.Ssl.CaFile) {
			return fmt.Errorf("not valid path to trusted certificate authorities (CA) file=%q",
				aeonConnectCtx.Ssl.CaFile)
		}
	}
	return nil
}

func internalAeonConnect(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	return nil
}
