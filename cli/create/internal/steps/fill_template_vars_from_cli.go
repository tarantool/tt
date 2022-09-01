package steps

import (
	"fmt"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
)

const varDefFormatError = `Wrong variable definition format: %s
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
type FillTemplateVarsFromCli struct {
}

// Run collects variables passed using command line args.
func (FillTemplateVarsFromCli) Run(createCtx *cmdcontext.CreateCtx,
	templateCtx *TemplateCtx) error {
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
