package analyzer

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// stubFiles are the stubs tt writes beside the configuration for the rocks
// whose own code EmmyLua cannot read the interface of: luatest alone.
//
//go:embed stubs
var stubFiles embed.FS

const (
	// stubsDirName is the directory of the stubs tt writes, beside the
	// configuration: a library of the configuration whenever it is
	// written.
	stubsDirName = "stubs"
	// luatestInit is the entry module of luatest, relative to the library
	// that installs it, which the stub of stubFiles stands for.
	luatestInit = "luatest/init.lua"
)

// stubsDir is the directory of the stubs tt writes for the layout.
func (l Layout) stubsDir() string {
	return filepath.Join(filepath.Dir(l.ConfigPath()), stubsDirName)
}

// luatestInits returns the entry modules of luatest in the libraries,
// luatest/init.lua of each library that installs luatest, the rocks of the
// project and the directories of --library: the configuration leaves them
// out and reads the stub of luatest (stubs/luatest/init.lua) in their place.
//
// luatest/init.lua makes the module a table whose metatable takes the
// assertions from luatest.assertions by __index, and defines before_suite
// and after_suite at run time: EmmyLua 0.30 does not follow that __index,
// and no release sees those hooks, so every t.assert_equals(...) of a test
// is an undefined field. The stub makes the module the assertions module of
// the rock itself, with the fields luatest/init.lua sets on it, so the
// assertions are those of the luatest installed. The project's own package
// (Own) is left as it is: a checkout of luatest checks its own code.
func (l Layout) luatestInits() []string {
	var inits []string

	for _, dir := range l.Libraries() {
		path := filepath.Join(dir, filepath.FromSlash(luatestInit))

		if under(path, l.Own) {
			continue
		}

		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			inits = append(inits, path)
		}
	}

	return inits
}

// writeStubs writes the stubs of stubFiles into stubsDir, for the
// configuration of a layout with luatest among its libraries.
func (l Layout) writeStubs() error {
	data, err := stubFiles.ReadFile(stubsDirName + "/" + luatestInit)
	if err != nil {
		return fmt.Errorf("reading the stub of luatest: %w", err)
	}

	path := filepath.Join(l.stubsDir(), filepath.FromSlash(luatestInit))

	err = os.MkdirAll(filepath.Dir(path), dirPerm)
	if err != nil {
		return fmt.Errorf("writing the stub of luatest: %w", err)
	}

	return writeFileAtomic(path, data)
}
