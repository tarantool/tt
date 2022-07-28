package cmd

import (
	"fmt"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/create"
	"github.com/tarantool/tt/cli/modules"
)

var (
	appName            string
	forceMode          bool
	nonInteractiveMode bool
	varsFromCli        *[]string
)

// NewCreateCmd creates an application from a template.
func NewCreateCmd() *cobra.Command {
	var createCmd = &cobra.Command{
		Use:   "create [TEMPLATE] [flags]",
		Short: "Creates an application.",
		Long: "Creates an application from a template. Default template is 'basic'.\n" +
			"If application name is not specified (--name option), " +
			`template name will be used as application name.`,
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalCreateModule, args)
			if err != nil {
				log.Fatalf(err.Error())
				fmt.Println(err.Error())
			}
		},
	}

	createCmd.Flags().StringVarP(&appName, "name", "", "", `Application name`)
	createCmd.Flags().BoolVarP(&forceMode, "force", "f", false,
		`Force rewrite application directory if already exists.`)
	createCmd.Flags().BoolVarP(&nonInteractiveMode, "non-interactive", "s", false,
		`Non-interactive mode.`)

	varsFromCli = createCmd.Flags().StringArray("var", []string{},
		"Variable definition. Usage: --var var_name=value")

	return createCmd
}

// internalCreateModule is a default create module.
func internalCreateModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	cmdCtx.Create.AppName = appName
	cmdCtx.Create.ForceMode = forceMode
	cmdCtx.Create.SilentMode = nonInteractiveMode
	cmdCtx.Create.VarsFromCli = *varsFromCli

	if err = create.FillCtx(*cliOpts, &cmdCtx.Create, args); err != nil {
		return err
	}

	return create.Run(cmdCtx.Create)
}
