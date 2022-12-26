package create_ctx

import "github.com/tarantool/tt/cli/config"

// CreateCtx contains information for creating applications from templates.
type CreateCtx struct {
	// AppName is application name to create.
	AppName string
	// WorkDir is tt launch working directory.
	WorkDir string
	// DestinationDir is the path where an application will be created.
	DestinationDir string
	// TemplateSearchPaths is a set of path to search for a template.
	TemplateSearchPaths []string
	// TemplateName is a template to use for application creation.
	TemplateName string
	// VarsFromCli template variables definitions provided in command line.
	VarsFromCli []string
	// ForceMode - if flag is set, remove application existing application directory.
	ForceMode bool
	// SilentMode if set, disables user interaction. All invalid format errors fail
	// app creation.
	SilentMode bool
	// VarsFile is a file with variables definitions.
	VarsFile string
	// CliOpts is loaded tt environment config.
	CliOpts *config.CliOpts
}
