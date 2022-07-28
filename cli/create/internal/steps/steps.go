package steps

import (
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

type Step interface {
	Run(ctx cmdcontext.CreateCtx, appTemplateCtx *templates.TemplateCtx) error
}
