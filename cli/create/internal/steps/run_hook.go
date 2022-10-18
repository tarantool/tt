package steps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/apex/log"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// RunHook represents run hook step.
type RunHook struct {
	HookType string
}

// Run executes template hooks.
func (hook RunHook) Run(ctx *create_ctx.CreateCtx, templateCtx *app_template.TemplateCtx) error {
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

	// Check if hook is present.
	if hookPath == "" {
		return nil
	}

	executablePath := filepath.Join(templateCtx.AppPath, hookPath)
	_, err := os.Stat(executablePath)
	if err != nil {
		return fmt.Errorf("Error access to %s: %s", executablePath, err)
	}
	log.Infof("Executing %s-hook %s", hook.HookType, hookPath)
	if err = exec.Command(executablePath, templateCtx.AppPath).Run(); err != nil {
		return fmt.Errorf("Error executing %s: %s", executablePath, err)
	}
	// Remove pre/post executable.
	if err = os.Remove(executablePath); err != nil {
		log.Errorf("Failed to remove %s: %s", executablePath, err)
	}

	return nil
}
