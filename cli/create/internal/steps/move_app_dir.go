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
		if err = os.RemoveAll(templateCtx.TargetAppPath); err != nil {
			return fmt.Errorf("failed to remove %s: %s", templateCtx.TargetAppPath, err)
		}
	}

	if err := copy.Copy(templateCtx.AppPath, templateCtx.TargetAppPath); err != nil {
		return err
	}

	if err := os.RemoveAll(templateCtx.AppPath); err != nil {
		log.Warnf("Failed to remove temporary directory: %s", err)
	}

	log.Infof("Application '%s' created successfully", createCtx.AppName)

	return nil
}
