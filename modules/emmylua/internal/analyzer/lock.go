package analyzer

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"
)

// lockFileName is the lock of a tt project, beside its manifest.
const lockFileName = "app.manifest.lock"

// lockedRocks is what the lock of a project pins: the closure of every
// product and the development closure.
type lockedRocks struct {
	Lock struct {
		Products map[string]struct {
			Dependencies []lockedRock `toml:"dependencies"`
		} `toml:"products"`
		DevDependencies []lockedRock `toml:"dev_dependencies"`
	} `toml:"lock"`
}

// lockedRock is a rock the lock pins.
type lockedRock struct {
	Name string `toml:"name"`
}

// LockPins reports whether the lock of the project pins the RockName rock,
// so that tt package fetch installs it. A project with no lock, or one tt
// cannot read, pins nothing.
func LockPins(projectDir string) bool {
	data, err := os.ReadFile(filepath.Join(projectDir, lockFileName))
	if err != nil {
		return false
	}

	var lock lockedRocks

	if toml.Unmarshal(data, &lock) != nil {
		return false
	}

	named := func(rock lockedRock) bool { return rock.Name == RockName }

	if slices.ContainsFunc(lock.Lock.DevDependencies, named) {
		return true
	}

	for _, product := range lock.Lock.Products {
		if slices.ContainsFunc(product.Dependencies, named) {
			return true
		}
	}

	return false
}
