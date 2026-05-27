package steps

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/create/builtin_templates"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/util"
)

// CopyAppTemplate represents template -> app directory copy step.
type CopyAppTemplate struct{}

// copyEmbedFs copies src file system tree to dst. Directories get 0755 and
// files get 0644 — every built-in template uses those modes, and embed.FS
// strips the source modes anyway, so there is nothing else to preserve.
func copyEmbedFs(srcFs fs.FS, dst string) error {
	const (
		dirPerm  fs.FileMode = 0o755
		filePerm fs.FileMode = 0o644
	)
	err := fs.WalkDir(srcFs, ".", func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %q: %w", path, err)
		}
		if dirEntry.IsDir() {
			return util.CreateDirectory(filepath.Join(dst, path), dirPerm)
		}
		inFile, err := srcFs.Open(path)
		if err != nil {
			return fmt.Errorf("open template file %q: %w", path, err)
		}
		defer inFile.Close()

		outFile, err := os.OpenFile(filepath.Join(dst, path), os.O_CREATE|os.O_WRONLY, filePerm)
		if err != nil {
			return fmt.Errorf("create %q: %w", path, err)
		}
		defer outFile.Close()

		if _, err := io.Copy(outFile, inFile); err != nil {
			return fmt.Errorf("copy %q: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("copy embedded template: %w", err)
	}
	return nil
}

// Run copies/extracts application template to target application directory.
func (CopyAppTemplate) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	templateName := createCtx.TemplateName

	// Search for template in template paths.
	for _, templatesLocation := range createCtx.TemplateSearchPaths {
		templatePath := path.Join(templatesLocation, templateName)

		if util.IsDir(templatePath) {
			log.Infof("Using template from %s", templatePath)
			if err := copy.Copy(templatePath, templateCtx.AppPath); err != nil {
				return fmt.Errorf("template copying failed: %s", err)
			}
			return nil
		}

		archivesToCheck := [2]string{
			path.Join(templatesLocation, templateName+".tgz"),
			path.Join(templatesLocation, templateName+".tar.gz"),
		}
		for _, archivePath := range archivesToCheck {
			if util.IsRegularFile(archivePath) {
				log.Infof("Using template from %s", archivePath)
				return util.ExtractTarGz(archivePath, templateCtx.AppPath)
			}
		}
	}

	// Search for a template in built-in templates.
	templateDirs, err := builtin_templates.TemplatesFs.ReadDir("templates")
	if err != nil {
		return err
	}

	for _, templateDir := range templateDirs {
		if templateName == templateDir.Name() {
			log.Infof("Using built-in '%s' template.", templateName)
			templateFs, err := fs.Sub(builtin_templates.TemplatesFs,
				filepath.Join("templates", templateDir.Name()))
			if err != nil {
				return err
			}
			return copyEmbedFs(templateFs, templateCtx.AppPath) //nolint:wrapcheck // already wraps.
		}
	}

	return fmt.Errorf("template '%s' is not found", templateName)
}
