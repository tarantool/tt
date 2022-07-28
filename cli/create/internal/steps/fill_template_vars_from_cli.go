package steps

import (
	"fmt"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

const formatError = `Wrong variable definition format: %s
Usage: --var "var-name=value"`

type FillTemplateVarsFromCli struct {
}

// Run collects variables passed using command line args.
func (FillTemplateVarsFromCli) Run(ctx cmdcontext.CreateCtx,
	templateCtx *templates.TemplateCtx) error {
	for _, varDefiniton := range ctx.VarsFromCli {
		varDefiniton = strings.TrimSpace(varDefiniton)
		varName, value, found := strings.Cut(varDefiniton, "=")
		if !found || varName == "" || value == "" {
			return fmt.Errorf(formatError, varDefiniton)
		}
		log.Debugf("Setting var from CLI: %s = %s", varName, value)
		templateCtx.Vars[varName] = value
	}
	return nil
}
