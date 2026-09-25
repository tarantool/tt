package steps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
)

var (
	errInvalidHookType = errors.New("invalid hook type ")
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
		return fmt.Errorf("%w%s", errInvalidHookType, hook.HookType)
	}

	// Check if hook is present.
	if hookPath == "" {
		return nil
	}

	executablePath := filepath.Join(templateCtx.AppPath, hookPath)

	_, err := os.Stat(executablePath)
	if err != nil {
		return fmt.Errorf("error access to %s: %w", executablePath, err)
	}

	log.Infof("Executing %s-hook %s", hook.HookType, hookPath)

	err = exec.CommandContext(context.Background(), executablePath, templateCtx.AppPath).Run()
	if err != nil {
		return fmt.Errorf("error executing %s: %w", executablePath, err)
	}

	// Remove pre/post executable.
	err = os.Remove(executablePath)
	if err != nil {
		log.Errorf("failed to remove %s: %s", executablePath, err)
	}

	return nil
}
