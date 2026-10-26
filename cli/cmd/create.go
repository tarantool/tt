package cmd

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/create"
	"github.com/tarantool/tt/v3/cli/create/builtin_templates"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errRequiresTemplateNameArgument = errors.New("requires template name argument")
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
	createCmd := &cobra.Command{
		Use:   "create <TEMPLATE_NAME> [flags]",
		Short: "Create an application from a template",
		RunE:  internalRunE(internalCreateModule),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errRequiresTemplateNameArgument
			}

			return nil
		},
		ValidArgsFunction: createValidArgsFunction,
		Long: `Create an application from a template.

Built-in templates:
	single_instance: Tarantool 3 application with a single instance configuration.
	cluster: Tarantool 3 cluster application.
	vshard_cluster: Tarantool 3 vshard cluster application.
	config_storage: Tarantool 3 cluster configuration storage.

A built-in template creates a project: the code of the application in its
directory, and the instances description and the cluster config it runs from
in .templates/default/.

A template of your own is given by its path: a directory, or a .tgz or
.tar.gz archive, which may be named without its extension. A path is told
from the name of a built-in template by a slash in it, so a template in the
current directory is given as ./<name>.`,
		Example: `
# Create an application app1 from a template.

    $ tt create <template name> --name app1

# Create an application app1 from the template in the templates/basic
# directory.

    $ tt create ./templates/basic --name app1

# Create a Tarantool 3 application with a single instance configuration.

    $ tt create single_instance --name new_app

# Create Tarantool 3 vshard cluster.

    $ tt create vshard_cluster --name cluster_app

# Create Tarantool 3 cluster configuration storage.

    $ tt create config_storage --name cs_app`,
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
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}

	// A template of the user's own is given by its path: the shell completes
	// it once it is typed as one.
	if strings.ContainsRune(toComplete, filepath.Separator) {
		return nil, cobra.ShellCompDirectiveDefault
	}

	templates := make([]string, 0, len(builtin_templates.Names))

	// Append built-in templates.
	for _, template := range builtin_templates.Names {
		templates = append(templates, template)
	}

	return templates, cobra.ShellCompDirectiveNoFileComp
}

// internalCreateModule is a default create module.
func internalCreateModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(appName) == 0 {
		return errNoAppName
	}

	// FillCtx fills the working directory.
	createCtx := create_ctx.CreateCtx{
		AppName:        appName,
		WorkDir:        "",
		TemplateName:   args[0],
		ForceMode:      forceMode,
		SilentMode:     nonInteractiveMode,
		VarsFromCli:    *varsFromCli,
		VarsFile:       varsFile,
		DestinationDir: dstPath,
	}

	err := create.FillCtx(&createCtx)
	if err != nil {
		return err
	}

	return create.Run(&createCtx)
}
