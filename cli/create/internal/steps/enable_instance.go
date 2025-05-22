package steps

import (
	"io/fs"
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
	templateCtx *app_template.TemplateCtx,
) error {
	if createSymlinkStep.SymlinkDir == "." {
		log.Debug("No need to create a symlink for application in current directory.")
		return nil
	}

	log.Debugf("Creating symlink to %q in %q", templateCtx.TargetAppPath,
		createSymlinkStep.SymlinkDir)
	relativeAppPath, err := filepath.Rel(createSymlinkStep.SymlinkDir,
		templateCtx.TargetAppPath)
	if err != nil {
		return err
	}

	// drwxr-x--- permissions.
	if err = util.CreateDirectory(createSymlinkStep.SymlinkDir, fs.FileMode(0o750)); err != nil {
		return nil
	}

	if err = util.CreateSymlink(relativeAppPath,
		filepath.Join(createSymlinkStep.SymlinkDir, createCtx.AppName),
		createCtx.ForceMode); err != nil {
		log.Warnf(`Failed to enable %q application: %s.
Further actions with %q application may not work correctly. Update symbolic link in %q `+
			`directory, or use -f flag to overwrite it.`,
			createCtx.AppName, err, templateCtx.TargetAppPath, createSymlinkStep.SymlinkDir)
	}

	return nil
}
