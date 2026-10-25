// Package tarantool chooses the tarantool executable an application runs on.
//
// tt run and the instance commands both start Tarantool for an application,
// and they must start the same one: an application that works under tt run
// and fails under tt instance start has two runtimes, and nobody can tell
// which of them it was tested on. The rule is therefore one function,
// Resolve, and its callers only decide what to do with the answer.
//
// The rule depends on the scope the application is in.
//
// An application installed into the user or system scope runs on the
// tarantool its archive was packed with: the version app.manifest.lock
// records as bundled_tarantool_version, installed under the versions/ tree of
// the scope. The active link of the scope, <root>/bin/tarantool, is never
// used: tt binary switch moves it for whoever works in a shell, and that must
// not change the runtime of every application already installed. An archive
// packed --without-deps bundles no tarantool, and its lock records none; such
// an application runs on the highest installed version that satisfies
// [platform].tarantool of its manifest.
//
// A project is being worked on, and runs on the tarantool its developer chose
// for it: the active link of the project, .tarantool/bin/tarantool, which
// tt binary install and tt binary switch set, and else the first tarantool on
// PATH. A version other than the one the lock bundles is a warning and does
// not stop the run: running a project on another tarantool is what a
// developer does on purpose.
//
// Resolve neither logs nor prints. It returns the path along with where it
// came from, so that a command can tell the user which tarantool runs and
// why, and it returns its warnings as values, for each caller to surface the
// way it surfaces everything else.
package tarantool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/scope"
)

// UseSystemEnv names the environment variable that makes a project run on the
// tarantool from PATH even when the project has one of its own. Any value but
// an empty one, "0" or "false" sets it.
//
// It applies to the project scope only. An application installed into the
// user or system scope runs on the version its lock names whatever the
// environment says, for the reason tt binary switch does not move it: the
// runtime of an installed application is fixed when it is installed, not by
// the shell, or the unit, that happens to start it.
const UseSystemEnv = "TT_USE_SYSTEM_TARANTOOL"

const (
	// lockFileName is the lock in the application directory.
	lockFileName = "app.manifest.lock"
	// flavorEE is the Enterprise flavor of a [platform].tarantool constraint.
	flavorEE = "ee"
)

var (
	// ErrNotInstalled reports that the user or system scope holds no tarantool
	// the application may run on.
	ErrNotInstalled = errors.New("tarantool is not installed")
	// ErrNoTarantool reports a project with no tarantool of its own and none on
	// PATH.
	ErrNoTarantool = errors.New("no tarantool executable found")
)

// Origin tells where Resolve took the executable from.
type Origin int

const (
	// OriginUnknown is the zero Origin; Resolve never returns it.
	OriginUnknown Origin = iota
	// OriginLock is the version app.manifest.lock bundles, installed in the
	// scope of the application.
	OriginLock
	// OriginConstraint is the highest version installed in the scope of the
	// application that satisfies [platform].tarantool, for a lock that
	// bundles none.
	OriginConstraint
	// OriginProject is the active link of the project,
	// .tarantool/bin/tarantool.
	OriginProject
	// OriginPath is the first tarantool on PATH.
	OriginPath
)

// App is the application Resolve chooses a tarantool for.
type App struct {
	// Scope is the scope the application is in.
	Scope binary.Scope
	// Dir is the application directory. In the project scope it is the
	// project directory, the one holding .tarantool. In the user and system
	// scopes it is the installed version, <root>/versions/<app>/<app>_<ver>,
	// with apps/<app> already resolved, so that the tarantool matches the
	// version that is started rather than whichever is active by the time
	// the link is read again.
	Dir string
}

// Executable is the tarantool Resolve chose.
type Executable struct {
	// Path is the executable to run.
	Path string
	// Origin tells where Path was taken from.
	Origin Origin
	// Program is binary.ProgramCe or binary.ProgramEe when Path is a
	// tt binary installation, and binary.ProgramUnknown when it is not one,
	// as for a tarantool from PATH.
	Program binary.Program
	// Version identifies the installation the way tt binary does, by the
	// suffix of its directory under versions/: "3.8.1",
	// "sdk-gc64-3.8.1-0-r93". It is empty when Program is unknown.
	Version string
	// Warnings are diagnostics that do not stop the run, for the caller to
	// surface.
	Warnings []string
}

// Resolve chooses the tarantool app runs on. The package documentation gives
// the rule; the error for an application without a tarantool says what to
// install, and in which scope.
func Resolve(app App) (Executable, error) {
	dir, err := filepath.Abs(app.Dir)
	if err != nil {
		return Executable{}, fmt.Errorf("cannot resolve application directory %q: %w",
			app.Dir, err)
	}

	app.Dir = dir

	switch app.Scope {
	case binary.ScopeProject:
		return resolveProject(app.Dir, useSystem())
	case binary.ScopeUser, binary.ScopeSystem:
		// The roots of these scopes come from the place tt binary takes them
		// from, so the versions looked up here are the ones it installed.
		root, err := app.Scope.ArtifactPath()
		if err != nil {
			return Executable{}, err
		}

		return resolveInstalled(app, root)
	default:
		return Executable{}, fmt.Errorf("%w: %s", binary.ErrUnknownScope, app.Scope)
	}
}

// useSystem reports whether UseSystemEnv is set. It reads the variable the way
// tt run always has, so a value that forced the tarantool from PATH before
// still does.
func useSystem() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(UseSystemEnv))) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

// installHint returns the command that installs version of program into the
// scope. Enterprise is installed through the tarantool command as well, and
// binary.VersionArgument spells the version so that the command selects it.
func installHint(program binary.Program, version string, appScope binary.Scope) string {
	return fmt.Sprintf("tt binary install %s %s --scope %s", binary.ProgramCe,
		binary.VersionArgument(program, version), appScope)
}

// manifestPath returns the path of the manifest in the application directory.
func manifestPath(dir string) string {
	return filepath.Join(dir, scope.ManifestFileName)
}

// lockPath returns the path of the lock in the application directory.
func lockPath(dir string) string {
	return filepath.Join(dir, lockFileName)
}
