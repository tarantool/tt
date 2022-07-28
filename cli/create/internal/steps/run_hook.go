package steps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

type RunHook struct {
	HookType string
}

// Run executes template hooks.
func (hook RunHook) Run(ctx cmdcontext.CreateCtx, templateCtx *templates.TemplateCtx) error {
	if !templateCtx.IsManifestPresent {
		log.Debug("No manifest. Skipping hook step.")
		return nil
	}

	var hookPath string
	switch hook.HookType {
	case "pre":
		hookPath = templateCtx.Manifest.PreHook
	case "post":
		hookPath = templateCtx.Manifest.PostHook
	default:
		return fmt.Errorf("Invalid hook type %s", hook.HookType)
	}

	executablePath := filepath.Join(templateCtx.AppPath, hookPath)
	_, err := os.Stat(executablePath)
	if err != nil {
		return fmt.Errorf("Error access to %s: %s", executablePath, err)
	}
	log.Infof("Executing %s-hook %s", hook.HookType, hookPath)
	if err := exec.Command(executablePath, templateCtx.AppPath).Run(); err != nil {
		return fmt.Errorf("Error executing %s: %s", executablePath, err)
	}

	return nil
}
