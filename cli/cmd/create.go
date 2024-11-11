package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create"
	"github.com/tarantool/tt/cli/create/builtin_templates"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

var (
	appName            string
	dstPath            string
	forceMode          bool
	nonInteractiveMode bool
	varsFromCli        *[]string
	varsFile           string

	// errNoAppName is returned if -n option was not provided.
	errNoAppName = util.NewArgError(`application name is required: ` +
		`specify it with the --name option.`)
)

// NewCreateCmd creates an application from a template.
func NewCreateCmd() *cobra.Command {
	var createCmd = &cobra.Command{
		Use:   "create <TEMPLATE_NAME> [flags]",
		Short: "Create an application from a template",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalCreateModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("requires template name argument")
			}
			return nil
		},
		ValidArgsFunction: createValidArgsFunction,
		Long: `Create an application from a template.

Built-in templates:
	cartridge: a simple Cartridge application.
	single_instance: Tarantool 3 application with a single instance configuration.
	vshard_cluster: Tarantool 3 vshard cluster application.
	tarantool_db: Tarantool DB application`,
		Example: `
# Create an application app1 from a template.

    $ tt create <template name> --name app1

# Create a Tarantool 3 application with a single instance configuration.

    $ tt create single_instance --name new_app

# Create cartridge_app application in /opt/tt/apps/, force replacing of application directory
# (cartridge_app) if it exists. ` +
			`User interaction is disabled.

    $ tt create cartridge --name cartridge_app -f --non-interactive --dst /opt/tt/apps/

# Create Tarantool 3 vshard cluster.

    $ tt create vshard_cluster --name cluster_app

# Create Tarantool DB application

	$ tt create tarantool_db --name myapp`,
	}

	createCmd.Flags().StringVarP(&appName, "name", "n", "", "Application name")
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

// createValidArgsFunction returns valid templates for `create` command.
func createValidArgsFunction(
	_ *cobra.Command,
	args []string,
	toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	templates := make([]string, 0, len(builtin_templates.Names))

	// Append built-in templates.
	for _, template := range builtin_templates.Names {
		templates = append(templates, template)
	}

	// Append cfg's templates.
	for _, templateDir := range cliOpts.Templates {
		path := templateDir.Path
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			eName := entry.Name()
			ext := filepath.Ext(eName)
			if entry.IsDir() {
				templates = append(templates, eName)
			} else if ext == ".tgz" {
				templates = append(templates, eName[:len(eName)-4])
			} else if ext == ".gz" && filepath.Ext(eName[:len(eName)-3]) == ".tar" {
				templates = append(templates, eName[:len(eName)-7])
			}
		}
	}
	return templates, cobra.ShellCompDirectiveNoFileComp
}

// internalCreateModule is a default create module.
func internalCreateModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}
	if len(appName) == 0 {
		return errNoAppName
	}

	createCtx := create_ctx.CreateCtx{
		AppName:        appName,
		ForceMode:      forceMode,
		SilentMode:     nonInteractiveMode,
		VarsFromCli:    *varsFromCli,
		VarsFile:       varsFile,
		DestinationDir: dstPath,
		CliOpts:        cliOpts,
	}

	if err := create.FillCtx(cliOpts, &createCtx, args); err != nil {
		return err
	}

	return create.Run(cliOpts, &createCtx)
}
