package manifest

import (
	"errors"
	"fmt"
)

// ErrTCMCommunity is wrapped by the error for TCM, the Tarantool Cluster
// Manager, declared in a project of Tarantool Community Edition: TCM is a
// product of Tarantool Enterprise and manages its clusters alone.
var ErrTCMCommunity = errors.New("TCM is Tarantool Enterprise only")

const (
	// flavorEE is the flavor of Tarantool Enterprise in a constraint.
	flavorEE = "ee"
	// tcmManifestFile names the manifest in the message of CheckTCM.
	tcmManifestFile = "app.manifest.toml"
)

// platformTable is [platform], which SetPlatformTCM edits with the means of
// the dependency tables: a key of it is a declaration of the short form.
const platformTable DepTable = "platform"

// SetPlatformTCM sets [platform].tcm to constraint in the source of the
// manifest, in place, as SetDependency sets a constraint: the string of a
// key that is there is replaced, and a key that is not is written after the
// last key of [platform]. The Change says what it was.
func (e *Editor) SetPlatformTCM(constraint string) (Change, error) {
	var change Change

	if constraint == "" {
		return change, invalid("platform.tcm", "version constraint is empty")
	}

	doc, err := indexSource(e.src)
	if err != nil {
		return change, err
	}

	decl, err := doc.find(platformTable, "tcm")

	switch {
	case errors.Is(err, ErrNoSuchDependency):
		e.src = doc.insert(platformTable, "tcm", constraint)

		return change, nil
	case err != nil:
		return change, err
	case !decl.short:
		return change, fmt.Errorf("platform.tcm: %w: it is not a string, edit it by hand",
			ErrUnsupportedForm)
	}

	change.Existed = true
	change.Previous = decl.versionData
	e.src = doc.replace(decl.version, quoteTOMLString(constraint))

	return change, nil
}

// CheckTCM refuses TCM in a package whose platform runs on Tarantool
// Community Edition: [platform].tarantool with the flavor [ce], or with no
// flavor, which is [ce]. The error says how to switch to Enterprise.
func CheckTCM(platform Platform) error {
	constraint := platform.Tarantool
	if constraint.EffectiveFlavor() == flavorEE {
		return nil
	}

	how := "the flavor [ce]"
	if constraint.Flavor == "" {
		how = "no flavor, which is [ce]"
	}

	enterprise := Constraint{Version: constraint.Version, Flavor: flavorEE}

	return fmt.Errorf("%w, and [platform].tarantool of %s is %q, with %s: Tarantool "+
		"Community Edition; switch the project to Tarantool Enterprise, "+
		"tarantool = %q, to run TCM", ErrTCMCommunity, tcmManifestFile,
		constraint.String(), how, enterprise.String())
}
