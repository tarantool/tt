// Package deps implements version parsing, constraint matching and the
// transitive dependency resolver used by the Rocks facade.
//
// Upstream references:
//
//   - luarocks/src/luarocks/core/vers.lua — Version parsing + comparator.
//   - luarocks/src/luarocks/queries.lua   — Constraint grammar.
//   - luarocks/src/luarocks/search.lua    — Remote search / pick-latest.
//
// The on-the-wire shapes (rocks.Version, rocks.VersionConstraint) live in
// the root rocks package so callers can talk about versions without
// depending on deps.
package deps

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// Numeric deltas applied to the version-component slot when a token is one
// of the recognized keywords (upstream core/vers.lua:9-17). `scm` and `dev`
// sort ABOVE numeric releases by virtue of these large positive deltas.
const (
	deltaDev   = 120000000
	deltaSCM   = 110000000
	deltaCVS   = 100000000
	deltaRC    = -1000
	deltaPre   = -10000
	deltaBeta  = -100000
	deltaAlpha = -1000000

	// asciiFallbackDivisor scales the first byte of an unknown alpha token
	// into a small delta (upstream's `token[0]/1000` ASCII fallback).
	asciiFallbackDivisor = 1000
)

var keywordDeltas = map[string]int{
	"dev":   deltaDev,
	"scm":   deltaSCM,
	"cvs":   deltaCVS,
	"rc":    deltaRC,
	"pre":   deltaPre,
	"beta":  deltaBeta,
	"alpha": deltaAlpha,
}

// digitsRe matches a leading numeric token; matches upstream's
// `^(%d+)[%.%-%_]*(.*)`.
var digitsRe = regexp.MustCompile(`^([0-9]+)[._-]*(.*)$`)

// alphaRe matches a leading alpha token; matches upstream's
// `^(%a+)[%.%-%_]*(.*)`.
var alphaRe = regexp.MustCompile(`^([A-Za-z]+)[._-]*(.*)$`)

// revisionRe matches the trailing `-N` revision suffix.
var revisionRe = regexp.MustCompile(`^(.*)-([0-9]+)$`)

// ParseVersion parses a rock version string (e.g. "1.2.3-4", "scm-1",
// "dev-1") into a rocks.Version.
//
// The semantics match upstream `core/vers.parse_version`:
//
//   - Strip surrounding whitespace.
//   - Strip a trailing `-N` digit revision and store as Version.Revision.
//   - Walk the remainder splitting on `.`, `-`, `_`. Numeric tokens become
//     components; recognized keywords are mapped via keywordDeltas and
//     contribute at the current slot; unknown alpha tokens fall back to
//     `token[0]/1000` per upstream's ASCII fallback.
//   - "scm" and "dev" set IsSCM / IsDev as user-facing booleans; the
//     numeric Components encode the actual ordering.
func ParseVersion(s string) (rocks.Version, error) {
	raw := s

	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return rocks.Version{}, errors.New("deps: empty version string")
	}

	v := rocks.Version{Raw: raw}

	// Trailing -N revision.
	if m := revisionRe.FindStringSubmatch(trimmed); m != nil {
		// Heuristic: keywords like "scm-1" have m[1] == "scm" — that's still
		// the version body, with revision == 1.
		rev, err := strconv.Atoi(m[2])
		if err != nil {
			return rocks.Version{}, fmt.Errorf("deps: parse revision in %q: %w", s, err)
		}

		trimmed = m[1]
		v.Revision = rev
	}

	cur := trimmed
	for len(cur) > 0 {
		if m := digitsRe.FindStringSubmatch(cur); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				return rocks.Version{}, fmt.Errorf("deps: parse %q in %q: %w", m[1], s, err)
			}

			v.Components = append(v.Components, n)
			cur = m[2]

			continue
		}

		m := alphaRe.FindStringSubmatch(cur)
		if m == nil {
			return rocks.Version{}, fmt.Errorf("deps: cannot parse remainder %q of version %q", cur, s)
		}

		tok := strings.ToLower(m[1])

		delta, ok := keywordDeltas[tok]
		if !ok {
			// Upstream fallback: token's first byte divided by 1000.
			// This is integer division in our model.
			delta = int(tok[0]) / asciiFallbackDivisor
		}

		switch tok {
		case "scm":
			v.IsSCM = true
		case "dev":
			v.IsDev = true
		}

		// Upstream `add_token` adds the delta to the current slot when a
		// component already exists; we emulate by appending if no slot
		// has been opened yet, otherwise merging into the last slot.
		if len(v.Components) == 0 {
			v.Components = append(v.Components, delta)
		} else {
			v.Components[len(v.Components)-1] += delta
		}

		cur = m[2]
	}

	if len(v.Components) == 0 {
		// All-keyword input ("scm") still needs a placeholder slot to
		// participate in comparisons. Upstream would have appended the delta.
		v.Components = append(v.Components, 0)
	}

	return v, nil
}

// Compare returns -1 if a < b, 0 if equal, 1 if a > b.
//
// Comparison is component-wise; missing trailing components on either side
// are treated as 0 (matches upstream __lt). When all components are equal the
// revision breaks the tie, compared UNCONDITIONALLY (a missing revision is 0)
// so callers always get a total order. This differs from upstream, which
// returns "equal" when one side lacks a revision — the Go API cannot tell
// "absent" from "0", so it always tie-breaks deterministically.
func Compare(a, b rocks.Version) int {
	n := max(len(b.Components), len(a.Components))
	for i := range n {
		var ai, bi int
		if i < len(a.Components) {
			ai = a.Components[i]
		}

		if i < len(b.Components) {
			bi = b.Components[i]
		}

		if ai != bi {
			if ai < bi {
				return -1
			}

			return 1
		}
	}
	// All components equal — fall back to revision. We compare revisions
	// unconditionally (treating missing as 0) so callers get a total order;
	// upstream returns "equal" when one side lacks revision, but our Go
	// API does not distinguish "absent" from "0".
	if a.Revision != b.Revision {
		if a.Revision < b.Revision {
			return -1
		}

		return 1
	}

	return 0
}
