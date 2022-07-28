package templates

import "github.com/tarantool/tt/cli/create/internal/templates/engines"

type TemplateCtx struct {
	AppPath           string
	Manifest          TemplateManifest
	IsManifestPresent bool
	Vars              map[string]string
	Engine            engines.TemplateEngine
}

func NewTemplateContext() TemplateCtx {
	var ctx TemplateCtx
	ctx.Vars = make(map[string]string)
	ctx.Engine = engines.GoTextEngine{}
	return ctx
}
