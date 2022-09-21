package steps

import (
	"github.com/tarantool/tt/cli/cmdcontext"
)

// SetPredefinedVariables represents a step for setting pre-defined variables.
type SetPredefinedVariables struct {
}

// Run sets predefined variables values.
func (SetPredefinedVariables) Run(createCtx *cmdcontext.CreateCtx, templateCtx *TemplateCtx) error {
	templateCtx.Vars["name"] = createCtx.AppName
	return nil
}
