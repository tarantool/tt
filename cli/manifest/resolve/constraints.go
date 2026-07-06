package resolve

import (
	luarocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/deps"
)

// satisfiable reports whether some version could satisfy every constraint at
// once. It is a structural check over the version order (deps.Compare), so a
// contradiction between merged declarations is caught before any registry is
// queried - a conflict is reported regardless of whether the rock is even
// published.
//
// The check is sound (it never rejects a satisfiable set): it intersects the
// lower/upper bounds from >, >=, <, <=, == and ~>, and rejects only a proven
// empty interval or conflicting == pins. The pessimistic ~> is translated to
// its exact [v, next) interval for the common case and to an == when it also
// pins a revision. ~= only narrows the single-point case, where it can make an
// otherwise-unique version impossible.
func satisfiable(constraints []luarocks.VersionConstraint) bool {
	var lower, upper, equal bound

	var excludes []luarocks.Version

	for _, constraint := range constraints {
		switch constraint.Op {
		case "==":
			if equal.set && deps.Compare(equal.version, constraint.Version) != 0 {
				return false
			}

			equal = bound{version: constraint.Version, set: true, inclusive: true}
		case "~=":
			excludes = append(excludes, constraint.Version)
		case ">":
			lower.tightenLower(constraint.Version, false)
		case ">=":
			lower.tightenLower(constraint.Version, true)
		case "<":
			upper.tightenUpper(constraint.Version, false)
		case "<=":
			upper.tightenUpper(constraint.Version, true)
		case "~>":
			narrowPessimistic(&lower, &upper, constraint.Version)
		}
	}

	if equal.set {
		return equalFits(equal.version, lower, upper, excludes)
	}

	return intervalNonEmpty(lower, upper, excludes)
}

// bound is one side of a version interval: a version, whether it is set, and
// whether the endpoint itself is allowed.
type bound struct {
	version   luarocks.Version
	set       bool
	inclusive bool
}

// tightenLower raises the lower bound to version, keeping the more restrictive
// (exclusive) endpoint on a tie.
func (b *bound) tightenLower(version luarocks.Version, inclusive bool) {
	if !b.set {
		*b = bound{version: version, set: true, inclusive: inclusive}

		return
	}

	cmp := deps.Compare(version, b.version)
	switch {
	case cmp > 0:
		*b = bound{version: version, set: true, inclusive: inclusive}
	case cmp == 0 && !inclusive:
		b.inclusive = false
	}
}

// tightenUpper lowers the upper bound to version, keeping the more restrictive
// (exclusive) endpoint on a tie.
func (b *bound) tightenUpper(version luarocks.Version, inclusive bool) {
	if !b.set {
		*b = bound{version: version, set: true, inclusive: inclusive}

		return
	}

	cmp := deps.Compare(version, b.version)
	switch {
	case cmp < 0:
		*b = bound{version: version, set: true, inclusive: inclusive}
	case cmp == 0 && !inclusive:
		b.inclusive = false
	}
}

// narrowPessimistic applies the ~> operator's interval. `~> 1.2` allows
// [1.2, 1.3); `~> 1.2.3` allows [1.2.3, 1.2.4).
//
// A revision-pinned ~> (`~> 1.2.3-2`) is only lower-bounded here, deliberately.
// deps.partialMatch accepts any version whose leading components match and
// whose revision equals the pin - including versions with extra trailing
// components that are not Compare-equal to the pin - so no single interval
// captures that set. Under-constraining (lower bound only) keeps satisfiable
// sound: it never reports a spurious conflict; a genuinely empty case just
// defers to the resolver.
func narrowPessimistic(lower, upper *bound, version luarocks.Version) {
	lower.tightenLower(version, true)

	if version.Revision != 0 {
		return
	}

	next, ok := bumpLastComponent(version)
	if ok {
		upper.tightenUpper(next, false)
	}
}

// bumpLastComponent returns version with its last numeric component incremented
// and everything below it cleared - the exclusive upper edge of a ~> interval.
// ok is false when version has no components.
func bumpLastComponent(version luarocks.Version) (luarocks.Version, bool) {
	if len(version.Components) == 0 {
		return luarocks.Version{
			Raw: "", Components: nil, Revision: 0, IsSCM: false, IsDev: false,
		}, false
	}

	components := append([]int(nil), version.Components...)
	components[len(components)-1]++

	return luarocks.Version{
		Raw:        "",
		Components: components,
		Revision:   0,
		IsSCM:      false,
		IsDev:      false,
	}, true
}

// equalFits reports whether an == pin falls inside the bounds and is not
// excluded.
func equalFits(
	version luarocks.Version, lower, upper bound, excludes []luarocks.Version,
) bool {
	if lower.set && !aboveLower(version, lower) {
		return false
	}

	if upper.set && !belowUpper(version, upper) {
		return false
	}

	return !excluded(version, excludes)
}

// intervalNonEmpty reports whether [lower, upper] contains a usable version.
func intervalNonEmpty(lower, upper bound, excludes []luarocks.Version) bool {
	if !lower.set || !upper.set {
		return true
	}

	cmp := deps.Compare(lower.version, upper.version)
	switch {
	case cmp > 0:
		return false
	case cmp == 0:
		// A single point survives only if both ends include it and it is not
		// excluded.
		return lower.inclusive && upper.inclusive && !excluded(lower.version, excludes)
	default:
		return true
	}
}

func aboveLower(version luarocks.Version, lower bound) bool {
	cmp := deps.Compare(version, lower.version)

	return cmp > 0 || (cmp == 0 && lower.inclusive)
}

func belowUpper(version luarocks.Version, upper bound) bool {
	cmp := deps.Compare(version, upper.version)

	return cmp < 0 || (cmp == 0 && upper.inclusive)
}

func excluded(version luarocks.Version, excludes []luarocks.Version) bool {
	for _, exclude := range excludes {
		if deps.Compare(version, exclude) == 0 {
			return true
		}
	}

	return false
}
