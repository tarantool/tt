package steps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// projectManifestName is the manifest of a project, which names the
// application the instance commands address.
const projectManifestName = "app.manifest.toml"

var (
	errNotPackageName     = errors.New("not a package name")
	errUnreadableManifest = errors.New("cannot read")
)

// CheckPackageName represents the step that checks the package name of the
// project manifest a template writes.
type CheckPackageName struct{}

// Run refuses the application when the template leaves a project manifest
// whose [package].name is not a package name.
//
// The name is what the instance commands, tt package and tt binary know the
// project by, and a manifest whose name does not fit breaks all of them: the
// project would be created only to fail on its first start. A template
// derives the name from --name its own way - vshard_cluster replaces "_" with
// "-" - so the name is checked as the template wrote it, once the template is
// instantiated and its hooks have run, and before anything is moved to the
// destination. A template without a manifest is left alone.
func (CheckPackageName) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	manifestPath := filepath.Join(templateCtx.AppPath, projectManifestName)

	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("%w %s the template writes: %w", errUnreadableManifest,
			projectManifestName, err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return fmt.Errorf("%w %s the template writes: %w", errUnreadableManifest,
			projectManifestName, err)
	}

	name := man.Package.Name
	if !manifest.ValidPackageName(name) {
		return fmt.Errorf("application name %q gives package.name %q in %s, which is %w: "+
			"use lowercase letters, digits and dashes, starting with a letter, "+
			"and not a reserved name", createCtx.AppName, name, projectManifestName,
			errNotPackageName)
	}

	return nil
}
