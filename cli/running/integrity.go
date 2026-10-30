package running

import (
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// AppIntegrity returns how tt finds the integrity checks of an application,
// for app.Options.IntegrityFor, or nil when the checks are off.
//
// The checks of an application are rooted at its directory - the project,
// or the installed version - whatever the directory tt runs in: an
// installed version is signed as the package it was packed from, and is
// checked where it is installed.
func AppIntegrity(cmdCtx *cmdcontext.CmdCtx) func(app.Scope, string) (
	integrity.IntegrityCtx, error,
) {
	publicKey := cmdCtx.Cli.IntegrityCheck
	if publicKey == "" {
		return nil
	}

	return func(_ app.Scope, dir string) (integrity.IntegrityCtx, error) {
		return integrityprovider.Get().InitializeIntegrityCheck(publicKey, dir)
	}
}
