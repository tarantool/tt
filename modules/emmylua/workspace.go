package emmylua

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/scope"
)

// errNotRoot reports tt emmylua prepare run below the root of a project.
var errNotRoot = errors.New("not the root of the project")

// workspace is where a command of EmmyLua runs.
type workspace struct {
	// dir is the root of the workspace: the project, or the directory of
	// the command outside every project. The programs check it when no path
	// is given.
	dir string
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
			return workspace{dir: root, project: true}, nil
		}

		if !errors.Is(err, fs.ErrNotExist) {
			return workspace{}, fmt.Errorf("reading the project %s: %w", root, err)
		}

		services.Log().Debug("the project " + root + " has no " + scope.ManifestFileName +
			", so no rocks: EmmyLua runs there as outside a project")
	}

	return workspace{dir: dir, project: false}, nil
}

// projectRoot returns the directory tt emmylua prepare declares EmmyLua in:
// the directory of the command, which must be the root of the project it is
// in, since the project of the SDK edits the manifest there.
func projectRoot(services sdk.Services) (string, error) {
	dir, err := services.Project().Dir()
	if err != nil {
		return "", err
	}

	found, err := findWorkspace(services)
	if err != nil {
		return "", err
	}

	if found.project && found.dir != dir {
		return "", fmt.Errorf("%w: %s is inside the project %s; run tt emmylua prepare there",
			errNotRoot, dir, found.dir)
	}

	return dir, nil
}
