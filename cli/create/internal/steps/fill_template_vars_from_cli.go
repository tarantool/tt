package steps

import (
	"fmt"
	"strings"

	"github.com/apex/log"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

const varDefFormatError = `wrong variable definition format: %s
Format: var-name=value`

func parseVarDefinition(varDefText string) (struct{ name, value string }, error) {
	varDefiniton := strings.TrimSpace(strings.TrimSuffix(varDefText, "\n"))
	varName, value, found := strings.Cut(varDefiniton, "=")
	if !found || varName == "" || value == "" {
		return struct{ name, value string }{}, fmt.Errorf(varDefFormatError, varDefText)
	}
	return struct{ name, value string }{name: varName, value: value}, nil
}

// FillTemplateVarsFromCli represents a step for collecting variables from command line args.
type FillTemplateVarsFromCli struct{}

// Run collects variables passed using command line args.
func (FillTemplateVarsFromCli) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	for _, varDefiniton := range createCtx.VarsFromCli {
		varDef, err := parseVarDefinition(varDefiniton)
		if err != nil {
			return err
		}
		log.Debugf("Setting var from CLI: %s = %s", varDef.name, varDef.value)
		templateCtx.Vars[varDef.name] = varDef.value
	}
	return nil
}
