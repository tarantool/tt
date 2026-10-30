package running

import (
	"errors"
	"fmt"
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
// its runtimes do not list: a program of the host, a tarantool from PATH, a
// script outside the package.
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
// with its hashes files there; one that has none adds nothing, and is
// refused when it is to run (checkSignedTarantool, watchdogCommandLine).
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

// checkSigned checks the file at path, which the instance inst is to run as
// what, against the integrity checks of its application, when they are on.
func checkSigned(inst *InstanceCtx, what, path string) error {
	if inst.Integrity == nil {
		return nil
	}

	reader, err := inst.Integrity.Repository.Read(path)
	if err != nil {
		return fmt.Errorf("instance %q of %q: %s %s %w by the package or the runtimes it "+
			"runs on: %w", inst.InstName, inst.AppName, what, path, errNotSigned, err)
	}

	return reader.Close() //nolint:wrapcheck // Nothing was read from it.
}

// checkSignedTarantool checks the tarantool exe the Tarantool instance inst
// is to start on, when the integrity checks are on: it has to be a runtime
// whose signature the checks know, so a tarantool from PATH is refused
// before it is looked at.
func checkSignedTarantool(inst *InstanceCtx, exe tarantool.Executable) error {
	if inst.Integrity == nil {
		return nil
	}

	if exe.Origin == tarantool.OriginPath {
		return fmt.Errorf("instance %q of %q: tarantool %s from PATH %w: with the "+
			"integrity checks on, an instance runs on the signed tarantool its package "+
			"bundles", inst.InstName, inst.AppName, exe.Path, errNotSigned)
	}

	return checkSigned(inst, "tarantool", exe.Path)
}
