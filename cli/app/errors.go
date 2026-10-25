package app

import (
	"errors"

	"github.com/tarantool/tt/sdk"
)

// The failures the resolver tells apart. An error about a target, a flag, a
// missing application or instance, or a pair that does not fit together
// wraps one of them, so that a caller matches it with errors.Is rather than
// by the message, which names the paths and the names involved. A
// description that does not parse wraps instances.ErrInvalid instead, and a
// file or a config storage that cannot be read wraps what failed.
var (
	// ErrInvalidTarget is a target outside the grammar of instance-level
	// commands, such as a bare application name, or a name that cannot be
	// an application or an instance.
	ErrInvalidTarget = errors.New("invalid target")
	// ErrUnknownScope is a --scope value other than project, user and
	// system.
	ErrUnknownScope = errors.New("unknown scope")
	// ErrInvalidTemplate is a --template value that cannot name a template
	// directory.
	ErrInvalidTemplate = errors.New("invalid template name")
	// ErrNoProject is a command that needs the project it runs in, without a
	// target or with --scope project, run outside of any project.
	ErrNoProject = errors.New("not inside a project")
	// ErrTT2Environment is wrapped, next to ErrNoProject or ErrAppNotFound,
	// when the directory the command runs in is a tt2 environment.
	ErrTT2Environment = errors.New("this is a tt2 environment")
	// ErrInvalidManifest is a project manifest that does not parse or names
	// no usable application.
	ErrInvalidManifest = errors.New("invalid project manifest")
	// ErrAppNotFound is an application missing from the scope it was looked
	// for in.
	ErrAppNotFound = errors.New("application not found")
	// ErrNoPair is an application without the pair it is to run from.
	ErrNoPair = errors.New("no instances description")
	// ErrIncompletePair is a pair with one of its two files missing.
	ErrIncompletePair = errors.New("incomplete pair")
	// ErrTemplateConflict is --template given for an application that runs
	// from the pair of the host.
	ErrTemplateConflict = errors.New("--template conflicts with the host pair")
	// ErrUnsupportedField is a field of an instance record that an instance
	// of its kind cannot honour.
	ErrUnsupportedField = errors.New("unsupported field")
	// ErrTopologyMismatch is a Tarantool instance of the description that the
	// cluster config does not declare.
	ErrTopologyMismatch = errors.New("instances missing from the cluster topology")
	// ErrInstanceNotFound is a target naming an instance the description
	// does not declare.
	ErrInstanceNotFound = errors.New("instance not found")
	// ErrNoInstances is a target that selects every instance of a
	// description that declares none.
	ErrNoInstances = errors.New("no instances")
)

var (
	// errNotAbsolute is a pinned application directory given as a relative
	// path, which would mean another directory from another working
	// directory.
	errNotAbsolute = errors.New("is not an absolute path")
	// errPinnedWithoutScope is a pinned application directory given without
	// the scope it belongs to, which says where its root is.
	errPinnedWithoutScope = errors.New("is pinned without its scope")
)

// failf wraps a formatted error as a request that cannot be carried out as
// given (sdk.ExitFailure), the code of every failure here: the fix is a
// target, a flag or a file on the user's side. A failure of the system
// underneath, such as a refused read or an unreachable etcd, still ends tt
// with sdk.ExitSystem, because sdk.ExitFailure does not hide what it wraps.
func failf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitFailure, format, args...)
}
