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
	template    string
	varsFromCli *[]string
)

// NewCreateCmd creates an application from a template.
func NewCreateCmd() *cobra.Command {
	var createCmd = &cobra.Command{
		Use:   "create [APP_NAME] [flags]",
		Short: "Creates an application.",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalCreateModule, args)
			if err != nil {
				log.Fatalf(err.Error())
				fmt.Println(err.Error())
			}
		},
	}

	createCmd.Flags().StringVarP(&template, "template", "t", "basic",
		`Application template name
		defaults to basic`)

	varsFromCli = createCmd.Flags().StringArray("var", []string{}, "Variable definition.")

	return createCmd
}

// internalCreateModule is a default create module.
func internalCreateModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	cmdCtx.Create.TemplateName = template
	cmdCtx.Create.VarsFromCli = *varsFromCli

	if err = create.FillCtx(*cliOpts, &cmdCtx.Create, args); err != nil {
		return err
	}

	return create.Run(cmdCtx.Create)
}
