package steps

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// MoveAppDirectory represents temporary application directory move step.
type MoveAppDirectory struct{}

// Run moves temporary application directory to destination.
func (MoveAppDirectory) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	if templateCtx.TargetAppPath == "" {
		return nil
	}

	if _, err := os.Stat(templateCtx.TargetAppPath); err == nil {
		if !createCtx.ForceMode {
			return fmt.Errorf("'%s' already exists", templateCtx.TargetAppPath)
		}

		err = os.RemoveAll(templateCtx.TargetAppPath)
		if err != nil {
			return fmt.Errorf("failed to remove %s: %w", templateCtx.TargetAppPath, err)
		}
	}

	err := copy.Copy(templateCtx.AppPath, templateCtx.TargetAppPath)
	if err != nil {
		return err
	}

	err = os.RemoveAll(templateCtx.AppPath)
	if err != nil {
		log.Warnf("Failed to remove temporary directory: %s", err)
	}

	log.Infof("Application '%s' created successfully", createCtx.AppName)

	return nil
}
