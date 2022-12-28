package app_template

import "github.com/tarantool/tt/cli/templates"

// TemplateCtx contains an information required for application template rendering.
type TemplateCtx struct {
	// AppPath is a path to application directory. Application template will be
	// instantiated in this directory.
	AppPath string
	// TargetAppPath is a path directory where an application to be moved to
	// after instantiation.
	TargetAppPath string
	// Manifest is a loaded template manifest.
	Manifest TemplateManifest
	// IsManifestPresent is true is a template manifest is loaded. False - otherwise.
	IsManifestPresent bool
	// Vars is a map if variables to be used for template rendering.
	Vars map[string]string
	// Engine is a template engine to use for template rendering.
	Engine templates.TemplateEngine
}

// NewTemplateContext creates new application template context.
func NewTemplateContext() TemplateCtx {
	var ctx TemplateCtx
	ctx.Vars = make(map[string]string)
	ctx.Engine = templates.NewDefaultEngine()
	return ctx
}
