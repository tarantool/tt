package steps

import (
	"errors"
	"fmt"
	"os"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/sdk/log"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
)

var (
	errAlreadyExists = errors.New("already exists")
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

	_, err := os.Stat(templateCtx.TargetAppPath)
	if err == nil {
		if !createCtx.ForceMode {
			return fmt.Errorf("'%s' %w", templateCtx.TargetAppPath, errAlreadyExists)
		}

		err = os.RemoveAll(templateCtx.TargetAppPath)
		if err != nil {
			return fmt.Errorf("failed to remove %s: %w", templateCtx.TargetAppPath, err)
		}
	}

	err = copy.Copy(templateCtx.AppPath, templateCtx.TargetAppPath)
	if err != nil {
		return fmt.Errorf("failed to copy the application to %s: %w",
			templateCtx.TargetAppPath, err)
	}

	err = os.RemoveAll(templateCtx.AppPath)
	if err != nil {
		log.Warnf("Failed to remove temporary directory: %s", err)
	}

	log.Infof("Application '%s' created successfully", createCtx.AppName)

	return nil
}
