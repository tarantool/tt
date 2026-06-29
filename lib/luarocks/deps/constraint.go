package deps

import (
	"fmt"
	"regexp"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// operatorAliases maps the surface forms accepted by upstream
// queries.lua:84-96 to their canonical operator strings used in
// rocks.VersionConstraint.Op.
var operatorAliases = map[string]string{
	"==": "==",
	"~=": "~=",
	">":  ">",
	"<":  "<",
	">=": ">=",
	"<=": "<=",
	"~>": "~>",
	"":   "==",
	"=":  "==",
	"!=": "~=",
}

// constraintRe matches one constraint segment "<op><version>". The op group
// is greedy over `[<>=~!]*` so multi-character operators like ">=" win
// over a bare ">". Trailing whitespace + comma are stripped by the caller
// when splitting segments.
var constraintRe = regexp.MustCompile(`^\s*(@?)([<>=~!]*)\s*([0-9A-Za-z._-]+)\s*$`)

// ParseConstraint parses one constraint segment (e.g. ">= 1.2.3", "~> 1.2",
// "1.0" with implicit "==") into a rocks.VersionConstraint.
//
// Whitespace is tolerated around the operator and version. An empty
// operator defaults to "==". An `@` prefix is accepted for upstream
// compatibility (no-upgrade marker) and silently dropped — the engine does
// not model no_upgrade since the facade does not perform automatic upgrades.
func ParseConstraint(s string) (rocks.VersionConstraint, error) {
	m := constraintRe.FindStringSubmatch(s)
	if m == nil {
		return rocks.VersionConstraint{}, fmt.Errorf("deps: cannot parse constraint %q", s)
	}

	op, ok := operatorAliases[m[2]]
	if !ok {
		return rocks.VersionConstraint{}, fmt.Errorf("deps: unknown constraint operator %q in %q", m[2], s)
	}

	v, err := ParseVersion(m[3])
	if err != nil {
		return rocks.VersionConstraint{}, fmt.Errorf("deps: constraint %q: %w", s, err)
	}

	return rocks.VersionConstraint{Op: op, Version: v}, nil
}

// ParseConstraints parses a full constraint expression (e.g.
// ">= 1.2.3, < 2.0") into the AND'd list of constraints upstream stores
// under queries[i].constraints.
//
// An empty input yields an empty (non-nil) slice — i.e. "no constraints,
// accept any version".
func ParseConstraints(s string) ([]rocks.VersionConstraint, error) {
	out := []rocks.VersionConstraint{}

	for _, seg := range splitConstraints(s) {
		if seg == "" {
			continue
		}

		c, err := ParseConstraint(seg)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	return out, nil
}

// splitConstraints splits on commas — operators do not contain commas, so
// this is unambiguous in the grammar.
func splitConstraints(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	return parts
}

// Match reports whether v satisfies every constraint in cs. An empty
// constraint list always matches (matches upstream — no constraints means
// any version is acceptable).
func Match(v rocks.Version, cs []rocks.VersionConstraint) bool {
	for _, c := range cs {
		if !matchOne(v, c) {
			return false
		}
	}

	return true
}

func matchOne(v rocks.Version, c rocks.VersionConstraint) bool {
	cv := c.Version

	cmp := Compare(v, cv)

	switch c.Op {
	case "==":
		return cmp == 0
	case "~=":
		return cmp != 0
	case ">":
		return cmp > 0
	case "<":
		return cmp < 0
	case ">=":
		return cmp >= 0
	case "<=":
		return cmp <= 0
	case "~>":
		return partialMatch(v, cv)
	default:
		// Unknown op — fail closed. The constructor rejects unknown
		// ops, but if a hand-constructed VersionConstraint reaches us we'd
		// rather refuse the match than silently allow it.
		return false
	}
}

// partialMatch implements the `~>` pessimistic operator: every component
// of `requested` must match `version`. Trailing components of `version`
// beyond requested's length are wildcards. If requested has a non-zero
// revision, that revision must match exactly too.
//
// Examples:
//
//   - `~> 1.2` matches 1.2, 1.2.1, 1.2.99; does NOT match 1.3.
//   - `~> 1.2.3` matches 1.2.3, 1.2.3-1; does NOT match 1.2.4.
func partialMatch(version, requested rocks.Version) bool {
	for i, ri := range requested.Components {
		var vi int
		if i < len(version.Components) {
			vi = version.Components[i]
		}

		if ri != vi {
			return false
		}
	}

	if requested.Revision != 0 {
		return version.Revision == requested.Revision
	}

	return true
}
