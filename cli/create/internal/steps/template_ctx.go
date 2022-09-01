package steps

import (
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/templates/engines"
)

// TemplateCtx contains an information required for application template rendering.
type TemplateCtx struct {
	// AppPath is a path to application directory. Application template will be
	// instantiated in this directory.
	AppPath string
	// Manifest is a loaded template manifest.
	Manifest app_template.TemplateManifest
	// IsManifestPresent is true is a template manifest is loaded. False - otherwise.
	IsManifestPresent bool
	// Vars is a map if variables to be used for template rendering.
	Vars map[string]string
	// Engine is a template engine to use for template rendering.
	Engine engines.TemplateEngine
}

// NewTemplateContext creates new application template context.
func NewTemplateContext() TemplateCtx {
	var ctx TemplateCtx
	ctx.Vars = make(map[string]string)
	ctx.Engine = engines.NewDefaultEngine()
	return ctx
}
