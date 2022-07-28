package steps

import (
	"fmt"
	"os"
	"path"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

type LoadManifest struct {
}

// Run loads template manifest. Missing manifets is not an error.
func (LoadManifest) Run(ctx cmdcontext.CreateCtx, templateCtx *templates.TemplateCtx) error {
	manifestPath := path.Join(templateCtx.AppPath, "MANIFEST.yaml")

	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			log.Info("There is no manifest in template.")
			templateCtx.IsManifestPresent = false
			return nil
		}
	}

	manifest, err := templates.LoadManifest(manifestPath)
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
