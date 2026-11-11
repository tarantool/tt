package resolve

import (
	"errors"
	"fmt"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/deps"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// tarantoolRock is the name a rockspec depends on Tarantool by. No server
// has such a rock: Tarantool is the VM the rocks run in, as lua is, and the
// versions a project runs on are those of [platform].tarantool.
const tarantoolRock = "tarantool"

// ErrTarantoolExcluded reports a rock that requires a Tarantool
// [platform].tarantool of the project excludes: the project could install it
// on no Tarantool it runs on.
var ErrTarantoolExcluded = errors.New("the rock requires a Tarantool the project does not run on")

// setTarantool takes [platform].tarantool of the manifest being resolved,
// without its flavor, as the versions a rock's dependency on tarantool is
// checked against.
func (e *Engine) setTarantool(expr string) error {
	bounds, err := deps.ParseConstraints(manifest.ConstraintExpr(expr))
	if err != nil {
		return fmt.Errorf("[platform].tarantool %q: %w", expr, err)
	}

	e.tarantool = expr
	e.tarantoolBounds = bounds

	return nil
}

// checkTarantool checks the dependency req of parent on tarantool against
// [platform].tarantool, instead of resolving it as a rock no server has. A
// requirement no version the project runs on meets fails the resolution; one
// the lowest of them does not meet is a warning, since the project may run on
// a Tarantool the rock does not support. Nothing is locked for it.
func (w *walker) checkTarantool(parent string, req depReq) error {
	if len(req.constraints) == 0 {
		return nil
	}

	bounds := w.engine.tarantoolBounds
	who := parentLabel(parent)

	if !satisfiable(append(append([]luarocks.VersionConstraint{}, bounds...),
		req.constraints...)) {
		return fmt.Errorf("%w: %s requires tarantool %s, and [platform].tarantool is %q",
			ErrTarantoolExcluded, who, req.constraintExpr, w.engine.tarantool)
	}

	floor, ok := lowestVersion(bounds)
	if !ok || !deps.Match(floor, req.constraints) {
		allowed := "any version"
		if ok {
			allowed = floor.Raw
		}

		warning := fmt.Sprintf("%s requires tarantool %s, but [platform].tarantool %q "+
			"allows %s too: raise its lower bound", who, req.constraintExpr,
			w.engine.tarantool, allowed)
		if !w.cache.markTarantool(warning) {
			return nil
		}

		w.warnings = append(w.warnings, warning)
	}

	return nil
}

// lowestVersion is the lowest version bounds allow, when they bound it from
// below with a version they include: >=, == and ~>. A > bound, or none,
// names no lowest version.
func lowestVersion(bounds []luarocks.VersionConstraint) (luarocks.Version, bool) {
	var (
		floor luarocks.Version
		found bool
	)

	for _, bound := range bounds {
		switch bound.Op {
		case ">=", "==", "~>":
		default:
			continue
		}

		if !found || deps.Compare(bound.Version, floor) > 0 {
			floor, found = bound.Version, true
		}
	}

	if found && !deps.Match(floor, bounds) {
		return luarocks.Version{}, false
	}

	return floor, found
}
