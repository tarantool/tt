package rockspec

import (
	"os"

	lua "github.com/yuin/gopher-lua"
)

// buildGetenv returns the sandboxed os.getenv implementation governed by
// RockspecConfig.Env:
//
//   - env == nil          → pass through to the host process via os.Getenv;
//   - env != nil (non-empty) → return env[name] if present, else nil;
//   - env != nil (empty)  → always nil (env is exhaustively allow-listed).
//
// Returning lua nil for absent keys matches upstream Lua 5.1 os.getenv
// semantics: a `or` fallback in the rockspec ("$X or default") still works.
func buildGetenv(env map[string]string) lua.LGFunction {
	if env == nil {
		return func(L *lua.LState) int {
			name := L.CheckString(1)
			if v, ok := os.LookupEnv(name); ok {
				L.Push(lua.LString(v))

				return 1
			}

			L.Push(lua.LNil)

			return 1
		}
	}
	// Captured by reference — caller controls the map; do not mutate.
	return func(L *lua.LState) int {
		name := L.CheckString(1)
		if v, ok := env[name]; ok {
			L.Push(lua.LString(v))

			return 1
		}

		L.Push(lua.LNil)

		return 1
	}
}
