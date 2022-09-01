package steps

import (
	"fmt"
	"os"
	"path"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// LoadManifest represents manifest load step.
type LoadManifest struct {
}

// Run loads template manifest. Missing manifest is not an error.
func (LoadManifest) Run(ctx *cmdcontext.CreateCtx, templateCtx *TemplateCtx) error {
	manifestPath := path.Join(templateCtx.AppPath, app_template.DefaultManifestName)

	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			log.Info("There is no manifest in template.")
			templateCtx.IsManifestPresent = false
			return nil
		}
	}

	manifest, err := app_template.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("Failed to load manifest file: %s", err)
	}

	templateCtx.Manifest = manifest
	templateCtx.IsManifestPresent = true

	if err = os.Remove(manifestPath); err != nil {
		return fmt.Errorf("Failed to remove manifest %s: %s", manifestPath, err)
	}

	return nil
}
