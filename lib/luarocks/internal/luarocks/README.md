# Vendored LuaRocks

`src/` holds the **Tarantool fork** of LuaRocks 3.9.2
([tarantool/luarocks](https://github.com/tarantool/luarocks), branch
`luarocks-3.9.2-tarantool`), vendored via `git subtree` (do not edit it
directly). This is the same source `tt` ships (`tt/cli/rocks/third_party`), not
stock upstream — the fork registers `tarantool` as a provided dependency
(via `TT_CLI_TARANTOOL_VERSION`), reads the build/install layout from
`hardcoded.lua`, uses Tarantool's `digest` module for md5, and adds embedded-use
guards (`rawget(_G,'arg')`, `cfg.initialized`). Stock upstream lacks all of
these, so tarantool-dependent rockspecs fail there. Because the subtree placed
the repo root here, the Lua modules live under `src/src/luarocks/`.

Bump with (track the fork's branch, not upstream tags):

```
git subtree pull --prefix=internal/luarocks/src \
    https://github.com/tarantool/luarocks.git luarocks-3.9.2-tarantool --squash
```

`extra/` holds the only files we control: two shims preloaded ahead of upstream
modules.

- `wrapper.lua` — dispatch entry point (`exec(bin, ...)`), maps subcommands to
  `luarocks.cmd.*` and runs `cmd.run_command`.
- `hardcoded.lua` — LuaRocks build/install settings. Reads `LUAROCKS_PREFIX`,
  `LUA_BINDIR`, `LUA_INCDIR`, `TARANTOOL_DIR` via the lua engine's custom
  `os.getenv` (backed by a Go map from `Config.Tarantool`), and the current
  working dir via the Go-bound `glr_getwd()` global.

The embedded FS is declared in `embed.go`.
