package build

import (
	"context"
	"slices"
	"sync"

	"github.com/tarantool/go-luarocks/tree"
	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
)

// shownHeadersWarnings are the warnings warnHeaders showed already: a command
// that compiles more than once - several components, a rock and a component -
// shows each once.
var shownHeadersWarnings sync.Map //nolint:gochecknoglobals // Once per process.

// warnHeaders shows the warning that the headers of the Tarantool of the
// build are of another version than its tarantool (rocks.TarantoolInfo
// HeadersWarning), once per command. A build shows it when it compiles
// against the headers, and only then: a command that builds nothing, or a
// package of Lua alone, has nothing a mismatch could break.
func (o Options) warnHeaders() {
	warning := o.Tarantool.HeadersWarning
	if warning == "" {
		return
	}

	if _, shown := shownHeadersWarnings.LoadOrStore(warning, true); shown {
		return
	}

	o.emit([]string{warning})
}

// compiles reports whether the backend of run may compile against the
// headers of Tarantool: it makes modules, which a native module is.
func (run componentRun) compiles() bool {
	return slices.Contains(run.backend.Produces, sdkbuild.Module)
}

// rockSet is the rocks a tree holds.
type rockSet map[tree.RockRef]bool

// installedRocks returns the rocks of the tree at root, nil when there is no
// tree to read.
func installedRocks(ctx context.Context, root string) rockSet {
	view, err := tree.Inspect(ctx, root)
	if err != nil {
		return nil
	}

	installed := rockSet{}
	for _, ref := range view.Rocks() {
		installed[ref] = true
	}

	return installed
}

// warnHeadersOfBuiltRocks shows the warning of warnHeaders when a rock that
// was not in the tree at root before, before being the rocks it held then,
// holds a compiled module now: the install built it, and built it against
// the headers. LuaRocks tells nothing of what it compiles before it does.
func (o Options) warnHeadersOfBuiltRocks(
	ctx context.Context, root string, before rockSet,
) {
	if o.Tarantool.HeadersWarning == "" {
		return
	}

	view, err := tree.Inspect(ctx, root)
	if err != nil {
		return
	}

	for _, ref := range view.Rocks() {
		if before[ref] {
			continue
		}

		files, err := view.Files(ref)
		if err != nil {
			continue
		}

		for _, file := range files {
			if file.Section == tree.SectionLib {
				o.warnHeaders()

				return
			}
		}
	}
}
