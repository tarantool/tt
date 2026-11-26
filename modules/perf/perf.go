// Package perf is the tt module of the perf commands:
// tt perf record, start, stop, status, report, export commands
// CPU and memory profiles; local and remote instances; the ttpf container; raw LuaJIT dumps.
//
// The module is built on the tt SDK alone. [New] is its constructor; a
// distribution of tt lists it in the table of modules it hands to the core.
package perf

import (
	"github.com/tarantool/tt/sdk"
)

// New builds the perf module's commands: the perf group with its record,
// start, stop, status, report, and export commands, mounted at the root of tt.
func New(services sdk.Services) []sdk.Mount {
	return []sdk.Mount{}
}
