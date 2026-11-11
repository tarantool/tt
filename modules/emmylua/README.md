# The emmylua module

`tt emmylua` checks, documents and formats the Lua code of a project with
[EmmyLua](https://github.com/EmmyLuaLs/emmylua-analyzer-rust), the analyzer
of annotated Lua, and runs its language server for an editor. It is a tt
module: `emmylua.New` is its constructor, and tt's main package lists it in
the table of modules it hands to the core.

This directory is a Go module of its own,
`github.com/tarantool/tt/modules/emmylua`. Its `go.mod` requires the tt SDK
(`github.com/tarantool/tt/sdk` and `github.com/tarantool/tt/sdk/fetch`) and
never tt's root module, so the compiler refuses any import of tt's
internals: the SDK is all the module sees. Inside the tt repository the SDK
modules come from their directories through `replace`.

- `emmylua.go`, `prepare.go`, `run.go` - the commands. `prepare` declares
  the binary rock `emmylua`, which carries the programs of EmmyLua -
  `emmylua_check`, `emmylua_doc_cli`, `emmylua_ls` and `luafmt` - in
  `[dev_dependencies]` and installs the rocks, all through the project of the
  SDK (`Project.AddDependency`, `Project.Install`), so the programs are in
  `.rocks/bin`. `rock` packs the rocks of a release for a rock server.
  `check`, `doc`, `fmt` and `ls` run the programs with the configuration
  `config` writes to `.tarantool/emmylua/emmyrc.json`: LuaJIT, the globals of
  Tarantool, the EmmyLua stubs of the Tarantool the project runs on
  (`Services.Tarantool`) and the modules its rocks install.
- `internal/analyzer` - the releases the module knows, the download of
  their assets through `sdk/fetch`, the binary rock, the configuration and
  where the programs are.

## Where the rock comes from

`tt emmylua prepare` declares the rock from a rock server:
`analyzer.DefaultRockServer`, the `tt-rocks-index.json` of the `index`
branch of
[tarantool-contrib/rocks-repo](https://github.com/tarantool-contrib/rocks-repo),
served by `raw.githubusercontent.com`, unless `--rock-server` names another.
The index names the rock of every platform on the release pages of that
repository with its sha256, so the lock of the project pins them all and an
install checks the rock of its host. Any version the server serves is
declared: tt does not need to know it.

The module still packs the rock itself in two cases: `prepare --from`, which
packs a local build as the rock of the host into `.tarantool/registry` and
declares it from there, and `rock`, which packs the rocks a server serves -
what the CI of rocks-repo publishes. Both write the rockspec from the
releases the module knows - their git refs, the md5 of their sources and the
sha256 of the assets of every platform - a table in
`internal/analyzer/releases.go`; `TT_EMMYLUA_RELEASES` names a JSON file of
others.
