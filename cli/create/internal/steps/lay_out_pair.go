package steps

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/v3/log"
	"github.com/tarantool/tt/sdk/v3/scope"

	"github.com/tarantool/tt/v3/cli/app"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
)

// pairDirPerm is the mode of the template directory the pair is moved to.
const pairDirPerm fs.FileMode = 0o755

var (
	errPairConflict     = errors.New("the template has its pair in two places")
	errPairInSeveralDir = errors.New("the template has instances.yaml in several directories")
	errNotAProject      = errors.New("makes no project")
)

// LayOutPair represents the step that puts the pair of a template where a
// project keeps it, in .templates/default/, as the built-in templates lay it
// out.
type LayOutPair struct{}

// Run moves the pair of the instantiated template into
// .templates/default/ when the template keeps it anywhere else.
//
// A project is a directory holding app.manifest.toml or the pair of a
// template under .templates/<name>/; a directory with instances.yaml and
// config.yaml at its root is none, and tt create must not make one. The
// paths of the pair are relative to the project directory wherever the pair
// lies, so moving it changes nothing of what it says.
//
// The pair is the instances.yaml of the root, or of the one directory below
// it that holds one, with the config.yaml beside it; a config.yaml of the
// root alone is the pair of a template of the config alone. A config.yaml of
// another directory without an instances.yaml beside it is left where it is:
// it may be anything. Hidden directories are not looked into. A template
// that already keeps a pair in .templates/ lays it out itself and is left
// as it is, a config.yaml of its root included, which is then a file of the
// application. An instances.yaml in more than one directory is an error:
// which one is the pair is not tt's to guess.
func (LayOutPair) Run(_ *create_ctx.CreateCtx, templateCtx *app_template.TemplateCtx) error {
	laidOut, err := hasTemplatePair(templateCtx.AppPath)
	if err != nil || laidOut {
		return err
	}

	source, err := pairDir(templateCtx.AppPath)
	if err != nil || source == "" {
		return err
	}

	target := filepath.Join(templateCtx.AppPath, scope.TemplatesDirName, app.DefaultTemplate)

	for _, name := range []string{scope.InstancesFileName, scope.ConfigFileName} {
		from := filepath.Join(source, name)
		if !isFile(from) {
			continue
		}

		err = movePairFile(templateCtx.AppPath, from, filepath.Join(target, name))
		if err != nil {
			return err
		}
	}

	if source != templateCtx.AppPath {
		// A directory that held the pair alone is left empty: it goes too.
		_ = os.Remove(source)
	}

	return nil
}

// hasTemplatePair reports whether a template directory under .templates/ of
// appPath holds a file of a pair.
func hasTemplatePair(appPath string) (bool, error) {
	templatesDir := filepath.Join(appPath, scope.TemplatesDirName)

	templates, err := os.ReadDir(templatesDir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("looking for the pair of the template: %w", err)
	}

	for _, template := range templates {
		for _, name := range []string{scope.InstancesFileName, scope.ConfigFileName} {
			if isFile(filepath.Join(templatesDir, template.Name(), name)) {
				return true, nil
			}
		}
	}

	return false, nil
}

// isFile reports whether path is there and is no directory.
func isFile(path string) bool {
	info, err := os.Lstat(path)

	return err == nil && !info.IsDir()
}

// movePairFile moves the pair file source to target, creating its directory.
func movePairFile(appPath, source, target string) error {
	relFrom, _ := filepath.Rel(appPath, source)
	relTo, _ := filepath.Rel(appPath, target)

	_, err := os.Lstat(target)
	if err == nil {
		return fmt.Errorf("%w: %s and %s; keep one of them", errPairConflict, relFrom, relTo)
	}

	err = os.MkdirAll(filepath.Dir(target), pairDirPerm)
	if err != nil {
		return fmt.Errorf("laying out the pair of the template: %w", err)
	}

	err = os.Rename(source, target)
	if err != nil {
		return fmt.Errorf("laying out the pair of the template: %w", err)
	}

	log.Debugf("Moved %s to %s", relFrom, relTo)

	return nil
}

// pairDir returns the directory of appPath the pair of the template is in,
// or "" when there is none.
func pairDir(appPath string) (string, error) {
	var dirs []string

	err := filepath.WalkDir(appPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if path != appPath && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}

			return nil
		}

		if entry.Name() == scope.InstancesFileName {
			dirs = append(dirs, filepath.Dir(path))
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("looking for the pair of the template: %w", err)
	}

	switch len(dirs) {
	case 0:
		if isFile(filepath.Join(appPath, scope.ConfigFileName)) {
			return appPath, nil
		}

		return "", nil
	case 1:
		return dirs[0], nil
	default:
		names := make([]string, 0, len(dirs))
		for _, dir := range dirs {
			rel, _ := filepath.Rel(appPath, dir)

			names = append(names, filepath.Join(rel, scope.InstancesFileName))
		}

		slices.Sort(names)

		return "", fmt.Errorf("%w: %s; keep one, or put each pair in %s/<name>/",
			errPairInSeveralDir, strings.Join(names, ", "), scope.TemplatesDirName)
	}
}

// CheckProject represents the step that checks that the instantiated
// template is a project.
type CheckProject struct{}

// Run refuses an application tt would not take for a project: one with no
// app.manifest.toml and no pair in .templates/<name>/, which every command
// working on a project would refuse as "not inside a project". It runs
// before the application is moved to its destination, so a refused one
// leaves nothing behind.
func (CheckProject) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	found, err := scope.HasProjectMarker(templateCtx.AppPath)
	if err != nil {
		return fmt.Errorf("checking that the application is a project: %w", err)
	}

	if found {
		return nil
	}

	hint := ""
	if templateCtx.IsManifestPresent && len(templateCtx.Manifest.Include) > 0 {
		hint = "; the include list of MANIFEST.yaml keeps only the files it names, " +
			"so name them there too"
	}

	return fmt.Errorf("template %s %w: it writes neither %s nor an %s or a %s, "+
		"one of which a project needs; add one to the template%s",
		createCtx.TemplateName, errNotAProject, projectManifestName,
		scope.InstancesFileName, scope.ConfigFileName, hint)
}
