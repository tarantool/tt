package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/create"
	"github.com/tarantool/tt/cli/modules"
)

var (
	appName            string
	dstPath            string
	forceMode          bool
	nonInteractiveMode bool
	varsFromCli        *[]string
	varsFile           string
)

// NewCreateCmd creates an application from a template.
func NewCreateCmd() *cobra.Command {
	var createCmd = &cobra.Command{
		Use:   "create [TEMPLATE] [flags]",
		Short: "Create an application from a template",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalCreateModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
		Example: `
# Create an application app1 from a template.

    $ tt create <template name> --name app1

# Create cartridge_app application in /opt/tt/apps/, set user_name value,
# force replacing of application directory (cartridge_app) if it exists. ` +
			`User interaction is disabled.

    $ tt create <template name> --name cartridge_app --var user_name=admin -f ` +
			`--non-interactive -dst /opt/tt/apps/`,
	}

	createCmd.Flags().StringVarP(&appName, "name", "n", "", "Application name")
	createCmd.MarkFlagRequired("name")
	createCmd.Flags().BoolVarP(&forceMode, "force", "f", false,
		`Force rewrite application directory if already exists`)
	createCmd.Flags().BoolVarP(&nonInteractiveMode, "non-interactive", "s", false,
		`Non-interactive mode`)

	varsFromCli = createCmd.Flags().StringArray("var", []string{},
		"Variable definition. Usage: --var var_name=value")
	createCmd.Flags().StringVarP(&varsFile, "vars-file", "", "", "Variables definition file path")
	createCmd.Flags().StringVarP(&dstPath, "dst", "d", "",
		"Path to the directory where an application will be created.")

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
	cmdCtx.Create.VarsFile = varsFile
	cmdCtx.Create.DestinationDir = dstPath

	if err = create.FillCtx(cliOpts, cmdCtx, args); err != nil {
		return err
	}

	return create.Run(&cmdCtx.Create)
}
