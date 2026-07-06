package rocks

import (
	"errors"
	"fmt"

	luarocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/rockspec"
)

// ErrNoLocalRockspec reports that a path dependency's directory ships no
// rockspec. It is not a failure: the dependency is a leaf pinned by path and
// content hash, without a version or transitive closure. Callers match it with
// errors.Is.
var ErrNoLocalRockspec = errors.New("no rockspec in path dependency directory")

// LocalMetadata evaluates the rockspec of a path dependency: the single
// top-level *.rockspec under dir, evaluated with the runtime platforms merged
// in, so its declared version and transitive dependencies are visible to the
// resolver. Unlike Metadata it fetches nothing - the source is a local
// directory already on disk.
//
// When the directory ships no rockspec it returns ErrNoLocalRockspec, which the
// caller treats as a leaf dependency rather than a failure.
func (a *Adapter) LocalMetadata(dir string) (*luarocks.Rockspec, error) {
	specPath, err := findRockspec(dir)
	if err != nil {
		// No rockspec in the directory is a leaf, not a failure. Any other
		// failure (e.g. the directory is unreadable) is real.
		if errors.Is(err, errNoRockspec) {
			return nil, ErrNoLocalRockspec
		}

		return nil, err
	}

	spec, err := rockspec.Eval(specPath, a.cfg.Rockspec)
	if err != nil {
		return nil, fmt.Errorf("rocks: eval %s: %w", specPath, err)
	}

	rockspec.MergePlatforms(spec, rockspec.RuntimePlatforms())

	return spec, nil
}
