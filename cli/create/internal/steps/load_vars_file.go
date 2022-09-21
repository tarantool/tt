package steps

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
)

// LoadVarsFile represents variables file load step.
type LoadVarsFile struct {
}

// Run collects variables passed using command line args.
func (LoadVarsFile) Run(ctx *cmdcontext.CreateCtx,
	templateCtx *TemplateCtx) error {
	if ctx.VarsFile == "" { // Skip if no file specified.
		return nil
	}

	varsDefFileFullPath := filepath.Join(templateCtx.AppPath, ctx.VarsFile)
	_, err := os.Stat(varsDefFileFullPath)
	if err != nil {
		return fmt.Errorf("Vars file loading error: %s", err)
	}

	varsFile, err := os.Open(varsDefFileFullPath)
	if err != nil {
		return fmt.Errorf("Vars file loading error: %s", err)
	}
	defer varsFile.Close()

	scanner := bufio.NewScanner(varsFile)
	for scanner.Scan() {
		varDef, err := parseVarDefinition(scanner.Text())
		if err != nil {
			return fmt.Errorf("Failed to load vars from %s: %s", varsDefFileFullPath, err)
		}
		log.Debugf("Setting var from vars file: %s = %s", varDef.name, varDef.value)
		templateCtx.Vars[varDef.name] = varDef.value
	}

	if err = scanner.Err(); err != nil {
		return err
	}

	return nil
}
