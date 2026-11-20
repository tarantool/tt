package cmd

import (
	"fmt"

	"github.com/tarantool/tt/sdk/v3/log"
	"github.com/tarantool/tt/v3/cli/manifest/install"
	"github.com/tarantool/tt/v3/cli/manifest/state"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
)

// resolveTarantool chooses the tarantool an application runs on, as
// tt instance start does; a variable for the tests.
//
//nolint:gochecknoglobals // Replaced by tests only.
var resolveTarantool = tarantool.Resolve

// missingTarantoolWarnings returns, for every application installed into the
// user or system scope, the warning that it has no tarantool to start on: an
// archive packed --without-deps bundles none, and none in the scope may
// satisfy [platform].tarantool. The warning is the error tt instance start
// would fail with, which names the constraint and the tt binary install
// command, so that the install says it rather than the first start.
func missingTarantoolWarnings(installed []install.OneResult, projectDir string) []string {
	var warnings []string

	for _, one := range installed {
		appScope, err := binary.ParseScope(string(one.Scope))
		if one.Skipped || one.Scope == install.ScopeProject || err != nil {
			continue
		}

		root, err := state.ScopeRoot(one.Scope, projectDir)
		if err != nil {
			continue
		}

		_, err = resolveTarantool(tarantool.App{
			Scope: appScope,
			Dir:   state.VersionDir(root, one.Package, one.Version),
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s %s cannot start yet: %s",
				one.Package, one.Version, err))
		}
	}

	return warnings
}

// warnMissingTarantool logs missingTarantoolWarnings.
func warnMissingTarantool(installed []install.OneResult, projectDir string) {
	for _, warning := range missingTarantoolWarnings(installed, projectDir) {
		log.Warn(warning)
	}
}
