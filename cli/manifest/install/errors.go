package install

import (
	"errors"

	"github.com/tarantool/tt/sdk"
)

// Names install reads from inside a .tt archive. They mirror the reserved set
// tt writes at pack time.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
	versionFileName  = "VERSION"
	runtimeDirName   = "_runtime"
	// templatesDirName holds the package's instance templates. Unlike the
	// metadata files it is laid into the install-root as is: it is how the
	// installed application describes the instances it runs.
	templatesDirName = ".templates"
)

var (
	// errSignedIntoProject reports a signed install into the project scope,
	// whose install root the project shares with every guest package: the
	// hashes files of one would lay over those of another.
	errSignedIntoProject = errors.New(
		"a signed archive is installed into the user or the system scope only")
	// errSignedWithoutDeps reports a signed install of an archive packed
	// --without-deps: what the install fetches from the registry is not
	// signed.
	errSignedWithoutDeps = errors.New(
		"an archive packed --without-deps is not signed: install it without " +
			"--integrity-check")
	// errRuntimeNotSigned reports a runtime component that cannot be checked
	// with the key a signed install was given.
	errRuntimeNotSigned = errors.New("does not match the signed runtime of the archive")
	// errNameCollision reports a package already installed in the scope while
	// neither --force nor --upgrade was given.
	errNameCollision = errors.New("package already installed")
	// errLockStale reports that the archive's lock no longer matches its manifest
	// while --locked forbids proceeding.
	errLockStale = errors.New("lock is out of date")
	// errTemplatesExist reports a project install-root that already has a
	// .templates/ the archive's own would be laid over.
	errTemplatesExist = errors.New("install root already has .templates/")
	// errRuntimeMismatch reports a bundled runtime whose version conflicts with
	// the one the project's primary package (or an earlier install) already
	// fixed.
	errRuntimeMismatch = errors.New("bundled runtime is incompatible")
	// errIncompatibleDeps reports a shared dependency no locked version can
	// satisfy across all packages that require it.
	errIncompatibleDeps = errors.New("incompatible shared dependency")
	// errBadArchive reports a .tt archive that cannot be read as tar+zstd or that
	// is missing a tt-owned member.
	errBadArchive = errors.New("invalid archive")
	// errUnsafePath reports an archive entry whose path escapes the destination
	// directory (a tar-slip attempt).
	errUnsafePath = errors.New("archive entry escapes destination")
	// errPartialInstall reports a multi-archive run where some archives
	// installed and some failed; it carries exit code 3.
	errPartialInstall = errors.New("some archives failed to install")
)

// stateErrorf wraps a formatted error as a usage or state failure
// (sdk.ExitFailure): a name collision without --force/--upgrade, an
// incompatible runtime, an application name or version the user and system
// scopes cannot take, running instances in the way of --force, a stale lock
// under --locked, an unreconcilable shared dependency.
func stateErrorf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitFailure, format, args...)
}

// partialErrorf wraps a formatted error as a multi-archive run where some
// targets installed and some failed (sdk.ExitPartial).
func partialErrorf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitPartial, format, args...)
}
