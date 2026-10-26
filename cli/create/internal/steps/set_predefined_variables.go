package steps

import (
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
)

// SetPredefinedVariables represents a step for setting pre-defined variables.
type SetPredefinedVariables struct{}

// Run sets predefined variables values.
func (SetPredefinedVariables) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	templateCtx.Vars["name"] = createCtx.AppName

	return nil
}
