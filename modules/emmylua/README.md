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
  (`Services.Tarantool`), the modules its rocks install and those of
  `--library`, and `.rocks/`, `.tarantool/` and `_build/`, where tt builds
  the project, in `ignoreGlobs`; `check`, `doc` and `ls` read every other
  Lua file of the project, those git ignores too. The project's own package,
  which `tt package build` installs into `.rocks/share/tarantool`, a library,
  goes to `ignoreDir` as absolute paths (`analyzer.OwnModules`): the Lua
  modules the `rock_manifest` of the rock of the package name, and of every
  product released under another, lists, read through go-luarocks'
  `tree.Inspect`, a directory at the top of the library given whole when no
  other rock has a module in it. Read twice, every class of the project was
  declared twice.
- `check` and `doc` always run the program on the root of the workspace:
  EmmyLua takes a path for the workspace itself, and resolves the module
  names, the relative paths of the configurations and the names in its
  report from it. A `PATH` narrows the run instead (`analyzer.Narrow`): one
  more configuration, written for the run into
  `.tarantool/emmylua/narrow-*/` and removed after it, puts every entry of
  the root that neither is a `PATH` nor leads to one into `ignoreDir`, as
  absolute paths, and reads the same entries, but `.rocks/`, `.tarantool/`
  and `_build/`, as a library: a directory of links to them laid out as in
  the root, so their modules keep their names, resolve, and are not reported
  or documented. EmmyLua applies `ignoreDir` to a library only outside it,
  and the links are not under the ignored paths. `ignoreGlobs` of the
  project match in the library as in the root; its `ignoreDir` does not
  reach the links. `fmt` runs `luafmt`
  without it. It passes `--level lua-jit`, the LuaJIT the configuration
  gives the analyzer, unless the arguments after `--` hold `--level` or the
  configuration of luafmt sets `[syntax] level`
  (`analyzer.FormatLevelConfigured`): luafmt defaults to Lua 5.5 and takes
  `--level` over its configuration, and merges no configurations, so tt
  reads the one luafmt would.
- `gitfiles.go` - the files of `fmt` with no path. In a git work tree
  (`git rev-parse --is-inside-work-tree`) that does not ignore the project
  (`git check-ignore .`), they are the Lua files `git ls-files -z --cached
  --others --exclude-standard` lists under the root: tracked, and untracked
  but not ignored. `luaFiles` keeps those luafmt's walk would take - `.lua`
  and `.luau`, no hidden name, nothing under `.git`, `node_modules` or
  `target`, no link, unless `--include-hidden` or `--follow-symlinks` follow
  `--` - and drops `_build/`, `.rocks/` and `.tarantool/` even tracked, the
  files gone from the work tree and the directories git lists: submodules
  and repositories inside the work tree, which are not the project's code.
  luafmt runs in the root (`runProgram` with a directory) on the paths
  relative to it, so that `.luafmtignore` and `--exclude` match as they do
  on a walk of the root; a relative `--config` is made absolute. The level
  is chosen per file (`analyzer.SplitFormatLevel`), so the files whose
  configuration sets it run apart, without `--level`, and the list goes in
  runs of `fmtArgBudget` bytes; every run is made, and the one with the
  highest exit code is the result. With no Lua file git sees, luafmt, which
  would read stdin, does not run. Out of git, with no git, or in a
  directory git ignores, `fmt` gives luafmt the root and
  `--exclude _build/**` (`analyzer.BuildDirGlob`): luafmt skips hidden
  directories only. A flag after `--` that formats no files (`--init`,
  `--explain-config`, `--stdin`, ...) gets no path: `--init [PATH]` took the
  root for the file to write. An `--init` with no file is given
  `luafmt.toml` of the root, and after luafmt wrote the configuration
  `analyzer.SetInitLevel` rewrites its `[syntax] level`, `"Lua55"`, to
  `"LuaJIT"`, the one line, unless `--level` follows `--`.
- `workspace.go` - where a command runs: the project `scope.FindProject` of
  `sdk/scope` finds from `Project().Dir()`, as the project commands of tt
  find theirs, when it holds the manifest, and the directory of the command
  outside every project.
- `internal/analyzer` - the releases the module knows, the download of
  their assets through `sdk/fetch`, the binary rock, the configuration,
  where the programs are, and the programs outside a project (`Tools`).

## Outside a project

`check`, `doc`, `fmt` and `ls` work outside every project too, with no
`prepare`: `analyzer.Tools` asks the rock server (`--rock-server`,
`DefaultRockServer` by default) for the `emmylua` rock of `--version` for
the host through go-luarocks (`remote.NewIndex`, which reads the
`tt-rocks-index.json`), downloads it checked against the sha256 the index
gives (`fetch.Fetcher`) and unpacks its `bin/` into
`<user cache>/tt/emmylua/programs/<sha256>/`, staged and renamed into
place. What the query resolved to is recorded in `resolved/`, so a later
run asks no server and works offline; a server that is a directory, such as
`tt emmylua rock` packs, is asked every time. The configuration goes to
`config/<key of the directory>/emmyrc.json` of the same cache, so nothing
is written into the directory. `prepare` outside a project fails and says
so. Inside a project nothing of this applies: the programs are those of
`.rocks/bin`, pinned by the lock, and `--version` and `--rock-server` are
refused.

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
