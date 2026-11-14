// Package standard lists the standard modules of tt: the modules of tt's
// repository the official tt is built with, and which a distribution of tt
// takes by default ([distribution].standard of tt.manifest.toml), at the
// commit of its core.
//
// The core's own commands, builtin, are not in the list: they are
// core.Builtin, and this package does not import the core, so that the
// core's internal tests may import it. The caller adds builtin itself:
//
//	modules := standard.Modules()
//
//	modules["builtin"] = core.Builtin
//	os.Exit(core.Main(modules))
//
// tt binary build tt reads the names of the modules from the source of
// this package, without building it, to check [distribution].exclude and
// to record the modules in tt.manifest.lock: Modules is one return of a
// map literal whose keys are string literals.
package standard

import (
	"github.com/tarantool/tt/modules/aeon"
	"github.com/tarantool/tt/modules/emmylua"
)

// Modules returns the standard modules by name, a new map on each call:
// the caller may add to it and delete from it. A value is the constructor
// of the module, of either type core.Modules takes: an sdk.Constructor or
// an sdk.ModuleConstructor.
func Modules() map[string]any {
	return map[string]any{
		"aeon":    aeon.New,
		"emmylua": emmylua.New,
	}
}
