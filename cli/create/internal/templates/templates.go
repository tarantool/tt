package templates

import "github.com/tarantool/tt/cli/create/internal/templates/engines"

// TemplateCtx contains an information required for application template rendering.
type TemplateCtx struct {
	// AppPath is a path to application directory. Application template will be
	// instantiated in this directory.
	AppPath string
	// Manifest is a loaded template manifest.
	Manifest TemplateManifest
	// IsManifestPresent is true is a template manifest is loaded. False - otherwise.
	IsManifestPresent bool
	// Vars is a map if variables to be used for template rendering.
	Vars map[string]string
	// Engine is a template engine to use for template rendering.
	Engine engines.TemplateEngine
}

func NewTemplateContext() TemplateCtx {
	var ctx TemplateCtx
	ctx.Vars = make(map[string]string)
	ctx.Engine = engines.GoTextEngine{}
	return ctx
}
