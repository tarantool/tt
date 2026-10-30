package running

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/scope"
)

// versionVarPerm is the mode of var/ in a version, the mode of the version
// directory itself: the instances of the system scope run as another user
// than the one that installs, and go through it.
const versionVarPerm = 0o755

var (
	// errIntegrityEnv is wrapped by the error for a Tarantool instance whose
	// record sets env while the integrity checks are on.
	errIntegrityEnv = errors.New("does not reach Tarantool Enterprise when it checks " +
		"integrity: it starts tarantool with an empty environment; put the values into " +
		"the config")
	// errIntegrityScript is wrapped by the error for an instance that runs
	// its script while the integrity checks are on: tt starts it through a
	// launcher that takes the script and the paths of the instance from the
	// environment, which Tarantool Enterprise drops then.
	errIntegrityScript = errors.New("runs its script through the launcher of tt, which " +
		"takes the script and the paths of the instance from the environment, and " +
		"Tarantool Enterprise starts with an empty environment when it checks integrity; " +
		"configure the instance by the cluster config instead")
)

// linksLayout reports whether the Tarantool instance inst finds the layout
// tt keeps it in through the links of its version (scope.VarLinks): it runs
// with the integrity checks on, from a version installed in the user or the
// system scope.
//
// Tarantool Enterprise starts with an empty environment when it checks
// integrity, so the TT_*_DEFAULT variables the layout is passed through do
// not reach it, and the paths of the instance fall back to the defaults of
// Tarantool under its working directory, the version: var/lib/<inst>,
// var/run/<inst>. A signed install links var/lib, var/run and var/log of
// the version to the directories of the layout, so the defaults name the
// files of the layout. A project has no signed install, and an external
// process gets its paths from tt, which runs it.
func linksLayout(inst *InstanceCtx) bool {
	if inst.Integrity == nil || IsExternal(inst) {
		return false
	}

	return inst.Scope == app.ScopeUser || inst.Scope == app.ScopeSystem
}

// checkIntegrityStart checks what a Tarantool instance needs to start with
// the integrity checks on: it is configured by the cluster config, not by a
// script tt launches; its record sets no env, which would not reach it; and
// the links of its version, when it runs on them, lead to the layout. A link
// that is missing is no problem here: linkIntegrityLayout creates it before
// the start.
func checkIntegrityStart(inst *InstanceCtx) error {
	if inst.Integrity == nil || IsExternal(inst) {
		return nil
	}

	var problems []error

	if IsScript(inst) {
		problems = append(problems, fmt.Errorf("instance %q of %q %w", inst.InstName,
			inst.AppName, errIntegrityScript))
	}

	if len(inst.Env) > 0 {
		problems = append(problems, fmt.Errorf("instance %q of %q: the env of its record (%s) %w",
			inst.InstName, inst.AppName,
			strings.Join(slices.Sorted(maps.Keys(inst.Env)), ", "), errIntegrityEnv))
	}

	if linksLayout(inst) {
		_, err := scope.CheckVarLinks(inst.Root, inst.AppName, inst.AppDir)
		if err != nil {
			problems = append(problems, fmt.Errorf("instance %q of %q: %w; the version is "+
				"not as tt package install --integrity-check lays it out", inst.InstName,
				inst.AppName, err))
		}
	}

	return errors.Join(problems...)
}

// linkIntegrityLayout gives the version of the instance inst the links to
// the layout (scope.LinkVar) it does not have, when it runs on them
// (linksLayout). A version installed signed has them; one installed before
// tt laid them out gets them at its first start, if the user that starts it
// may write into it.
func linkIntegrityLayout(inst *InstanceCtx) error {
	if !linksLayout(inst) {
		return nil
	}

	missing, err := scope.CheckVarLinks(inst.Root, inst.AppName, inst.AppDir)
	if err != nil {
		return fmt.Errorf("instance %q of %q: %w", inst.InstName, inst.AppName, err)
	}

	if !missing {
		return nil
	}

	err = scope.LinkVar(inst.Root, inst.AppName, inst.AppDir, versionVarPerm)
	if err != nil {
		return fmt.Errorf("instance %q of %q: the version in %s does not link the layout of "+
			"its instances, which Tarantool Enterprise finds only through those links when "+
			"it checks integrity, and they cannot be made: %w; install the version again "+
			"with tt package install --force --integrity-check", inst.InstName, inst.AppName,
			inst.AppDir, err)
	}

	return nil
}
