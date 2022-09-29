package steps

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/cmdcontext"
)

// MoveAppDirectory represents temporary application directory move step.
type MoveAppDirectory struct {
}

// Run moves temporary application directory to destination.
func (MoveAppDirectory) Run(createCtx *cmdcontext.CreateCtx, templateCtx *TemplateCtx) error {
	if templateCtx.TargetAppPath == "" {
		return nil
	}

	if _, err := os.Stat(templateCtx.TargetAppPath); err == nil {
		if !createCtx.ForceMode {
			return fmt.Errorf("'%s' already exists.", templateCtx.TargetAppPath)
		}
		if err = os.RemoveAll(templateCtx.TargetAppPath); err != nil {
			return fmt.Errorf("Failed to remove %s: %s", templateCtx.TargetAppPath, err)
		}
	}

	if err := copy.Copy(templateCtx.AppPath, templateCtx.TargetAppPath); err != nil {
		return err
	}

	if err := os.RemoveAll(templateCtx.AppPath); err != nil {
		log.Warnf("Failed to remove temporary directory: %s", err)
	}

	return nil
}
