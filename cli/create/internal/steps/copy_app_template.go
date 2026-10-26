package steps

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/create/builtin_templates"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errTemplateIsNotFound = errors.New("template '")
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

		defer func() {
			_ = inFile.Close()
		}()

		outFile, err := os.OpenFile(filepath.Join(dst, path), os.O_CREATE|os.O_WRONLY, filePerm)
		if err != nil {
			return fmt.Errorf("create %q: %w", path, err)
		}

		defer func() {
			_ = outFile.Close()
		}()

		_, err = io.Copy(outFile, inFile)
		if err != nil {
			return fmt.Errorf("copy %q: %w", path, err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("copy embedded template: %w", err)
	}

	return nil
}

// templateArchiveExts are the extensions of a template packed into an
// archive.
func templateArchiveExts() []string {
	return []string{".tgz", ".tar.gz"}
}

// isTemplatePath reports whether templateName names a template of the user's
// own by its path rather than a built-in template. A path is told from a name
// by a separator in it: ./basic is the directory, basic the built-in template.
func isTemplatePath(templateName string) bool {
	return strings.ContainsRune(templateName, filepath.Separator)
}

// hasArchiveExt reports whether path has the extension of a template archive.
func hasArchiveExt(path string) bool {
	for _, ext := range templateArchiveExts() {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}

	return false
}

// copyTemplateFromPath copies or extracts the template at templatePath, taken
// relative to workDir, to appPath. The path names a directory or an archive;
// an archive may be named without its extension.
func copyTemplateFromPath(templatePath, workDir, appPath string) error {
	if !filepath.IsAbs(templatePath) {
		templatePath = filepath.Join(workDir, templatePath)
	}

	if util.IsDir(templatePath) {
		log.Infof("Using template from %s", templatePath)

		err := copy.Copy(templatePath, appPath)
		if err != nil {
			return fmt.Errorf("template copying failed: %w", err)
		}

		return nil
	}

	archivesToCheck := []string{templatePath}
	for _, ext := range templateArchiveExts() {
		archivesToCheck = append(archivesToCheck, templatePath+ext)
	}

	for _, archivePath := range archivesToCheck {
		if hasArchiveExt(archivePath) && util.IsRegularFile(archivePath) {
			log.Infof("Using template from %s", archivePath)

			return util.ExtractTarGz(archivePath, appPath)
		}
	}

	return fmt.Errorf("%w%s' is not found", errTemplateIsNotFound, templatePath)
}

// Run copies/extracts application template to target application directory.
func (CopyAppTemplate) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	templateName := createCtx.TemplateName

	if isTemplatePath(templateName) {
		return copyTemplateFromPath(templateName, createCtx.WorkDir, templateCtx.AppPath)
	}

	// Search for a template in built-in templates.
	templateDirs, err := builtin_templates.TemplatesFs.ReadDir("templates")
	if err != nil {
		return fmt.Errorf("failed to list built-in templates: %w", err)
	}

	for _, templateDir := range templateDirs {
		if templateName == templateDir.Name() {
			log.Infof("Using built-in '%s' template.", templateName)

			templateFs, err := fs.Sub(builtin_templates.TemplatesFs,
				filepath.Join("templates", templateDir.Name()))
			if err != nil {
				return fmt.Errorf("failed to open built-in template %q: %w", templateName, err)
			}

			return copyEmbedFs(templateFs, templateCtx.AppPath)
		}
	}

	// A template of the user's own used to be found by its name in the
	// directories tt.yaml listed, so the name may be meant as one of them.
	return fmt.Errorf("%w%s' is not found: a template of your own is given by its path, "+
		"such as ./%s", errTemplateIsNotFound, templateName, templateName)
}
