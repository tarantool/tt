package build

import (
	"errors"
)

// File names the build reads and writes in the project root.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
)

// The build carries its exit codes in *sdk.ExitError: a usage or state error
// (stale lock under --locked, version.lua collision, bad manifest,
// unresolvable dependency) is sdk.ExitFailure, and a component build backend
// or hook that fails is sdk.ExitSystem. Classifying the failures that carry no
// code - a rock server that cannot be reached, a filesystem that refuses a
// write - is done once for every manifest command, by cli/exitcode.

var (
	// errLockStale reports that the lock no longer matches the manifest while
	// --locked forbids rewriting it. It carries exit code 1.
	errLockStale = errors.New("lock is out of date")
	// errNoLock reports that an operation requiring a lock found none:
	// tt package fetch (which never resolves) or a --locked build.
	errNoLock = errors.New("no lock file")
	// errVersionLuaCollision reports that a component laid a file at the path
	// the build generates version.lua into. It carries exit code 1.
	errVersionLuaCollision = errors.New("version.lua collision")
	// errUnknownProduct reports that --product named a product the manifest
	// does not define.
	errUnknownProduct = errors.New("unknown product")
	// errNoDefaultProduct reports that no product could be selected: several
	// products exist and none is marked default (should be caught by Validate,
	// but the build fails safe).
	errNoDefaultProduct = errors.New("no default product")
	// errUnknownComponent reports that a component argument names a component
	// the selected product does not build.
	errUnknownComponent = errors.New("unknown component")
	// errUnknownSource reports a lock dependency whose source is neither
	// registry nor path.
	errUnknownSource = errors.New("unknown dependency source")
	// errAmbiguousRockspec reports a path dependency directory that ships more
	// than one rockspec, so which one to build is ambiguous.
	errAmbiguousRockspec = errors.New("multiple rockspecs in path dependency")
)
