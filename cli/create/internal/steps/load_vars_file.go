package steps

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// LoadVarsFile represents variables file load step.
type LoadVarsFile struct{}

// Run collects variables passed using command line args.
func (LoadVarsFile) Run(ctx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	if ctx.VarsFile == "" { // Skip if no file specified.
		return nil
	}

	varsDefFileFullPath := filepath.Join(templateCtx.AppPath, ctx.VarsFile)
	_, err := os.Stat(varsDefFileFullPath)
	if err != nil {
		return fmt.Errorf("vars file loading error: %s", err)
	}

	varsFile, err := os.Open(varsDefFileFullPath)
	if err != nil {
		return fmt.Errorf("vars file loading error: %s", err)
	}
	defer varsFile.Close()

	scanner := bufio.NewScanner(varsFile)
	for scanner.Scan() {
		varDef, err := parseVarDefinition(scanner.Text())
		if err != nil {
			return fmt.Errorf("failed to load vars from %s: %s", varsDefFileFullPath, err)
		}
		log.Debugf("Setting var from vars file: %s = %s", varDef.name, varDef.value)
		templateCtx.Vars[varDef.name] = varDef.value
	}

	if err = scanner.Err(); err != nil {
		return err
	}

	return nil
}
