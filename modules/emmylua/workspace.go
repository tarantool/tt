package emmylua

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/modules/emmylua/internal/analyzer"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/scope"
)

var (
	// errNotRoot reports tt emmylua prepare run below the root of a project.
	errNotRoot = errors.New("not the root of the project")
	// errNotADirectory reports a --library that is not a directory.
	errNotADirectory = errors.New("not a directory")
)

// workspace is where a command of EmmyLua runs.
type workspace struct {
	// dir is the root of the workspace: the project, or the directory of
	// the command outside every project. The programs check it when no path
	// is given.
	dir string
	// workDir is the directory of the command (Project().Dir()), which the
	// relative paths of its flags are taken from.
	workDir string
	// project tells a project from a directory outside every project.
	project bool
}

// findWorkspace returns the workspace of a command: the project the
// directory of the command (Project().Dir()) is in, as sdk/scope finds it -
// the rule the project commands of tt follow - provided that it holds the
// manifest, and the directory of the command itself otherwise. A project of
// instance templates alone has no rocks to run EmmyLua from, and is not
// taken for one.
func findWorkspace(services sdk.Services) (workspace, error) {
	dir, err := services.Project().Dir()
	if err != nil {
		return workspace{}, err
	}

	root, found, err := scope.FindProject(dir)
	if err != nil {
		return workspace{}, fmt.Errorf("finding the project of %s: %w", dir, err)
	}

	if found {
		_, err = os.Stat(filepath.Join(root, scope.ManifestFileName))
		if err == nil {
			return workspace{dir: root, workDir: dir, project: true}, nil
		}

		if !errors.Is(err, fs.ErrNotExist) {
			return workspace{}, fmt.Errorf("reading the project %s: %w", root, err)
		}

		services.Log().Debug("the project " + root + " has no " + scope.ManifestFileName +
			", so no rocks: EmmyLua runs there as outside a project")
	}

	return workspace{dir: dir, workDir: dir, project: false}, nil
}

// projectRoot returns the directory tt emmylua prepare declares EmmyLua in:
// the directory of the command, which must be the root of the project it is
// in, since the project of the SDK edits the manifest there. Outside every
// project there is nothing to declare it in, and the programs the other
// commands run come from the user cache without it.
func projectRoot(services sdk.Services) (string, error) {
	dir, err := services.Project().Dir()
	if err != nil {
		return "", err
	}

	found, err := findWorkspace(services)
	if err != nil {
		return "", err
	}

	if !found.project {
		return "", fmt.Errorf("%w: no %s in %s or any directory above it; create one "+
			"with tt new, or run tt emmylua check, doc, fmt and ls as they are: outside a "+
			"project they fetch EmmyLua into the user cache themselves",
			analyzer.ErrNoManifest, scope.ManifestFileName, dir)
	}

	if found.dir != dir {
		return "", fmt.Errorf("%w: %s is inside the project %s; run tt emmylua prepare there",
			errNotRoot, dir, found.dir)
	}

	return dir, nil
}
