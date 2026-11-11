// Package emmylua is the tt module of the emmylua commands: they put the
// programs of EmmyLua (github.com/EmmyLuaLs/emmylua-analyzer-rust), the
// analyzer of annotated Lua, into a project as a dev dependency, and run
// them - the checker, the doc generator, the formatter and the language
// server - with what tt knows of the project.
//
// The module is built on the tt SDK alone. [New] is its constructor; a
// distribution of tt lists it in the table of modules it hands to the core.
package emmylua

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/modules/emmylua/internal/analyzer"
	"github.com/tarantool/tt/sdk"
)

// passHelp says how the arguments of a program are given.
const passHelp = "Arguments after -- go to the program as they are."

// errUsage reports flags or arguments of tt emmylua that do not go
// together.
var errUsage = errors.New("invalid flags")

// New builds the emmylua module's commands: the emmylua group with its
// prepare, rock, check, doc, fmt, ls and config commands, mounted at the
// root of tt.
func New(services sdk.Services) []sdk.Mount {
	group := &cobra.Command{
		Use:   "emmylua",
		Short: "Check, document and format Lua code with EmmyLua (experimental)",
		Long: "Commands for EmmyLua (github.com/EmmyLuaLs/emmylua-analyzer-rust), the " +
			"analyzer of annotated Lua: prepare puts its programs into the project as " +
			"the dev dependency " + analyzer.RockName + ", check, doc, fmt and ls run " +
			"them, config writes the configuration they run with, and rock packs the " +
			"rocks of a release for a rock server.\n\n" +
			"The programs run with what tt knows of the project: LuaJIT, the globals " +
			"of Tarantool, the EmmyLua stubs of the Tarantool the project runs on " +
			"(share/tarantool/emmylua, Tarantool 3.9 and newer) and the modules its " +
			"rocks install. The project's own .emmyrc.json is read after that and " +
			"adds to it. The " + analyzer.RockName + " rock of every platform comes " +
			"from the index of tarantool-contrib/rocks-repo; prepare --from and rock " +
			"pack it themselves, from a local build or from the releases the module " +
			"knows.\n\n" +
			"The project is the one the current directory is in, found as the other " +
			"project commands of tt find it. Outside every project check, doc, fmt and " +
			"ls need no prepare: they run the programs of the rock, fetched once into " +
			"the user cache (tt/emmylua there), with the configuration kept there too; " +
			"--rock-server names another server, a directory of rocks to work offline.",
	}

	group.AddCommand(newPrepareCmd(services), newRockCmd(services),
		newCheckCmd(services), newDocCmd(services), newFmtCmd(services), newLsCmd(services),
		newConfigCmd(services))

	return []sdk.Mount{{Path: "", Cmd: group}}
}
