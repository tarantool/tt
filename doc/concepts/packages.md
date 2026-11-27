# Packages

A package is an application described by `app.manifest.toml`: its name, the
versions of Tarantool and tt it runs on, its dependencies, the components it
is built from and the products it is packed as. A package is developed in a
[project](projects-and-scopes.md), packed into an archive and installed from
it.

## The manifest

```toml
manifest_version = "0.1"

[package]
name = "kv-api"

[platform]
tarantool = ">=3.0.0"          # [ce] by default; ">=3.3.0[ee]" for Enterprise
tt = ">=3.0.0"

[dependencies]
checks = ">=3.0.0"             # a rock from the rock servers
mylib = { source = "path", path = "../mylib" }

[dev_dependencies]
luatest = "*"

[components.app]
path = "kv"
```

`tt new` writes a skeleton; `tt package add` and `tt package remove` edit
`[dependencies]` in place, keeping comments and order, and with `--dev`
`[dev_dependencies]`. `tt package add NAME` of a name declared already
keeps its constraint; `tt package add NAME CONSTRAINT` changes it. An
unknown field is an
error, so a typo does not go unnoticed. Every field is in the
[reference](../reference/app-manifest.md).

## The lock

`app.manifest.lock`, next to the manifest, pins the exact version of every
dependency, direct and transitive. Commit it: a build of the same commit
installs the same versions anywhere. For a rock from a server with a
[rock index](rock-servers.md#a-rock-index), it pins the sha256 of the files
of the version as well, and an install checks the file of its platform
against it.

The lock changes only when asked to:

- `tt package add`, `tt package remove` and `tt package resolve` resolve the
  lock again for what the manifest declares, keeping every version the lock
  holds. Only what was added, and what it requires, can move. `add` and
  `remove` write the manifest only once the lock resolved: when the
  resolution fails, for a rock no server has say, both files are left as
  they were.
- `tt package update [NAME]` takes the newest versions the constraints
  allow, for every dependency or for NAME only.
- `tt package build` and `tt package pack` resolve a lock that no longer
  matches the manifest, keeping its versions. With `--locked` they fail
  instead, which is what CI wants.

A version that tracks a branch, `scm-1` or `dev-1`, is taken only when the
constraint names it or the rock has no other, and resolving to it warns
that the lock will not pin fixed code. So is a version tt made from
uncommitted changes, `1.2.0.dirty-1`: every such build of a commit has it.
The version tt makes from a commit, `1.2.1.dev.3.gabc1234-1` or
`0.0.0.dev.36.g7170ebf-1`, names that commit and counts as a release.

The lock matches the manifest by `manifest_hash`, a hash of what the
manifest says - its tables, keys and values - and not of the bytes of the
file. Comments, blank lines and indentation, the order of keys and tables,
the quoting of a string and the form of a table (inline, dotted keys) do not
count, so such an edit leaves the lock current. A changed key or value
makes the lock out of date, one that pins nothing, such as `description`,
included; so does a lock of a tt that hashed the manifest another way.
`tt package resolve` then writes the hash anew and, when no version moved,
says what it rewrote in the lock instead of finding it up to date.

`tt package deps` shows what the manifest declares and what the lock
resolved it to, with no network.

The dependencies come from rock servers; see [Rock servers](rock-servers.md).

## Components and products

A component is a part of the package: a directory of files, with an
optional build.

A component lays its files out under `.rocks/share/tarantool/<namespace>/`,
`<namespace>` being the package name unless the component says otherwise.
A namespace nests with `/`: the files of `namespace = "myapp/native"` are
in `myapp/native/`, and `require` names them `myapp.native.<name>` (see
[`[components.NAME]`](../reference/app-manifest.md#componentsname)). The
files of a component are the `*.lua` and `*.so` of its `path` unless
`include` lists others; a `go` component lays out only what `include`
lists. A build backend makes what its block asks for, before the layout,
and one component may make all of it:

- a **module**, into `.rocks/lib/tarantool/<namespace>/`: a native module
  of `c`, `lua-c`, `cargo` or `zig` with `module`, or of `cmake`, `make` and
  `shell` in `output`;
- a **data file**, into `.rocks/share/tarantool/<namespace>/`: a
  WebAssembly component, say, see
  [Run WebAssembly components](../how-to/wasm-components.md), an entry of
  `output` with `to = "share"`;
- a **program**, into `.rocks/bin/`: of `go`, `cargo` or `zig` with `bin`, or an
  entry of `output` with `to = "bin"`. An
  [external process](../how-to/external-processes.md) of the application
  runs it.

[Build a module in Rust](../how-to/rust-modules.md) describes `cargo`, and
[Build a module in Zig](../how-to/zig-modules.md) `zig`. The
`kind` of a component, `"module"` or `"executable"`, is deprecated: tt
checks what it says against what the build makes.

A native module is `<module>.so` in `.rocks/lib/tarantool/<namespace>/`, and
`<module>.dylib` on macOS, where Tarantool looks for no other name: the `c`,
`lua-c`, `cargo` and `zig` backends name it so, `cmake` copies a `.so` it built as
`.dylib` there, and a `.dylib` a component lays out goes there as a `.so`
does.

A product is a set of components built and packed together. A manifest
without `[products]` has one implicit product, `default`, made of every
component.

## Building

`tt package build` resolves the lock, installs the dependencies into
`.rocks/`, runs the build backends of the components and lays the
components out, with `_version.lua` and `_rocks.lua` of the package.
`tt package fetch` only installs `.rocks/` from the lock, with no build.
`-j N` runs at most `N` jobs at once in each component build, one a CPU by
default; see [Jobs](../reference/app-manifest.md#jobs). `--target OS/ARCH`
builds for another platform than the machine; see
[Other platforms](#other-platforms).

The components are laid out in `_build/rock/<product>/` and installed into
`.rocks/` as a rock of the package's name, at its version: the manifest of
`.rocks/` lists their files as it lists those of the dependencies, and a
build at another version replaces the rock. A file of the package that a
dependency also installs fails the build.

`_build/rock/<product>/` stays between builds, and
`_build/rock/<product>.json` records which component put each of its files
there. A build removes from it, and so from `.rocks/`, what no component
owns any more: a file deleted from the sources, the files of a component
no longer in the product. What a skipped incremental build wrote stays, and
so does what the other components put there when `--component` builds one.
A file a backend wrote into `TT_OUTPUT_DIR` once and leaves alone since
stays its own, as an up-to-date `make` target does; `--rebuild` starts from
an empty `_build/rock/<product>/`, so only what the build writes again
stays. A component whose `build` block changed is built as `--rebuild`
builds it: what its last build made leaves `_build/rock/<product>/` before
the build runs, `TT_BUILD_DIR` is emptied, and only what the build makes
stays - a module renamed in the block, `module = "hello"` made `module =
"greet"`, leaves no `hello.so` behind. A build also removes from
`.rocks/` the versions of the dependencies the lock pins other than the one
it pins, and repairs a `.rocks/` an older tt left out of step with its
manifest.

- A build backend finds the programs of the project's `.tarantool/bin` -
  what `tt binary` and `tt wasm prepare` installed - ahead of `PATH`, with or
  without `.env/activate.sh`. The build of a dependency rock finds the
  project's `tarantool` there as well: a rock that runs `tarantool` by
  name, as the cmake of a rock that looks for it does, runs that one.
- A `make` or `shell` build with `incremental = true` is skipped while
  nothing it depends on changed; see [Incremental builds](#incremental-builds).
- A `.wasm` file a component lays out, or its build writes into `share/`,
  has to be a WebAssembly binary: any other fails the build, and a core
  module, which tarantool-wasm does not load, is laid out with a warning.
  The check is that of the standard module `wasm`, and a distribution
  without it makes none.
- `tt package build`, `fetch` and `pack` need no Tarantool: where tt finds
  none, neither in `.tarantool/bin` nor in `PATH`, a package of Lua builds
  and packs all the same. What is compiled against the headers of Tarantool
  fails then, saying so: a `c` or `lua-c` component, before anything is
  built, and a rock of a server with a C part. A `make`, `shell` or `cmake`
  build is not told where Tarantool is, `TT_TARANTOOL_PREFIX` and
  `TT_TARANTOOL_INCLUDE_DIR` unset, and fails on its own if it needs the
  headers. A Tarantool whose headers tt does not find
  ([where it looks](../how-to/binaries.md#headers-for-rocks-with-a-c-part))
  builds the same, and a `c` or `lua-c` component fails naming the
  directories tt looked into.

The version of a package is, in order:

1. the line of a `VERSION` file next to the manifest;
2. the nearest version tag of git, `v1.2.0` or `1.2.0`: `1.2.0` on the tag,
   `1.2.1-dev.3+gabc1234` three commits after it;
3. `0.0.0-dev.<N>+g<sha>` without a tag.

A tag with a `v` is a version as before, `v1.2` too; a tag without one only
in full SemVer, `1.2.0` or `1.2.0-rc.1`, since a bare number as often names
a date or a build. Any other tag is passed over for the version tag before
it. Of the version tags on one commit the highest version counts, and of
`v1.2.0` and `1.2.0` the tag is `v1.2.0`, whichever of them is annotated or
newer.

Changes not committed, but for the lock, add `dirty` to the build
metadata: `1.2.0+dirty`.

A project in a subdirectory of its repository - one of a monorepo, or a
path dependency inside the repository of the project that depends on it -
is versioned by its directory alone. The version is that of the last commit
that changed the directory, and only a change under it adds `dirty`: a
commit or a change elsewhere in the repository does not move the version,
nor make the lock of a project that depends on it by path out of date. A
tag counts on that commit or before it, not on a later commit that left the
directory alone: to release the project, tag a commit that changes it.
`<N>` counts the commits in the history of that commit, the ones elsewhere
too, as the commits after a tag do. A project at the root of its repository
is versioned by `HEAD` and the whole tree.

The code reads its version as `require('<name>._version').version`; set
`generate_version_lua = false` in `[package]` to leave `_version.lua` out.
The underscore marks a file tt generates, so a module of the package may be
named `version.lua` or `rocks.lua`.

### The rocks of the build

Beside `_version.lua` the build writes `_rocks.lua`, the rocks the package
was built with: the closure of the product the lock pins, the dependencies
of the dependencies included, with no `[dev_dependencies]`. It is a module
of data alone, the same for the same lock and version, with no time in it:

```lua
-- Generated by tt package build. Do not edit.
return {
    package = { name = "myapp", version = "0.0.0-dev.0", commit = "", dirty = false },
    product = "default",
    rocks = {
        ["my-helper"] = { version = "0.3.0-1", source = "path" },
        stat          = { version = "0.3.2-1", source = "registry" },
    },
}
```

`package` is the name of the package and its version, as `_version.lua`
has it; `product` the product built; `rocks` every rock of the closure by
name, with the version and the source, `registry` or `path`, of the lock.
A path dependency with neither a manifest nor a rockspec, which installs
nothing, is listed with the version `""`; a package with no dependency has
`rocks = {}`. A running application reads it as `require('<name>._rocks')`,
an instance of the project over iproto too:

```console
$ echo "return require('myapp._rocks').rocks.stat" | tt connect myapp:instance001
---
- version: 0.3.2-1
  source: registry
...
```

Only the package itself gets `_rocks.lua`, not the rock of a path dependency
or of a source rock tt builds: their rocks are the closure of the package.
It is in the `.tt` archive, which runs the application, and not in the rock
`--format rock` packs: a project installing the rock resolves rocks of its
own. A component that lays out a `_rocks.lua` of its own into the namespace
of the package fails the build; set `generate_rocks_lua = false` in
`[package]` to leave the generated one out, which `generate_version_lua`
does not do.

### Incremental builds

A `make` or `shell` build of a component, whatever it makes, can be
skipped when nothing it depends on changed since its last build:

```toml
[components.calc.build]
backend = "shell"
command = "tarawasm"
args = ["all"]
incremental = true
```

Before it builds such a component, `tt package build` hashes its inputs:

- its build block, with `env`, `cwd`, `output` and the rest;
- the files under its `path`: each regular file with its mode and content,
  and each symbolic link by where it points.

Some files are not inputs:

- the hidden ones, at any depth: `.git`, `.tarawasm`, the `.gitignore` files
  themselves;
- those of a `_build/` or a `_runtime/` directory;
- those the `.gitignore` files ignore - those of the project and of the
  directories down to the component, and those of the component, each for
  its subtree, as git reads them, but without the excludes of the git
  configuration (`core.excludesFile`, `.git/info/exclude`).

What a build writes and git does not keep - its output, its bindings - is
so not an input, and the hash stays the same from one build to the next.
Nor is the number of jobs, `-j` (see
[Jobs](../reference/app-manifest.md#jobs)): a build with other jobs is
skipped as well.

The build is skipped when the hash is the one its last successful build
recorded, in `_build/cache/<component>.json`, and what that build left is
still there:

- the files `output` lists;
- the files of the component the layout took;
- the modules, the data files and the programs it made, by the record of
  `_build/results.json`: the files it created or changed, at any depth, and
  those an earlier build made and it left alone, as a `make` target that is
  up to date does.

A build whose files in `_build/rock/` were cleared since runs again; a
cleared `.rocks/` gets the package's rock again from there. A skipped
build copies the files `output` lists into `_build/rock/` again, and the
component is laid out as after any build. `tt -V package build` logs every
component it skips.

A skipped build does not run its backend at all. A `make` target with rules
of its own is cheap when nothing changed, and may not need `incremental`:
without it, tt runs make every time, and make decides what to rebuild.

The hash does not cover everything a build depends on:

- the compilers and their versions;
- the environment tt runs in, but the `env` of the build block;
- the files outside the `path` of the component, those of a `cwd` elsewhere
  included;
- the dependencies in `.rocks/`.

After a change of these, `tt package build --rebuild`, and
`tt package pack --rebuild`, build every incremental component and record
it; removing `_build/cache/` does the same, and `tt package clean` too, for
the components it cleans.

A component whose build writes files no `.gitignore` ignores gets another
hash at every build, and is built every time, as if it were not
incremental. `tt wasm new` writes the `.gitignore` of a WebAssembly
component, and declares it incremental.

### Cleaning

`tt package clean` removes what the builds made, and nothing else:

- `_build/`, but `_build/tt.lock`: the build trees of the components
  (`TT_BUILD_DIR`), the records of the incremental builds and of what each
  component made, the stages, the builds of the path dependencies and the
  archives `tt package pack` wrote into `_build/pack/`;
- the rock of the package in `.rocks/`, by the files its rock manifest
  lists: those the build installed.

The rocks of the dependencies stay, and so do the packages
`tt package install` put into `.rocks/`, the manifest, the lock and the
files of the sources. A file the build made beside the sources stays too,
the files `output` lists among them: tt cannot tell it from a file the
project keeps. What a tool leaves there, the backend of the component
removes, if it knows how, before tt removes anything; what the project
generates itself, the command of `[hooks.clean]` in the manifest removes
(see [the reference](../reference/app-manifest.md#hookspre_build-hookspost_build-and-hooksclean)).
The next build builds every component again. Of a package of Lua whose
`pre_build` hook generates `src/gen/year.lua`, and whose clean hook removes
`src/gen/`, as in the example of the reference:

```console
$ tt package clean --dry-run
would run the clean hook
would remove _build/rock
would remove myapp 1.0.0-1 from .rocks (5 files)
  .rocks/share/tarantool/rocks/myapp/1.0.0-1/myapp-1.0.0-1.rockspec
  .rocks/share/tarantool/myapp/_rocks.lua
  .rocks/share/tarantool/myapp/_version.lua
  .rocks/share/tarantool/myapp/init.lua
  .rocks/share/tarantool/myapp/src/gen/year.lua
$ tt package clean
ran the clean hook
removed _build/rock
removed myapp 1.0.0-1 from .rocks (5 files)
$ ls -A _build
tt.lock
```

A component that builds adds its build tree and records:
`_build/component`, `_build/cache`, `_build/results.json`.

`--dry-run` removes and runs nothing; `-V` lists the files of the rock as
the dry run does. `tt package clean <component>` cleans one component: its
build tree, its records and its files in `_build/rock/`, and the same of the
builds for other platforms in `_build/target/<os>-<arch>/` with the trees
of their packages, after what its
backend cleans. The rock of the package leaves `.rocks/` then too, since it
is installed whole, and the next build installs it again, the component
built even when it is incremental and nothing changed. The clean hook runs
on a clean of the whole package only.

### One command at a time

The commands that change a project hold its lock, `_build/tt.lock`:
`tt package build`, `fetch`, `pack`, `clean`, `add`, `remove`, `update` and
`resolve`, `tt package install` and `uninstall` in the project scope,
`tt test` while it builds, and the commands of modules that change the
project, such as those of `tt wasm`. Another command that would change the
project meanwhile waits for it, and says so:

```console
$ tt package build
   • waiting for another tt working on the project /home/me/myapp (pid 48213)
```

Ctrl-C stops the wait. The commands that only read a project, such as
`tt package deps` and `list`, take no lock and wait for nothing. A tt that
a hook or a build runs on the same project, `tt package fetch` in a
`post_build` hook say, works under the lock of the tt that runs it rather
than wait for it: tt names its lock in `TT_PROJECT_LOCK`, which the
commands it runs inherit. The lock is released when tt exits, however it
exits, so a tt that was killed leaves no lock behind; `_build/tt.lock` stays
for the next command. A directory without `app.manifest.toml` gets no lock.

### Other platforms

`tt package build --target linux/arm64` builds the components for
`linux/arm64` on a machine of another platform: an `x86_64` laptop, a CI
runner. The backends that can build for another platform - `cargo`, `go`,
`zig`, `make` and `shell` - are told it, and a component of one that
cannot, `c`, `lua-c` or `cmake`, fails the build before anything is built.
What the builds made is checked against the platform, and kept in
`_build/target/linux-arm64/`, apart from what a build for the machine made:
neither build takes the other's as up to date. The package is installed
into `_build/target/linux-arm64/tree/` rather than into `.rocks/`, whose
modules and programs are those of the machine, which its Tarantool loads
and `tt instance` runs; the dependencies are installed into `.rocks/` and
built for the machine, as for any build. The platforms, the backends and
what each does are in
[Other platforms](../reference/app-manifest.md#other-platforms).

`tt package pack --target linux/arm64` packs such a build: the rock of the
platform, and the archive `--without-deps` (see [Packing](#packing)).

## Packing

`tt package pack` builds the package and packs it into
`_build/pack/<name>-<version>-<os>-<arch>.tt`, a tar.zst archive, printing
its path. The archive is reproducible: packing the same commit twice gives
the same bytes. It holds:

- the manifest, the lock and `VERSION`;
- `.rocks/`: the rock of the package and those of the dependencies, dev
  dependencies excepted, with the manifest of the tree; a file of `.rocks/`
  no rock owns is left out, with a warning;
- `.templates/`: the instance templates of the project, each checked;
- `_runtime/`: the Tarantool and the tt the package runs on, and TCM when
  `[platform].tcm` asks for it: the versions `app.manifest.lock` pins
  ([the pinned runtime](../reference/app-manifest.md#the-pinned-runtime)),
  exactly, from the runtime cache, the installations of the project, a
  [mirror](rock-servers.md#a-mirror-for-offline-builds) of `--registry` or
  `TT_REGISTRIES`, or the active ones; a pinned version none of them has
  fails the pack. A program the lock pins no version of is the highest of
  the cache or of the project that satisfies `[platform]`, else the active
  one;
- the files `[package].include` lists, and the license files.

Other files of the project, `init.lua` at its root say, are not in the
archive unless `[package].include` lists them. So the cluster config of a
package loads its code as `app.module`, from `.rocks/`: `app.module: kv`
loads the `init.lua` of a component of the package `kv`, which the build
lays out as `.rocks/share/tarantool/kv/init.lua`:

```yaml
app:
  module: kv
```

The `vshard_cluster` template of `tt create` does the same: its component
has `namespace = ""`, and the config loads `app.module: storage` and
`app.module: router`, its `storage.lua` and `router.lua`.

A config of `app.file: init.lua` runs in the project, which has `init.lua`,
and fails in every installed copy of the package, which has only what the
archive carries. If the code has to be loaded from a file, list the file in
`[package].include`, and the archive carries it at the same place:

```toml
[package]
name = "kv"
include = ["init.lua"]
```

`tt package pack` warns about an `app.file` of a template's `config.yaml`
that names a file the archive does not carry:

```text
   ⚠ .templates/default/config.yaml: app.file init.lua is not in the archive, so the package installed from it cannot load it; load the code from .rocks/ with app.module: kv, or add "init.lua" to [package].include
```

Each runtime is the first of these that matches `[platform]`:

1. the highest version in the runtime cache,
   `~/.cache/tt/runtimes/<tarantool|tt|tcm>/<ce|ee>/<version>/`
   (`$XDG_CACHE_HOME/tt/runtimes/` when that is set,
   `~/Library/Caches/tt/runtimes/` on macOS). No command fills it: it
   holds the builds placed there by hand, a directory with `bin/<name>` and,
   for Tarantool, its `LICENSE` and `include/tarantool/`;
2. a tt binary installation of the project, in `.tarantool/versions/`: the
   active one, which `tt binary install` and `tt binary switch` select, and
   otherwise the highest version. So `tt binary install tarantool 3.8.1` in
   the project, or `tt binary install tt`, chooses what pack bundles. An
   `[ee]` requirement takes `tarantool-ee` and `tt-ee`; an SDK, or a build
   named by a commit, is not taken. The headers of the tarantool come along,
   and the lock records the name of the installation;
3. the active `tarantool` and `tt`, with a warning.

With no `tarantool` found, the first two are the only sources of one, and
the pack fails when neither has a match. tt binary installs a tarantool
without its license file, so pack warns that it bundles one from an
installation without a license.

`--without-deps` packs the package alone: no `_runtime/` and no
dependencies, which `tt package install`
fetches by the lock; `.rocks/` holds the rock of the package only, a
component of `namespace = ""` included. Such an archive is named `...-any.tt` when it holds no
native file.

A pack of a build for another platform, `--target linux/arm64` (see
[Other platforms](#other-platforms)), names the archive after that
platform, `<name>-<version>-linux-arm64.tt`, and checks its native files
against it. It packs `--without-deps` alone, from the tree of the target:
the Tarantool and the tt it would bundle in `_runtime/`, and the
dependencies of `.rocks/`, are those of the machine, so a pack with them
fails before it builds:

```text
⨯ a .tt archive with its dependencies is packed for the machine tt runs on alone, linux-amd64: its _runtime/ is the Tarantool and the tt of this machine, and its .rocks/ the dependencies built for it; pack the archive for linux-arm64 with --without-deps, whose dependencies are installed where it is installed, or pack --format rock
```

### Debug information

The `c` and `lua-c` builds compile with `-g`, and a `cmake` build is
`RelWithDebInfo`, so the modules and the programs of a package carry their
debug information: a module that crashes Tarantool has a backtrace with its
lines; a build of `c` and `lua-c` by LuaRocks, of the source rock, compiles
with `CFLAGS` of LuaRocks instead, which has no `-g` (see
[Packing a rock](#packing-a-rock)). `--debuginfo` chooses what the pack
does with it: `keep`, the default, packs the files as the builds made them,
and `strip` packs them without it, the symbols kept, so a backtrace still
names the functions:

```console
$ tt package pack --debuginfo strip
```

What is stripped is what the builds of the components of the package
made, its modules and programs in `.rocks/` of the archive, and every
native file of the rock, which holds the package alone. The bundled
runtime, the rocks of the dependencies and the files of
`[package].include` are packed as they are, and the files of the project,
its `.rocks/` and `_build/`, are not touched: the copies being packed are.
The tool is `strip --strip-debug` on Linux, `objcopy --strip-debug` when
there is no `strip`, and `strip -S` of Xcode on macOS, found in `PATH`; a
pack that strips with none fails before it builds:
`--debuginfo strip needs strip --strip-debug of binutils, or objcopy, in
PATH`. A pack for another platform, `--target`, strips with `llvm-strip
--strip-debug`, which strips the files of every platform, where the
`strip` of binutils reads those of the machine alone.

### Packing a rock

A package other projects depend on, a Tarantool module say, is packed as a
rock: `tt package pack --format rock`. The rock holds the files the build
laid out, the rock of the package `tt package build` installs into
`.rocks/` but for `_rocks.lua` (see
[The rocks of the build](#the-rocks-of-the-build)), and the license files,
`[package].license_files`, in `doc/`, where LuaRocks keeps the documents
of a rock: a project installing the rock
has them in `.rocks/share/tarantool/rocks/<name>/<version>/doc/`. It is
written with its rockspec beside it:

```console
$ tt package pack --format rock --out dist
.../mylib/dist/mylib-1.2.0-1.linux-x86_64.rock
.../mylib/dist/mylib-1.2.0-1.rockspec
```

The rock is the binary rock of the platform tt runs on when the package has
a native file, a module or a program, and `<name>-<version>.all.rock`
otherwise; a native file built for another platform fails the pack. A pack
with `--target` packs the binary rock of that platform,
`<name>-<version>.linux-aarch64.rock` for `linux/arm64`,
`macosx-aarch64` for `darwin/arm64` (see
[Other platforms](#other-platforms)). The
version is that of the package, with the revision `--revision` gives, 1 by
default: raise it to publish a new build of a version that is out.

The project needs no rockspec: tt makes it from the manifest. Its
dependencies are the ranges `[dependencies]` and the components of the
product declare, not the versions the lock pins, and `[platform].tarantool`
becomes its dependency on `tarantool`; the source is
`[package].repository`, at the commit of the version: `source.tag` is the
tag of the version, named as git has it, and for a version of a commit no
tag names, `1.2.1-dev.3+gabc1234` say, the full sha of the commit, LuaRocks
having no field for a commit. LuaRocks fetches a git source with `git clone
--branch`, which takes no commit, so it fails to fetch the source of such a
rockspec rather than fetch another commit; tt fetches the source of no rock.
A `dirty` version, of uncommitted changes, and one a `VERSION` file gives
name the repository with no commit, a `dirty` one with a comment saying so;
a package with no `repository` has the source `file://.`. `[package]` gives the
description, the license and the maintainers, `[platform].platforms` the
supported platforms. The rockspec is the same for the rock of every
platform and for the source rock: LuaRocks installs a binary rock as it is,
whatever its build table says, and builds the source rock by that table
(see below). A path dependency
fails the pack: the projects installing the rock have no such directory.
So does a product with no rock of its own, and both fail it before the
build, not after minutes of it. A dependency from a registry of its own is
declared with a warning, since a rockspec cannot name the server.

`--out`, `_build/pack/` by default, is indexed as a rock server once the
rock is written, keeping the rocks already there: a pack on each platform
into the same directory fills one server. The directory gets the LuaRocks
manifests and a [rock index](rock-servers.md#a-rock-index),
`tt-rocks-index.json`, both made from every rock and rockspec in it:

```console
$ ls dist
manifest
manifest-5.1
manifest-5.2
manifest-5.3
manifest-5.4
mylib-1.2.0-1.linux-x86_64.rock
mylib-1.2.0-1.rockspec
tt-rocks-index.json
```

Another project then depends on the rock from it, and its lock pins the
sha256 of every file of the version the index lists, which every install of
the rock checks:

```toml
[dependencies]
mylib = { version = ">=1.2.0", registry = "/path/to/dist" }
```

A rock packed anew with the same version and other bytes no longer matches
such a lock: `tt package resolve` pins it again, and a rock already
published gets a new `--revision` instead. The rocks of several machines,
collected into one directory - from the jobs of a CI matrix, say - are
indexed with `tt package index DIR`, which also writes an index kept apart
from the rocks, for rocks on release pages; see
[Writing an index](rock-servers.md#writing-an-index).

`--format tt,rock` packs the `.tt` archive and the rock from one build;
`--without-deps` and signing apply to the archive only.

`--format rock-src` packs the source rock, `<name>-<version>.src.rock`,
for a platform no binary rock is published for: the same rockspec, and the
files of the project git tracks, those of its submodules included, with a
`VERSION` file of the version, since the unpacked rock has no git history
to derive it from. The manifest and the license files are packed whether
git tracks them or not, a license file that is not there failing the pack
as it fails the binary rock; a
file of a component git neither tracks nor ignores is left out, with a
warning, since the binary rock has it and a build of the source rock would
not. Out of a git repository, every file of the project is
packed, but `_build/`, `.rocks/` and `_runtime/`. The rock is reproducible:
its entries carry no mode but the executable bit, and no time of their own:
every entry of either rock is dated 1980-01-01 00:00, the earliest time a
zip says, which `unzip -l` shows as it is in any time zone. A pack of the
source rock alone builds nothing - no hook, no backend, nothing installed
into `.rocks/` - so it needs no compiler and no headers of Tarantool; the
manifest is checked and the lock resolved, or refused under `--locked`, as a
build does. `--format rock,rock-src` builds once, for the binary rock. A pack on
each platform with `--format rock`, or on one machine with the `--target` of
each platform the backends of the package build for, and one anywhere with
`--format rock-src` fill a server with every rock of a version.

A project that depends on such a rock builds it from its source rock when
the servers it installs the rock from have no rock of its platform, neither
a binary one nor an `.all` one: tt downloads the source rock into
`_build/src/`, builds the package in it with its manifest, as it builds a
path dependency, and installs it into `.rocks/` at the version the lock
pins, revision included, with the license files of the package in `doc/`,
as its binary rock has them; one the source rock does not hold, a file git
did not track where it was packed, is a warning. The hooks of the package
are not run. A rock built
so is not built again while the tree holds it at that version. A source
rock of no tt package, one without `app.manifest.toml`, fails the build.
tt reads no build table of a rockspec.

LuaRocks, `tt rocks` of tt 2 and of tt 3 among them, knows no manifest: it
builds the source rock, and the bare rockspec from the repository at the
version, by the build table of the rockspec, which tt writes for it:

- A package with no build block - no component of the product has one, and
  there is no `pre_build` or `post_build` hook - is of build type `none`,
  and its `build.install` installs every file the layouts of its components
  take, where the binary rock has it: a Lua file by its module name,
  `mylib/util/str.lua` of the namespace `mylib` as `mylib.util.str`, and any
  other file by its directory, under the name it has. The `_version.lua` the
  build generates is in `build.extra_files`, the bytes the binary rock has,
  and LuaRocks writes it into the sources, as `tt-rock-version.lua`, before
  it builds: neither the source rock nor the repository holds a file the
  build generates. So LuaRocks installs into `lua/` and `lib/` of the tree
  what the binary rock holds there, with no tool at all; `_rocks.lua` is in
  neither rock. Its `doc/` is the one LuaRocks makes of any rock: the
  `doc/` directory of the project, or, with none, the README, LICENSE,
  COPYING and `*.md` files of its root. The binary rock has the license
  files there instead, wherever they are in the project. A file at a path
  a module name cannot say makes the build of type `command` instead,
  whose script lays that file out (see below).
- A package with a build block, whose builds are all made without tt too -
  those of `make` and `shell`, the backends that run a command of the
  project's own, of `c` and `lua-c`, whose compiles and link the script
  runs with the compiler of LuaRocks, and of `cmake`, `go`, `cargo` and
  `zig`, which run their tool as tt runs it (see
  [Without tt](../reference/app-manifest.md#without-tt)) - is of build
  type `command`, and its rockspec carries
  `tt-rock.sh`, a script of POSIX sh, in `build.extra_files` beside
  `_version.lua`. `build_command` runs `sh ./tt-rock.sh build -` with `CC`,
  `CFLAGS`, `LIBFLAG` and `LUA_INCDIR` of LuaRocks, and `install_command`
  runs `sh ./tt-rock.sh install -`, which copies what the build made into
  `LUADIR`, `LIBDIR` and `BINDIR` of the rock. Each gives the script the
  variables in a quoted here-document, a line `NAME=value` each, so a
  directory of any name but one with a newline reaches it as it is, and
  the shell runs nothing of it. The script does what a build
  of tt does: it sets the `TT_*` variables of a build,
  `TT_TARANTOOL_INCLUDE_DIR` being `LUA_INCDIR`; looks for the tools of the
  builds in `PATH`; runs `pre_build`, the builds of the components in the
  order of the product, each in its working directory with the `env` of its
  block and the command tt runs, copies the files `output` lists, and runs
  `post_build`, as `tt package build` of the package does; tt runs no hook
  of a source rock it builds. Everything goes into `.tt-rock/stage/`.
  `build.install` installs the files of the layout as for type `none`; the
  script lays out those it cannot install - a file of `output`, which the
  layout of tt takes after the builds, and one at a path `build.install`
  cannot say - so every file is in the rock once. The rockspec has no other
  file a build makes: a file the layout of tt takes from what a build left
  in the directory of its component, which `output` does not list, is in
  the binary rock and not in one LuaRocks builds, and the pack that built
  the package warns of it once a component, naming the `output` to list it
  in. So the rockspec is the same whether the project was built or not. A
  build by the script has
  no cache and no incremental build, and none of the checks tt makes of what
  a build made, and LuaRocks resolves the dependencies by the rockspec, not
  by the lock; what the build of a backend lacks besides is in the comments
  of the script, and the pack warns of it.
- A package with a build that is made with tt alone - one of a backend that
  is not made without tt, or one whose field has no counterpart in a
  script, such as `static = true` or `bundle` of `lua-c` - builds with tt
  alone: the build of its rockspec,
  of type `command`, prints which components and hooks need tt, their
  backends and why, and fails, so LuaRocks installs nothing. The pack of its
  source rock warns that it builds with tt only. A package builds without tt
  whole or not at all: a rock with some of its modules is worse than none.

The rockspec of `mylib`, a package of Lua and data under its own name and a
role under `roles`:

```console
$ tt package pack --format rock,rock-src --out dist
.../mylib/dist/mylib-1.2.0-1.all.rock
.../mylib/dist/mylib-1.2.0-1.src.rock
.../mylib/dist/mylib-1.2.0-1.rockspec
$ cat dist/mylib-1.2.0-1.rockspec
-- The rock of the tt package mylib 1.2.0.
-- tt package pack generated it from app.manifest.toml. tt builds the
-- package from that manifest; LuaRocks builds it as the build table says.
rockspec_format = "3.0"
package = "mylib"
version = "1.2.0-1"
source = {
   url = "git+https://github.com/example/mylib",
   tag = "v1.2.0",
}
description = {
   summary = "A library of Lua and data",
   homepage = "https://github.com/example/mylib",
   license = "BSD-2-Clause",
}
dependencies = {
   "tarantool >= 3.0.0",
}
build = {
   type = "none",
   install = {
      lua = {
         ["mylib._version"] = "tt-rock-version.lua",
         ["mylib.data.schema"] = "mylib/data/schema.json",
         ["mylib.init"] = "mylib/init.lua",
         ["mylib.util.str"] = "mylib/util/str.lua",
         ["roles.mylib"] = "roles/mylib.lua",
      },
   },
   extra_files = {
      ["tt-rock-version.lua"] = [=[
return {
    version  = "1.2.0",
    commit   = "119869b",
    dirty    = false,
    flavor   = "ce",
    built_at = "2026-11-22T11:36:47Z",
}
]=],
   },
}
deploy = {
   wrap_bin_scripts = false,
}
```

`fastlib` is a module of C its component builds with `make`, and a
`fastlib/init.lua` that requires it:

```toml
[components.fastlib]
path = 'fastlib'

[components.fastlib.build]
backend = 'make'
make_target = 'module'
```

`fastlib/Makefile` compiles `fast.c` against the headers of Tarantool into
the directory of the modules; `?=` keeps the flags LuaRocks gives, and has
its own under tt, which gives none:

```make
CFLAGS ?= -O2 -fPIC
LIBFLAG ?= -shared

module:
	$(CC) $(CFLAGS) -I$(TT_TARANTOOL_INCLUDE_DIR) $(LIBFLAG) -o $(TT_OUTPUT_DIR)/fast.so fast.c
```

The script in its rockspec is long, so `sed` shows the parts of it the
package gives; the rest is the same for every package:

```console
$ tt package pack --format rock,rock-src --out dist
.../fastlib/dist/fastlib-1.0.0-1.linux-x86_64.rock
.../fastlib/dist/fastlib-1.0.0-1.src.rock
.../fastlib/dist/fastlib-1.0.0-1.rockspec
$ sed -n '/^build = {/,/^   extra_files/p' dist/fastlib-1.0.0-1.rockspec
build = {
   type = "command",
   build_command = [=[
sh ./tt-rock.sh build - <<'TT_ROCK_VARIABLES'
CC=$(CC)
CFLAGS=$(CFLAGS)
LIBFLAG=$(LIBFLAG)
LUA_INCDIR=$(LUA_INCDIR)
TT_ROCK_VARIABLES
]=],
   install_command = [=[
sh ./tt-rock.sh install - <<'TT_ROCK_VARIABLES'
LUADIR=$(LUADIR)
LIBDIR=$(LIBDIR)
BINDIR=$(BINDIR)
TT_ROCK_VARIABLES
]=],
   install = {
      lua = {
         ["fastlib._version"] = "tt-rock-version.lua",
         ["fastlib.init"] = "fastlib/init.lua",
      },
   },
   extra_files = {
$ sed -n '/^# The component/,/^}/p' dist/fastlib-1.0.0-1.rockspec
# The component "fastlib", built by the tt backend "make".
tt_step_1() {
	TT_COMPONENT_NAME=fastlib
	TT_OUTPUT_DIR=$tt_stage/lib/fastlib
	TT_SHARE_DIR=$tt_stage/lua/fastlib
	TT_PROGRAMS_DIR=$tt_stage/bin
	TT_BUILD_DIR=$tt_state/build/fastlib
	TT_LINK_DIR=$TT_LINK_ROOT/fastlib
	export TT_COMPONENT_NAME TT_OUTPUT_DIR TT_SHARE_DIR TT_PROGRAMS_DIR TT_BUILD_DIR TT_LINK_DIR
	mkdir -p -- "$TT_OUTPUT_DIR" "$TT_SHARE_DIR" "$TT_PROGRAMS_DIR" "$TT_BUILD_DIR" "$TT_LINK_DIR"
	cd "$tt_root"/fastlib
	(tt_export_1)
}
$ sed -n '/^# The script of the export/,/^}/p' dist/fastlib-1.0.0-1.rockspec
# The script of the export of the tt backend "make" for the component "fastlib".
tt_export_1() {
make -C . -f Makefile module
}
$ sed -n '/^tt_build()/,/^}/p' dist/fastlib-1.0.0-1.rockspec
tt_build() {
	tt_env
	rm -rf -- "$tt_state"
	mkdir -p -- "$tt_stage/lua" "$tt_stage/lib" "$tt_stage/bin" "$TT_LINK_ROOT"
	tt_need make 'the component "fastlib"'
	(tt_step_1)
}
```

A pack of the source rock alone, which builds nothing, writes the same
rockspec, and so LuaRocks builds the bare rockspec from the repository of
the package, which holds no file of the script, as it builds the source
rock: the layout of a file of `output` is planned whether the project has
it or not, and no other file a build makes is laid out. Into a directory of
its own, from which LuaRocks takes the
source rock, as it takes the binary rock of its platform where a server has
one; `tt rocks`, which runs LuaRocks, builds it so, with no manifest, and the
module loads:

```console
$ tt package pack --format rock-src --out dist-src
.../fastlib/dist-src/fastlib-1.0.0-1.src.rock
.../fastlib/dist-src/fastlib-1.0.0-1.rockspec
$ cmp dist/fastlib-1.0.0-1.rockspec dist-src/fastlib-1.0.0-1.rockspec
$ cd .. && mkdir lr-src && cd lr-src
$ tt rocks install fastlib --only-server ../fastlib/dist-src
Installing ../fastlib/dist-src/fastlib-1.0.0-1.src.rock

fastlib 1.0.0-1 depends on tarantool >= 3.0.0 (3.9.0-1 provided by VM)
sh ./tt-rock.sh build - <<'TT_ROCK_VARIABLES'
CC=gcc
CFLAGS=-O2 -fPIC
LIBFLAG=-shared
LUA_INCDIR=.../include/tarantool
TT_ROCK_VARIABLES

make: Entering directory '.../luarocks_fastlib-1.0.0-1-ak8B5n/fastlib/fastlib'
gcc -O2 -fPIC -I.../include/tarantool -shared -o .../luarocks_fastlib-1.0.0-1-ak8B5n/fastlib/.tt-rock/stage/lib/fastlib/fast.so fast.c
make: Leaving directory '.../luarocks_fastlib-1.0.0-1-ak8B5n/fastlib/fastlib'
sh ./tt-rock.sh install - <<'TT_ROCK_VARIABLES'
LUADIR=.../lr-src/.rocks/share/tarantool/rocks/fastlib/1.0.0-1/lua
LIBDIR=.../lr-src/.rocks/share/tarantool/rocks/fastlib/1.0.0-1/lib
BINDIR=.../lr-src/.rocks/share/tarantool/rocks/fastlib/1.0.0-1/bin
TT_ROCK_VARIABLES

No existing manifest. Attempting to rebuild...
fastlib 1.0.0-1 is now installed in .../lr-src/.rocks (license: BSD-2-Clause)

$ tarantool -e "print(require('fastlib').answer)" - </dev/null
42
```

The tools of the script are looked for where the rockspec is built, not
where it is packed: the build fails before the first component, naming the
tool and the component, where one is not in `PATH`.

`build.install` installs only the files the source rock holds: a file of
the layout git does not track, or ignores, is left out of it, as it is left
out of the source rock, and the pack of the source rock warns of it. A path
of the layout a module name of LuaRocks cannot say - a directory with a dot
in its name, `migrations/1.0.0/`, or a Lua file with one before `.lua`,
`init.min.lua` - is laid out by `tt-rock.sh`, as for a package with build
blocks, so no pack fails for it. The rockspec of `mylib` with a
`mylib/data/v1.2/schema.json`, committed and tagged, is
of type `command`, and its script runs no build and lays out that file
alone; the others stay in `build.install`:

```console
$ tt package pack --format rock,rock-src --out dist
.../mylib/dist/mylib-1.2.0-1.all.rock
.../mylib/dist/mylib-1.2.0-1.src.rock
.../mylib/dist/mylib-1.2.0-1.rockspec
$ sed -n '/^build = {/,/^   extra_files/p' dist/mylib-1.2.0-1.rockspec
build = {
   type = "command",
   build_command = [=[
sh ./tt-rock.sh build - <<'TT_ROCK_VARIABLES'
CC=$(CC)
CFLAGS=$(CFLAGS)
LIBFLAG=$(LIBFLAG)
LUA_INCDIR=$(LUA_INCDIR)
TT_ROCK_VARIABLES
]=],
   install_command = [=[
sh ./tt-rock.sh install - <<'TT_ROCK_VARIABLES'
LUADIR=$(LUADIR)
LIBDIR=$(LIBDIR)
BINDIR=$(BINDIR)
TT_ROCK_VARIABLES
]=],
   install = {
      lua = {
         ["mylib._version"] = "tt-rock-version.lua",
         ["mylib.data.schema"] = "mylib/data/schema.json",
         ["mylib.init"] = "mylib/init.lua",
         ["mylib.util.str"] = "mylib/util/str.lua",
         ["roles.mylib"] = "roles/mylib.lua",
      },
   },
   extra_files = {
$ sed -n '/^tt_build()/,/^}/p' dist/mylib-1.2.0-1.rockspec
tt_build() {
	tt_env
	rm -rf -- "$tt_state"
	mkdir -p -- "$tt_stage/lua" "$tt_stage/lib" "$tt_stage/bin" "$TT_LINK_ROOT"
	tt_lay mylib/data/v1.2/schema.json lua/mylib/data/v1.2/schema.json
}
```

LuaRocks installs the source rock with both files of `data`:

```console
$ tt package pack --format rock-src --out dist-src
.../mylib/dist-src/mylib-1.2.0-1.src.rock
.../mylib/dist-src/mylib-1.2.0-1.rockspec
$ cd .. && mkdir lr && cd lr
$ tt rocks install mylib --only-server ../mylib/dist-src
Installing ../mylib/dist-src/mylib-1.2.0-1.src.rock

mylib 1.2.0-1 depends on tarantool >= 3.0.0 (3.9.0-1 provided by VM)
sh ./tt-rock.sh build - <<'TT_ROCK_VARIABLES'
CC=gcc
CFLAGS=-O2 -fPIC
LIBFLAG=-shared
LUA_INCDIR=.../include/tarantool
TT_ROCK_VARIABLES

sh ./tt-rock.sh install - <<'TT_ROCK_VARIABLES'
LUADIR=.../lr/.rocks/share/tarantool/rocks/mylib/1.2.0-1/lua
LIBDIR=.../lr/.rocks/share/tarantool/rocks/mylib/1.2.0-1/lib
BINDIR=.../lr/.rocks/share/tarantool/rocks/mylib/1.2.0-1/bin
TT_ROCK_VARIABLES

No existing manifest. Attempting to rebuild...
mylib 1.2.0-1 is now installed in .../lr/.rocks (license: BSD-2-Clause)

$ find .rocks/share/tarantool/mylib -name schema.json
.rocks/share/tarantool/mylib/data/schema.json
.rocks/share/tarantool/mylib/data/v1.2/schema.json
```

The rock reserves `tt-rock-version.lua`, `tt-rock.sh` and `.tt-rock/` at the
root of its sources, for what LuaRocks and a build without tt write there,
and `.luarocks.patches.applied`, with which LuaRocks writes no extra file: a
file the source rock would hold at one of them - one git tracks, or any out
of a git repository - fails the pack of either rock. See
[tt rocks](rock-servers.md#tt-rocks) for LuaRocks.

[Publish a module as a rock](../how-to/rocks.md) walks one module through
all of it: its manifest, its versions, its rocks, a server and an index for
them, and a project that depends on it.

## Installing

`tt package install ARCHIVE` installs into one of the
[scopes](projects-and-scopes.md#scopes):

- **A project** (the default): the package becomes a guest of the project.
  Its rocks join the project's `.rocks/` and its manifest, each checked
  against what it says it holds, with command wrappers for the project; when
  two packages lock a dependency at different versions, the install takes
  one version both accept or fails, explaining why. A project that has no
  Tarantool of its own gets the bundled one.
- **The user or the system scope**: the archive becomes a version of an
  application, `<root>/versions/<name>/<name>_<version>/`, and
  `<root>/apps/<name>` points to it. Its runtime goes into
  `<root>/versions/`, shared like a binary `tt binary` installs, and the
  application runs on it whatever `<root>/bin/` selects; so do the command
  wrappers of its rocks, written for the version where it is, but those of a
  signed version, which is installed as it was signed. Versions live side
  by side, and every install makes the version it installs active;
  `--upgrade` installs only a version newer than every installed one, and
  `--force` reinstalls one that is there, which is refused while an
  instance of the application runs. `tt package switch` makes another
  installed version active again, as a rollback does, while the instances
  run; they move to it on `tt instance restart`.

`tt package list` shows what a scope holds; `tt package uninstall` removes a
guest from a project, or every version of an application from the user or
the system scope, keeping the data of its instances in `<root>/var/` and
the pair of the host in `<root>/etc/<name>/`.

A with-deps archive installs with no network. See
[Install an application for a user](../how-to/user-scope.md) and
[Run instances under systemd](../how-to/systemd.md).
