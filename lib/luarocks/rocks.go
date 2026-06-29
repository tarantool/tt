// Package rocks is a pure-Go implementation of the LuaRocks subset needed
// to manage rocks inside a Tarantool installation.
//
// The library produces on-disk artifacts (tree layout, manifest files,
// .rock archives) that are byte-equal to what upstream LuaRocks 3.9.2 would
// produce for the same rock at the same version against a Tarantool target.
// A rock installed by lib/luarocks is queryable by upstream
// LuaRocks's `luarocks show` and vice versa.
//
// The facade — `client.New(rocks.Config{...}).Install(ctx, "metrics", opts)`
// — lives in the sub-package `github.com/tarantool/tt/lib/luarocks/client`
// rather than at this root: it calls `deps.Resolve` and composes the `remote`
// index, both of which transitively import this root package for shared data
// types, so a facade here would form an import cycle.
package rocks

// LuaRocksVersion is the LuaRocks release the embedded Tarantool fork is based on for
// the lua backend (see internal/luarocks). The native backend produces on-disk
// artifacts byte-equal to this LuaRocks version.
const LuaRocksVersion = "3.9.2"
