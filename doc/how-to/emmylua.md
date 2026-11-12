# Check and format Lua code with EmmyLua (experimental)

[EmmyLua](https://github.com/EmmyLuaLs/emmylua-analyzer-rust) is an analyzer
of annotated Lua: it checks code for mistakes, formats it, generates
documentation from its annotations and serves an editor as a language
server. `tt emmylua` runs its programs with what tt knows of the code: the Lua
of Tarantool, its globals, the types Tarantool ships and the modules the rocks
of the project install. Every command and flag is in the
[command reference](../reference/commands/emmylua.md).

The page is a walkthrough: run its commands in order, each from the directory
it names, starting in an empty directory that is not inside a git work tree.
The steps that need an editor or a CI service are marked as not run in it.
The output shown is that of Linux x86_64 with EmmyLua 0.30.0pre1, the release
tt takes by default; paths, hashes and timings differ on your machine, and
another release of EmmyLua may word and count its diagnostics otherwise.

You need:

- tt 3 and git;
- Tarantool 3.9 or newer on `PATH`, installed from a package or built from
  source: it installs the EmmyLua stubs of its modules into
  `share/tarantool/emmylua`. With an older Tarantool, or none, the commands
  run without those types (see [Troubleshooting](#troubleshooting));
- access to `raw.githubusercontent.com` and `github.com`, where the `emmylua`
  rock comes from.

## Set up the project

The project is a package, `kv`, with a Lua module and a script that uses it:

```console
$ mkdir kv && cd kv
$ git init -q
$ mkdir kv scripts
$ printf '.rocks/\n.tarantool/\n_build/\napi/\n' > .gitignore
```

`app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "kv"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[dependencies]

[components.kv]
path = "kv"
```

`kv/init.lua`, the module, `require('kv')`:

```lua
local checks = require('checks')
local log = require('log')

--- A store of values by string keys, on a space of Tarantool.
---@class kv.Store
---@field space string the name of the space
local Store = {}
Store.__index = Store

--- The most keys a store holds.
local LIMIT = 1000000ULL

--- Open the store on a space, creating the space if there is none.
---@param name string the name of the space
---@return kv.Store
local function open(name)
    checks('string')
    box.schema.space.create(name, {if_not_exists = true})
    box.space[name]:create_index('pk', {parts = {{1, 'string'}}, if_not_exists = true})
    return setmetatable({space = name}, Store)
end

--- Put a value under a key.
---@param key string
---@param value any
function Store:put(key, value)
    if box.space[self.space]:len()>=LIMIT then
        error('the store is full')
    end
    box.space[self.space]:replace({key, value})
    log.info('put %s', key)
end

return {open = open, LIMIT = LIMIT}
```

`scripts/fill.lua`, a script with a mistake in it:

```lua
local kv = require('kv')

box.cfg({})

local store = kv.open('demo')
for i = 1, 10 do
    store:set('key-' .. i, 'value ' .. i)
end
```

The component `kv` is the code of the package: `tt package build` lays out
its files as `require('kv')`. `scripts/` is code of the project that the
package does not carry; EmmyLua checks both. `api/` is where the
documentation goes [below](#generate-the-documentation).

## Add EmmyLua to the project

In the project directory, the outer `kv/`:

```console
$ tt emmylua prepare
declared emmylua 0.30.0pre1 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/ in [dev_dependencies]
  emmylua 0.30.0pre1-1
installing the rocks
EmmyLua 0.30.0pre1 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
next: tt emmylua check
$ tail -2 app.manifest.toml
[dev_dependencies]
emmylua = { version = "0.30.0pre1", registry = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/" }
$ grep -A3 'lock.dev_dependencies' app.manifest.lock
[[lock.dev_dependencies]]
name = 'emmylua'
version = '0.30.0pre1-1'
source = 'registry'
$ ls -1 .rocks/bin
emmylua_check
emmylua_doc_cli
emmylua_ls
luafmt
```

`tt emmylua prepare` declares the binary rock `emmylua` as a
[dev dependency](../reference/app-manifest.md#dependencies-and-dev_dependencies),
from the rock index of
[tarantool-contrib/rocks-repo](https://github.com/tarantool-contrib/rocks-repo):
the tools of the project, which no archive of the package carries. It
resolves the lock, which pins the version of the rock and the sha256 of its
file for every platform (the `artifacts` line after those shown), and
installs the rocks, so the four programs of EmmyLua are in `.rocks/bin`: the
checker, the doc generator, the language server and the formatter. A
declaration already in the manifest is left as it is. `--version` names
another release the server serves, `--rock-server` another server, and
`--from` packs a local build of EmmyLua instead; see
[`tt emmylua prepare`](../reference/commands/emmylua.md#tt-emmylua-prepare).
How a rock index and the lock work together is in
[Rock servers](../concepts/rock-servers.md).

## Check the code

```console
$ tt emmylua check
--- scripts/fill.lua [2 warnings]
warning: function `store:set` may be nil [need-check-nil]
  --> scripts/fill.lua:7:5

  6 | for i = 1, 10 do
  7 |     store:set('key-' .. i, 'value ' .. i)
  8 | end

warning: Undefined field: `set` [undefined-field]
  --> scripts/fill.lua:7:11

  6 | for i = 1, 10 do
  7 |     store:set('key-' .. i, 'value ' .. i)
  8 | end


--- kv/init.lua [1 warning]
warning: Cannot resolve module `log`. [unresolved-require]
  --> kv/init.lua:2:21

  1 | local checks = require('checks')
  2 | local log = require('log')
  3 |



Summary
  3 warnings

Check completed with warnings
Check finished
```

`tt emmylua check` checks every Lua file of the project, and reports them in
an order of its own, which may differ on your machine. It exits with 0 on
warnings and with 1 on an error; `--warnings-as-errors` fails on a warning
too. Two things are reported:

- `store:set`: the store has no method `set`, it is `put`. EmmyLua knows,
  from the annotations of `kv/init.lua`, that `kv.open` returns a `kv.Store`.
- `log`: the stubs of Tarantool 3.9 describe the modules `checks` and
  `metrics`, so `require('checks')` resolves, though the stub gives the
  module no type, so a call of `checks` is not checked; the built-in
  modules, `log`, `fiber` and the others, have no stubs yet. `box` is no warning: tt declares it, and the
  other globals of Tarantool, to EmmyLua.

Paths, directories or files of the project, narrow what is reported:

```console
$ tt emmylua check scripts/fill.lua
--- scripts/fill.lua [2 warnings]
warning: function `store:set` may be nil [need-check-nil]
  --> scripts/fill.lua:7:5

  6 | for i = 1, 10 do
  7 |     store:set('key-' .. i, 'value ' .. i)
  8 | end

warning: Undefined field: `set` [undefined-field]
  --> scripts/fill.lua:7:11

  6 | for i = 1, 10 do
  7 |     store:set('key-' .. i, 'value ' .. i)
  8 | end



Summary
  2 warnings

Check completed with warnings
Check finished
```

The rest of the project is still read, as a library, so `require('kv')`
resolves and `store:set` is still found; the module names and the paths of
the report and of `.emmyrc.json` are those of the root. A path outside the
project is refused. `tt emmylua doc` takes paths the same way.

Fix the script. `scripts/fill.lua`:

```lua
local kv = require('kv')

box.cfg({})

local store = kv.open('demo')
for i = 1, 10 do
    store:put('key-' .. i, 'value ' .. i)
end
```

For `log`, describe what the project calls of it in a stub, a file of
annotations that EmmyLua reads and nothing runs. `---@meta log` makes it the
module `log`. The stubs of the project go into a directory of their own:

```console
$ mkdir types
```

`types/log.lua`:

```lua
---@meta log

--- The logger of Tarantool: the functions of it this project calls.
local log = {}

--- Write a message at the INFO level, formatted as string.format formats it.
---@param fmt string
---@param ... any
function log.info(fmt, ...) end

return log
```

and the configuration of EmmyLua for the project, `.emmyrc.json`, which
makes `types/` a library, read for its types and not checked or documented
as code of the project:

```json
{
  "workspace": {
    "library": ["types"]
  }
}
```

```console
$ tt emmylua check
No issues found
Check finished
```

tt runs EmmyLua with a configuration of its own first (see
[What tt gives the programs](#what-tt-gives-the-programs)), then the
project's `.luarc.json`, `.emmyrc.json` and `.emmyrc.lua`. EmmyLua merges
them: objects merge and lists join, so `library` above adds `types/` to the
libraries tt gives.

Build the package and check again:

```console
$ tt package build
$ tt emmylua check
No issues found
Check finished
```

`tt package build` copies the files of the component into `_build/` and lays
the package out in `.rocks/share/tarantool/kv/`. tt leaves `_build/` out of
the code EmmyLua reads. It reads `.rocks/share/tarantool` as a library, for
the modules of the rocks the project depends on, but leaves the project's
own package out of it, so the module is not read twice.

## Format the code

The formatter is `luafmt`. It reads its own configuration, `.luafmt.toml`,
not that of the analyzer. This one keeps tables without spaces inside the
braces, as the code of Tarantool writes them. `.luafmt.toml`:

```toml
[spacing]
space_inside_braces = false
```

```console
$ tt emmylua fmt
diff --git a/kv/init.lua b/kv/init.lua
index 8f517bc411362f42a4e0f4ec06a219b4cbd0abc2..2b21b6273f3be20a33e0caa189febf3c1734c3b0 100644
--- a/kv/init.lua
+++ b/kv/init.lua
@@ -21,10 +21,10 @@
 end
 
 --- Put a value under a key.
----@param key string
+---@param key   string
 ---@param value any
 function Store:put(key, value)
-    if box.space[self.space]:len()>=LIMIT then
+    if box.space[self.space]:len() >= LIMIT then
         error('the store is full')
     end
     box.space[self.space]:replace({key, value})
1 file(s) would be reformatted, 2 file(s) OK
```

With no flag, `tt emmylua fmt` checks: it prints how the files would change
and exits with 1 if any would, 2 if one could not be formatted. `--write`
rewrites them:

```console
$ tt emmylua fmt --write
$ tt emmylua fmt
0 file(s) would be reformatted, 3 file(s) OK
```

The arguments after `--` go to luafmt as they are. Its `--explain-config`
shows what a file is formatted with:

```console
$ tt emmylua fmt -- --explain-config kv/init.lua
config file: .luafmt.toml
resolved for: kv/init.lua
settings differing from defaults:
  spacing.space_inside_braces = false
  syntax.level = LuaJIT
```

`spacing` comes from `.luafmt.toml`, and `syntax.level` from tt: luafmt
parses Lua 5.5 unless told otherwise, and fails on what only LuaJIT has, such
as the literal `1000000ULL` of `kv/init.lua`. tt passes `--level lua-jit`
unless the configuration luafmt finds for the file sets `[syntax] level`, or
the arguments after `--` give `--level` or `--config`.

With no path, in a git work tree, `tt emmylua fmt` formats the Lua files git
sees under the project: those it tracks, and those it does not track but
does not ignore either. That is why it formatted three files: `.rocks/`,
`.tarantool/` and `_build/` are ignored, and they are left out even when they
are tracked. A path is formatted as given, ignored or not:

```console
$ printf 'local x=1\n' > scratch.lua
$ echo scratch.lua >> .git/info/exclude
$ tt emmylua fmt
0 file(s) would be reformatted, 3 file(s) OK
$ tt emmylua fmt scratch.lua
diff --git a/scratch.lua b/scratch.lua
index 8e14037c58d17f33aed468463436b8b8b22bea4d..e728b6407be23b4963820447feda2060d3382c49 100644
--- a/scratch.lua
+++ b/scratch.lua
@@ -1 +1 @@
-local x=1
+local x = 1
1 file(s) would be reformatted, 0 file(s) OK
$ rm scratch.lua
```

Out of a git work tree, in a directory git ignores, or without git, it walks
the whole directory instead, `_build/` aside. luafmt skips hidden files and
directories itself, and those `.luafmtignore` lists.

## Generate the documentation

```console
$ tt emmylua doc -f markdown -o api
Documentation markdown exported to "api/docs"
$ find api/docs -name '*.md' | sort
api/docs/index.md
api/docs/modules/kv.md
api/docs/types/kv.Store.md
```

`tt emmylua doc` writes the documentation of the annotated code: the module
`kv` with `open` and `LIMIT`, and the class `kv.Store` with its methods and
fields. By default it is an HTML site in `./output`; `-f markdown` writes
Markdown pages and an `api/mkdocs.yml` for them, `-f json` one JSON file. The
stub `types/log.lua` is a library, so it is not documented. Options of
`emmylua_doc_cli` such as `--site-name` go after `--`.

## Use it in an editor

`tt emmylua ls` is the language server, run by the editor on its standard
streams. It runs `emmylua_ls` with the configuration of `tt emmylua check`,
so the editor shows the same diagnostics, and completes and documents the
modules of Tarantool, of the rocks and of the project. Run in a terminal, it
says that it waits for an editor; Ctrl+C stops it.

For Neovim 0.11 or newer, in `~/.config/nvim/init.lua` (not run here):

```lua
vim.lsp.config('emmylua_ls', {
    cmd = {'tt', 'emmylua', 'ls'},
    filetypes = {'lua'},
    root_markers = {'app.manifest.toml', '.emmyrc.json', '.git'},
})
vim.lsp.enable('emmylua_ls')
```

Neovim starts the server in its own working directory unless `cmd_cwd`
names another, and tt finds the project from there, as every project
command does: start Neovim inside the project. Outside every project the
server works too, as [below](#outside-a-project).

An editor that runs `emmylua_ls` itself, such as the EmmyLua extension of
VS Code, needs the configuration tt writes, through the variable
`EMMYLUALS_CONFIG`. `tt emmylua config` writes it and prints its path:

```console
$ tt emmylua config
/home/me/work/kv/.tarantool/emmylua/emmyrc.json
$ cat .tarantool/emmylua/emmyrc.json
{
  "$comment": "Written by tt emmylua for this machine, anew on every run: put the settings of the project into .emmyrc.json.",
  "diagnostics": {
    "globals": [
      "box",
      "misc",
      "package",
      "tonumber64",
      "utf8",
      "_TARANTOOL"
    ]
  },
  "runtime": {
    "version": "LuaJIT"
  },
  "workspace": {
    "ignoreDir": [
      "/home/me/work/kv/.rocks/share/tarantool/kv"
    ],
    "ignoreGlobs": [
      ".rocks/**/*.lua",
      ".tarantool/**/*.lua",
      "_build/**"
    ],
    "library": [
      "/usr/share/tarantool/emmylua",
      "/home/me/work/kv/.rocks/share/tarantool"
    ]
  }
}
```

and the editor is started with it (not run here):

```console
$ EMMYLUALS_CONFIG=$(tt emmylua config) code .
```

A VS Code that is already running keeps the environment it started with:
quit it first.

The file holds the paths of this machine, so it lives in `.tarantool/`,
which is not committed. The other commands write it anew on every run;
`tt emmylua config` is for when nothing else has, and after the project or
its Tarantool changed.

## Check in CI

A pipeline needs no `tt emmylua prepare`: the lock pins the `emmylua` rock,
and `tt package fetch` installs the rocks it pins, dev ones too, without
resolving anything again. Commit the project, and run the job's commands in
a fresh clone:

```console
$ git add -A
$ git commit -q -m 'kv: a store on a space'
$ cd ..
$ git clone -q kv kv-ci && cd kv-ci
$ tt package fetch
$ tt emmylua check --warnings-as-errors
No issues found
Check finished
$ tt emmylua fmt
0 file(s) would be reformatted, 3 file(s) OK
```

Both fail the job on a finding: `check` with `--warnings-as-errors` on any
warning, `fmt` on any file it would change. `-f github` reports the
diagnostics of `check` as annotations of GitHub Actions, `-f gitlab` as a
code quality report of GitLab (with `-- --output FILE`), and `-f sarif` for
code scanning. `tt package fetch` checks the rock it downloads against the
sha256 the lock pins for the platform of the job.

A GitHub Actions workflow, `.github/workflows/lua.yml` (not run here):

```yaml
name: lua
on: [push, pull_request]
jobs:
  emmylua:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: tarantool/setup-tarantool@v4
        with:
          tarantool-version: '3.9'
      - name: Install tt
        run: |
          curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - run: tt package fetch
      - run: tt emmylua check --warnings-as-errors -f github
      - run: tt emmylua fmt
```

The job checks against the Tarantool it installs: one without the stubs,
older than 3.9 say, reports `require('checks')` as unresolved, and
`--warnings-as-errors` fails on it. See [Install](../get-started/install.md)
for the installer of tt.

## Outside a project

`tt emmylua check`, `doc`, `fmt` and `ls` work in any directory, with no
manifest and no `prepare`: a script, a checkout of a rock that is not a tt
package. In the starting directory:

```console
$ cd ..
$ mkdir snippets && cd snippets
```

`hello.lua`:

```lua
local function greet(name)
    return 'Hello, '..nme
end

local calls = 0ULL
calls = calls + 1
print(greet('tt'), calls)
```

```console
$ tt emmylua check
outside a project: fetching EmmyLua 0.30.0pre1-1 for linux-x86_64 (21.4 MB) from https://github.com/tarantool-contrib/rocks-repo/releases/download/emmylua-0.30.0pre1-1/emmylua-0.30.0pre1-1.linux-x86_64.rock
--- hello.lua [1 error, 1 hint]
hint: name is never used, if this is intentional, prefix it with an underscore: _name [unused]
  --> hello.lua:1:22

  1 | local function greet(name)
  2 |     return 'Hello, '..nme

error: undefined global variable: `nme` [undefined-global]
  --> hello.lua:2:23

  1 | local function greet(name)
  2 |     return 'Hello, '..nme
  3 | end



Summary
  1 error
  1 hint

Check failed
Error: "exit code: 1"
$ tt emmylua fmt hello.lua
diff --git a/hello.lua b/hello.lua
index fbea083d59a45c492fa52dbc1bea179530ef19a0..0c155512782bbfa93aeb8cac3b09b5e9d3c1e306 100644
--- a/hello.lua
+++ b/hello.lua
@@ -1,5 +1,5 @@
 local function greet(name)
-    return 'Hello, '..nme
+    return 'Hello, ' .. nme
 end
 
 local calls = 0ULL
1 file(s) would be reformatted, 0 file(s) OK
$ ls -1 ~/.cache/tt/emmylua/programs/*
emmylua_check
emmylua_doc_cli
emmylua_ls
luafmt
$ ls -a
.
..
hello.lua
```

The first run fetches the `emmylua` rock of the host from the rock server,
checks it against the sha256 the server's index gives, and unpacks its
programs into the user cache, `tt/emmylua/` of `~/.cache`
(`~/Library/Caches` on macOS); the later runs take them from there and ask no
server, so they work offline. The configuration tt writes goes there too,
one file for each directory, so nothing is written into the directory. The
layout of that cache is in [Layout](../reference/layout.md#the-user-cache).
`--version` runs another release, and `--library DIR` reads the modules of a
directory as a library, a checkout of a rock the code uses, say. In a
project, the release is the one the lock pins, and `--version` and
`--rock-server` are refused.

The directory of the command is the root here: `check` and `doc` take paths
inside it, as in a project, and refuse the others. `fmt` takes any file or
directory.

### Work offline

On a machine without network access, the first run has no server to fetch
from. `--rock-server` names another one, a directory of rocks too. Make one
where the network is, with the rock of this platform from the same index:

```console
$ tt package download emmylua --registry https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/ --dir ../emmylua-rocks
/home/me/work/emmylua-rocks/emmylua-0.30.0pre1-1.linux-x86_64.rock
indexed 1 rock(s) in /home/me/work/emmylua-rocks
```

Copy the directory to the machine, and give it to the commands there:

```console
$ tt emmylua fmt --rock-server ../emmylua-rocks hello.lua
diff --git a/hello.lua b/hello.lua
index fbea083d59a45c492fa52dbc1bea179530ef19a0..0c155512782bbfa93aeb8cac3b09b5e9d3c1e306 100644
--- a/hello.lua
+++ b/hello.lua
@@ -1,5 +1,5 @@
 local function greet(name)
-    return 'Hello, '..nme
+    return 'Hello, ' .. nme
 end
 
 local calls = 0ULL
1 file(s) would be reformatted, 0 file(s) OK
```

A directory is read on every run, so a rock put there anew is taken. Here
the user cache already holds the programs of that rock, which has the same
sha256 as the one fetched above, so nothing is unpacked; on a machine where
it does not, the first run unpacks it and says so. A project works offline
from its `.rocks/`, or from a mirror of its lock: see
[A mirror for offline builds](../concepts/rock-servers.md#a-mirror-for-offline-builds).

## What tt gives the programs

`check`, `doc` and `ls` run with a configuration tt writes on every run, in
`.tarantool/emmylua/emmyrc.json` of a project and in the user cache outside
one (the file `tt emmylua config` printed above):

- `runtime.version` is `LuaJIT`, the Lua of Tarantool: its syntax, such as
  `0ULL`, and its standard library, `bit` and `jit` among them.
- `diagnostics.globals` are the globals Tarantool defines beyond LuaJIT:
  `box`, `misc`, `package`, `tonumber64`, `utf8` and `_TARANTOOL`.
- `workspace.library` are the stubs of the Tarantool tt runs the project
  with, `share/tarantool/emmylua` of its prefix, and the modules the rocks of
  the project install, `.rocks/share/tarantool` and `.rocks/share/lua/5.1`;
  then the directories of `--library`.
- `workspace.ignoreGlobs` leave `.rocks/`, `.tarantool/` and `_build/` out of
  the code of the project. `_build/` is where `tt package build` builds the
  project, out of copies of its sources and its path dependencies: read as
  code, every module would be there twice.
- `workspace.ignoreDir` leaves the project's own package out of the library
  `.rocks/share/tarantool`, where `tt package build` installs it: the
  modules its rock installs, absolute paths, `.rocks/share/tarantool/kv`
  here. Before a build there is none.

The project's `.luarc.json`, `.emmyrc.json` and `.emmyrc.lua` are read after
it and add to it, as [above](#check-the-code). `check`, `doc` and `ls` read
every other Lua file of the project, those git ignores too: `ignoreGlobs`
or `ignoreDir` of `.emmyrc.json` leave more out. With paths, `check` and
`doc` add one more configuration for the run, which leaves the rest of the
root out of the code and reads it as a library instead.

`fmt` runs luafmt without that configuration: luafmt reads `.luafmt.toml`
or `luafmt.toml`, the nearest one above each file, and `.luafmtignore`. What
tt adds is the level, `--level lua-jit`, for the files whose configuration
sets no `[syntax] level`, when the arguments after `--` give neither
`--level` nor `--config`; and the files of a run with no path, those git
sees, or the whole directory but `_build/` out of git, as
[above](#format-the-code).

## Troubleshooting

### A level of Lua 5 breaks the formatting of LuaJIT code

`luafmt --init`, run by itself, writes the whole default configuration,
`[syntax] level = "Lua55"` included, and a configuration taken from a
project of plain Lua may set its level too. Once a configuration sets the
level, tt passes no `--level`, and the code that only LuaJIT parses fails.
`tt emmylua fmt -- --init` writes the configuration into the root, the
directory of the command outside a project, and sets the level of LuaJIT in
it. In `snippets/`:

```console
$ tt emmylua fmt -- --init
Wrote /home/me/work/snippets/luafmt.toml
   • set [syntax] level = "LuaJIT" in /home/me/work/snippets/luafmt.toml, the Lua of Tarantool, for "Lua55" luafmt writes module=emmylua
$ head -2 luafmt.toml
[syntax]
level = "LuaJIT"
```

The level of Lua 5.5, as `luafmt --init` writes it:

```console
$ printf '[syntax]\nlevel = "Lua55"\n' > luafmt.toml
$ tt emmylua fmt hello.lua
hello.lua: syntax error at 5:15: unexpected character 'U' after number literal
0 file(s) would be reformatted, 0 file(s) OK, 1 file(s) could not be formatted
```

Remove the `[syntax]` table from the file, or set the level of LuaJIT,
which the configuration spells `LuaJIT`:

```console
$ printf '[syntax]\nlevel = "LuaJIT"\n' > luafmt.toml
$ tt emmylua fmt hello.lua
diff --git a/hello.lua b/hello.lua
index fbea083d59a45c492fa52dbc1bea179530ef19a0..0c155512782bbfa93aeb8cac3b09b5e9d3c1e306 100644
--- a/hello.lua
+++ b/hello.lua
@@ -1,5 +1,5 @@
 local function greet(name)
-    return 'Hello, '..nme
+    return 'Hello, ' .. nme
 end
 
 local calls = 0ULL
1 file(s) would be reformatted, 0 file(s) OK
```

### `unknown variant` of the level

The configuration and the flag spell the level differently: `LuaJIT` in
`[syntax] level`, `lua-jit` for `--level`. The spelling of the flag in the
configuration fails every file:

```console
$ printf '[syntax]\nlevel = "lua-jit"\n' > luafmt.toml
$ tt emmylua fmt hello.lua
Failed to format hello.lua: syntax error: failed to parse config luafmt.toml: TOML parse error at line 2, column 9
  |
2 | level = "lua-jit"
  |         ^^^^^^^^^
unknown variant `lua-jit`, expected one of `Lua51`, `Lua52`, `Lua53`, `Lua54`, `Lua55`, `LuaJIT`, `LuaJIT3`

0 file(s) would be reformatted, 0 file(s) OK
$ rm luafmt.toml
```

The other way round, `tt emmylua fmt -- --level LuaJIT` is refused by
luafmt with `invalid value 'LuaJIT' for '--level <LEVEL>'`.

### `Cannot resolve module` for a module of Tarantool

The stubs of Tarantool 3.9 describe `checks` and `metrics` only; a built-in
module such as `log` or `fiber` is reported as `unresolved-require`. Describe
what the code calls of it in a stub, as `types/log.lua`
[above](#check-the-code), or turn the diagnostic off in `.emmyrc.json` with
`"diagnostics": {"disable": ["unresolved-require"]}`, which hides the
misspelled modules of the project too.

With no Tarantool, or one without stubs, `check`, `doc`, `ls` and `config`
warn on stderr, and `checks` and `metrics` are unresolved as well:

```text
   ⚠ no Tarantool found: checking without the types of the Tarantool API module=emmylua
```

or, for a Tarantool without stubs, `Tarantool 3.8.1 has no EmmyLua stubs:
checking without the types of the Tarantool API`.

A Tarantool older than 3.9 has no stubs, and neither has one
`tt binary install` installed, which keeps the executable and the headers
only. tt takes the Tarantool of the project as the other commands do; see
[Manage binaries](binaries.md#install-and-switch).

### `not in the workspace`

```text
   ⨯ not in the workspace: /home/me/work/snippets is not in /home/me/work/kv
```

`check` and `doc` take paths inside the root they run on: the project, or
outside every project the directory of the command. Run them from the
directory of the code, or give the path to `fmt`, which takes any.

### `EmmyLua is not installed in the project`

```text
   ⨯ EmmyLua is not installed in the project: no emmylua_check in .rocks/bin; the lock pins emmylua: run tt package fetch to install the rocks it pins
```

The lock pins EmmyLua, but `.rocks/` is not installed: a fresh clone, say.
`tt package fetch` installs the rocks the lock pins. In a project whose lock
does not pin it, the message says to run `tt emmylua prepare`, which
declares the rock first.
