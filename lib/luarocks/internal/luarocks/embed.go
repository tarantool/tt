// Package luarocksembed exposes the vendored Tarantool LuaRocks fork (luarocks-3.9.2-tarantool)
// (under src/) and the extra shims (under extra/) as an embedded
// filesystem. The lua engine in client/lua.go preloads these modules into a
// gopher-lua VM. Do not edit src/ directly; it is the vendored Tarantool LuaRocks fork.
// Shim behavior lives in extra/.
package luarocksembed

import "embed"

//go:embed src extra
var FS embed.FS

// ReadFile reads a file from the embedded FS by its slash-separated path
// relative to the internal/luarocks directory (e.g. "extra/wrapper.lua" or
// "src/src/luarocks/core/cfg.lua").
func ReadFile(name string) ([]byte, error) {
	return FS.ReadFile(name)
}
