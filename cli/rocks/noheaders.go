package rocks

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/cli/util"
)

// noHeadersDirMode is the mode of the directories of stubHeaders.
const noHeadersDirMode = 0o755

// noHeadersFileMode is the mode of its headers.
const noHeadersFileMode = 0o644

// stubHeaders makes, for a LuaRocks run with no headers of Tarantool, a
// directory of headers that are none: a lua.h of Lua 5.1 and a module.h,
// each an #error that says where tt looked for the headers (hint). It
// returns the include/tarantool directory and its removal, or "" and a
// no-op when it cannot make one.
//
// LuaRocks checks that LUA_INCDIR holds a lua.h of its Lua version before
// it builds any rock, a rock of Lua alone included, so a run with no headers
// failed to install a rock that compiles nothing. Given these, a rock of Lua
// alone installs, and a rock of C still fails, at its first #include, with
// the hint.
func stubHeaders(hint string) (string, func()) {
	root, err := os.MkdirTemp("", "tt-no-headers-")
	if err != nil {
		return "", func() {}
	}

	remove := func() { _ = os.RemoveAll(root) }

	include := filepath.Join(root, "include", "tarantool")

	err = os.MkdirAll(include, noHeadersDirMode)
	if err != nil {
		remove()

		return "", func() {}
	}

	// A quote or a newline would end the #error early.
	message := strings.NewReplacer("\"", "'", "\n", " ").Replace(
		"tt found no headers of Tarantool to compile against: " + hint)

	for name, body := range map[string]string{
		"lua.h":    "#define LUA_VERSION_NUM 501\n#error \"" + message + "\"\n",
		"module.h": "#error \"" + message + "\"\n",
	} {
		err = os.WriteFile(filepath.Join(include, name), []byte(body), noHeadersFileMode)
		if err != nil {
			remove()

			return "", func() {}
		}
	}

	return include, remove
}

// includeDirOf returns the include directory a LuaRocks run of args is to be
// given for prefix, and what to call once it is done: that of prefix, or for
// a run that may compile when prefix has no headers, one of stubHeaders.
func includeDirOf(prefix tarantool.Prefix, args []string) (string, func(), error) {
	include, err := util.JoinAbspath(prefix.Dir, "include", "tarantool")
	if err != nil {
		return "", func() {}, err
	}

	hint := prefix.MissingHeaders()
	if hint == "" || !buildingCommands[luarocksCommand(args)] {
		return include, func() {}, nil
	}

	stub, remove := stubHeaders(hint)
	if stub == "" {
		return include, func() {}, nil
	}

	return stub, remove, nil
}
