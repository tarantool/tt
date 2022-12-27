package steps

import (
	"path/filepath"

	"github.com/apex/log"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/util"
)

// CreateAppSymlink step creates a symbolic link to the new application in
// instances enabled directory.
type CreateAppSymlink struct {
	SymlinkDir string
}

// Run creates a symbolic link to the new application in instances enabled directory.
func (createSymlinkStep CreateAppSymlink) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx) error {
	if createSymlinkStep.SymlinkDir == "." {
		log.Debug("No need to create a symlink for application in current directory.")
		return nil
	}

	relativeAppPath, err := filepath.Rel(createSymlinkStep.SymlinkDir,
		templateCtx.TargetAppPath)
	if err != nil {
		return err
	}
	if err = util.CreateSymlink(relativeAppPath,
		filepath.Join(createSymlinkStep.SymlinkDir, createCtx.AppName),
		createCtx.ForceMode); err != nil {
		log.Warnf("Failed to enable %s application: %s", createCtx.AppName, err)
	}

	return nil
}
