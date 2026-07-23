package install

import (
	"fmt"
	"sort"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/deps"
)

// contribution is one package's stake in a shared dependency: the version that
// package's lock pinned, and the version constraint its manifest declared. The
// constraint is empty for a purely transitive dependency — one a package pulls
// in without declaring, so it pins a version but restricts nothing.
type contribution struct {
	// pkg is the package that brought this dependency.
	pkg string
	// pin is the exact version the package's lock recorded.
	pin string
	// constraint is the version expression the package's manifest declared,
	// e.g. ">=3.0.0,<4.0.0". Empty when the dependency is transitive.
	constraint string
}

// reconcile chooses the single locked version of a shared dependency that all
// contributing packages can live with. The choice is made only among the
// versions the packages already locked — no registry is consulted and nothing
// is re-resolved:
//
//   - if every package pinned the same version, that version is used;
//   - otherwise the pins that satisfy every package's declared constraint are
//     the candidates, and the highest of them wins;
//   - if no pin satisfies all constraints, it is a hard error carrying a
//     breakdown of who pinned what and whose constraint excluded it.
//
// dep is the dependency name, used only for diagnostics. contributions must be
// non-empty.
func reconcile(dep string, contributions []contribution) (string, error) {
	if len(contributions) == 0 {
		return "", fmt.Errorf("%w %q: no contributors", errIncompatibleDeps, dep)
	}

	pins, err := parsePins(dep, contributions)
	if err != nil {
		return "", err
	}

	if allEqual(pins) {
		return contributions[0].pin, nil
	}

	constraints, err := gatherConstraints(dep, contributions)
	if err != nil {
		return "", err
	}

	best, ok := highestMatching(pins, constraints)
	if ok {
		return best, nil
	}

	return "", incompatibleError(dep, contributions)
}

// parsePin pairs a contribution's raw pin string with its parsed version.
type parsedPin struct {
	raw     string
	version luarocks.Version
}

// parsePins parses every contribution's pin into a comparable version.
func parsePins(dep string, contributions []contribution) ([]parsedPin, error) {
	pins := make([]parsedPin, 0, len(contributions))

	for _, contrib := range contributions {
		version, err := deps.ParseVersion(contrib.pin)
		if err != nil {
			return nil, fmt.Errorf("%w %q: package %q pinned unparseable version %q: %w",
				errIncompatibleDeps, dep, contrib.pkg, contrib.pin, err)
		}

		pins = append(pins, parsedPin{raw: contrib.pin, version: version})
	}

	return pins, nil
}

// allEqual reports whether every pin is the same version.
func allEqual(pins []parsedPin) bool {
	for i := 1; i < len(pins); i++ {
		if deps.Compare(pins[i].version, pins[0].version) != 0 {
			return false
		}
	}

	return true
}

// gatherConstraints parses and unions every declared constraint. Transitive
// contributions (empty constraint) add nothing. The union is an AND: a
// candidate must satisfy all of them at once.
func gatherConstraints(
	dep string, contributions []contribution,
) ([]luarocks.VersionConstraint, error) {
	all := make([]luarocks.VersionConstraint, 0, len(contributions))

	for _, contrib := range contributions {
		if contrib.constraint == "" {
			continue
		}

		parsed, err := deps.ParseConstraints(contrib.constraint)
		if err != nil {
			return nil, fmt.Errorf("%w %q: package %q declared unparseable constraint %q: %w",
				errIncompatibleDeps, dep, contrib.pkg, contrib.constraint, err)
		}

		all = append(all, parsed...)
	}

	return all, nil
}

// highestMatching returns the highest pin that satisfies every constraint, and
// whether any pin did. With no constraints every pin qualifies, so the highest
// pin wins outright.
func highestMatching(
	pins []parsedPin, constraints []luarocks.VersionConstraint,
) (string, bool) {
	// Highest first, so the first match is the answer.
	sorted := make([]parsedPin, len(pins))
	copy(sorted, pins)
	sort.SliceStable(sorted, func(i, j int) bool {
		return deps.Compare(sorted[i].version, sorted[j].version) > 0
	})

	for _, pin := range sorted {
		if len(constraints) == 0 || deps.Match(pin.version, constraints) {
			return pin.raw, true
		}
	}

	return "", false
}

// incompatibleError builds the diagnostic for a shared dependency no locked
// version satisfies: every package's pin, and every declared constraint, so the
// user can see which requirement excluded which version.
func incompatibleError(dep string, contributions []contribution) error {
	pins := make([]string, 0, len(contributions))
	constraints := make([]string, 0, len(contributions))

	for _, contrib := range contributions {
		pins = append(pins, fmt.Sprintf("%s pinned %s", contrib.pkg, contrib.pin))

		if contrib.constraint != "" {
			constraints = append(constraints,
				fmt.Sprintf("%s requires %s", contrib.pkg, contrib.constraint))
		}
	}

	msg := fmt.Sprintf("%s: %s", dep, strings.Join(pins, ", "))
	if len(constraints) > 0 {
		msg += "; " + strings.Join(constraints, ", ")
	}

	msg += "; no locked version satisfies every requirement"

	return &ExitError{Code: exitStateError, Err: fmt.Errorf("%w: %s", errIncompatibleDeps, msg)}
}
