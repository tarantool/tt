package resolve

import (
	"errors"
	"fmt"
	"strings"
)

// ErrConflict reports that two requirements for the same dependency cannot both
// be satisfied - either a global and a per-component declaration disagree, or
// two branches of the dependency graph demand incompatible versions. Callers
// match it with errors.Is; the wrapping message carries the offending
// dependency and, for graph conflicts, the chain that led there.
var ErrConflict = errors.New("dependency conflict")

// ErrCycle reports a cycle in the dependency graph. The wrapping message
// carries the chain that closed the loop.
var ErrCycle = errors.New("dependency cycle")

// conflictError renders "<detail> (via a -> b -> c): dependency conflict".
type conflictError struct {
	detail string
	chain  []string
}

func (e *conflictError) Error() string {
	if len(e.chain) == 0 {
		return fmt.Sprintf("%s: %s", e.detail, ErrConflict)
	}

	return fmt.Sprintf("%s (via %s): %s", e.detail, strings.Join(e.chain, " -> "), ErrConflict)
}

func (e *conflictError) Unwrap() error { return ErrConflict }

// cycleError renders "dependency cycle on <name> (via a -> b -> name)".
type cycleError struct {
	name  string
	chain []string
}

func (e *cycleError) Error() string {
	return fmt.Sprintf("%s on %q (via %s)", ErrCycle, e.name, strings.Join(e.chain, " -> "))
}

func (e *cycleError) Unwrap() error { return ErrCycle }
