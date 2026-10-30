package running

import (
	"errors"
	"os"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// errNotSigned is wrapped by the error for a file an instance would run,
// with the integrity checks on, that the signatures of its application and
// its runtimes do not list, such as a tt of the host for its watchdog.
var errNotSigned = errors.New("is not signed")

// AppIntegrity returns how tt finds the integrity checks of an application,
// for app.Options.IntegrityFor, or nil when the checks are off.
//
// The checks of an application are rooted at its directory - the project,
// or the installed version - whatever the directory tt runs in: an
// installed version is signed as the package it was packed from, and is
// checked where it is installed. They are rooted at the directories of the
// runtimes it runs on as well: the tarantool it is resolved to and the tt
// that runs its watchdog. A runtime a signed package bundles is installed
// with its hashes files there; one that has none adds nothing, and the tt
// of the watchdog is refused when it is not signed.
func AppIntegrity(cmdCtx *cmdcontext.CmdCtx) func(app.Scope, string) (
	integrity.IntegrityCtx, error,
) {
	publicKey := cmdCtx.Cli.IntegrityCheck
	if publicKey == "" {
		return nil
	}

	return func(scope app.Scope, dir string) (integrity.IntegrityCtx, error) {
		ttExecutable, err := os.Executable()
		if err != nil {
			ttExecutable = ""
		}

		roots := append([]string{dir}, integrityprovider.ExecutableRoots(
			tarantoolOf(scope, dir), ttExecutable)...)

		return integrityprovider.Get().InitializeIntegrityCheck(publicKey, roots...)
	}
}

// tarantoolOf returns the tarantool the application in dir of scope runs
// on, or an empty string when it has none: the start that needs it reports
// that.
func tarantoolOf(scope app.Scope, dir string) string {
	var appScope binary.Scope

	switch scope {
	case app.ScopeProject:
		appScope = binary.ScopeProject
	case app.ScopeUser:
		appScope = binary.ScopeUser
	case app.ScopeSystem:
		appScope = binary.ScopeSystem
	default:
		return ""
	}

	exe, err := tarantool.Resolve(tarantool.App{Scope: appScope, Dir: dir})
	if err != nil {
		return ""
	}

	return exe.Path
}
