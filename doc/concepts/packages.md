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
`[dependencies]` in place, keeping comments and order. An unknown field is an
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
  holds. Only what was added, and what it requires, can move.
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

- A **module** component, the default, lays its files out under
  `.rocks/share/tarantool/<namespace>/`, `<namespace>` being the package name
  unless the component says otherwise. Its files are the `*.lua` and `*.so`
  of its `path` unless `include` lists others. A build backend - `c`,
  `lua-c`, `cargo`, `cmake`, `make` or `shell` - makes a native module
  first, or a WebAssembly component: see
  [Run WebAssembly components](../how-to/wasm-components.md).
  [Build a module in Rust](../how-to/rust-modules.md) describes `cargo`.
- An **executable** component, `kind = "executable"`, builds a program into
  `.rocks/bin/`, with the `go`, `cargo`, `cmake`, `make` or `shell` backend.
  An [external process](../how-to/external-processes.md) of the application
  runs it.

A native module is `<module>.so` in `.rocks/lib/tarantool/<namespace>/`, and
`<module>.dylib` on macOS, where Tarantool looks for no other name: the `c`,
`lua-c` and `cargo` backends name it so, `cmake` copies a `.so` it built as
`.dylib` there, and a `.dylib` a component lays out goes there as a `.so`
does.

A product is a set of components built and packed together. A manifest
without `[products]` has one implicit product, `default`, made of every
component.

## Building

`tt package build` resolves the lock, installs the dependencies into
`.rocks/`, runs the build backends of the components and lays the
components out, with `version.lua` of the package. `tt package fetch` only
installs `.rocks/` from the lock, with no build.

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
stays. A build also removes from
`.rocks/` the versions of the dependencies the lock pins other than the one
it pins, and repairs a `.rocks/` an older tt left out of step with its
manifest.

- A build backend finds the programs of the project's `.tarantool/bin` -
  what `tt binary` and `tt wasm prepare` installed - ahead of `PATH`, with or
  without `.env/activate.sh`.
- A `make` or `shell` build with `incremental = true` is skipped while
  nothing it depends on changed; see [Incremental builds](#incremental-builds).
- A `.wasm` file a component lays out has to be a WebAssembly binary: any
  other fails the build, and a core module, which tarantool-wasm does not
  load, is laid out with a warning.
- `tt package build`, `fetch` and `pack` need no Tarantool: where tt finds
  none, neither in `.tarantool/bin` nor in `PATH`, a package of Lua builds
  and packs all the same. What is compiled against the headers of Tarantool
  fails then, saying so: a `c` or `lua-c` component, before anything is
  built, and a rock of a server with a C part. A `make`, `shell` or `cmake`
  build is not told where Tarantool is, `TT_TARANTOOL_PREFIX` and
  `TT_TARANTOOL_INCLUDE_DIR` unset, and fails on its own if it needs the
  headers.

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

The code reads its version as `require('<name>.version').version`; set
`generate_version_lua = false` in `[package]` to leave `version.lua` out.

### Incremental builds

A `make` or `shell` build of a component, a module or an executable one,
can be skipped when nothing it depends on changed since its last build:

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

The build is skipped when the hash is the one its last successful build
recorded, in `_build/cache/<component>.json`, and what that build left is
still there:

- the files `output` lists;
- the files of the component the layout took;
- the files it wrote into `TT_OUTPUT_DIR`, at any depth: those it created or
  changed, and those an earlier build wrote and it left alone, as a `make`
  target that is up to date does. Of an executable component, these are its
  programs.

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
it; removing `_build/cache/` does the same.

A component whose build writes files no `.gitignore` ignores gets another
hash at every build, and is built every time, as if it were not
incremental. `tt wasm new` writes the `.gitignore` of a WebAssembly
component, and declares it incremental.

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
  `[platform].tcm` asks for it;
- the files `[package].include` lists, and the license files.

Other files of the project, `init.lua` at its root say, are not in the
archive unless `[package].include` lists them. So the cluster config of a
package loads its code as `app.module: <name>`, from `.rocks/`.

The runtimes come from `~/.cache/tt/runtimes/<tarantool|tt|tcm>/<ce|ee>/<version>/`
when a build there matches `[platform]`, and otherwise from the active
`tarantool` and `tt`, with a warning; with no `tarantool` found, the cache
is the only source of one, and a cache with no match fails the pack.
`--without-deps` packs the package alone: no `_runtime/` and no
dependencies, which `tt package install`
fetches by the lock; `.rocks/` holds the rock of the package only, a
component of `namespace = ""` included. Such an archive is named `...-any.tt` when it holds no
native file.

### Packing a rock

A package other projects depend on, a Tarantool module say, is packed as a
rock: `tt package pack --format rock`. The rock holds the files the build
laid out, the rock of the package `tt package build` installs into
`.rocks/`, and the license files, `[package].license_files`, in `doc/`,
where LuaRocks keeps the documents of a rock: a project installing the rock
has them in `.rocks/share/tarantool/rocks/<name>/<version>/doc/`. It is
written with its rockspec beside it:

```console
$ tt package pack --format rock --out dist
.../mylib/dist/mylib-1.2.0-1.linux-x86_64.rock
.../mylib/dist/mylib-1.2.0-1.rockspec
```

The rock is the binary rock of the platform tt runs on when the package has
a native file, a module or a program, and `<name>-<version>.all.rock`
otherwise; a native file built for another platform fails the pack. The
version is that of the package, with the revision `--revision` gives, 1 by
default: raise it to publish a new build of a version that is out.

The project needs no rockspec: tt makes it from the manifest. Its
dependencies are the ranges `[dependencies]` and the components of the
product declare, not the versions the lock pins, and `[platform].tarantool`
becomes its dependency on `tarantool`; the source is
`[package].repository`, at the tag of the version, named as git has it,
when the version is that of a tag and not `dirty`; `[package]` gives the
description, the license and the maintainers, `[platform].platforms` the
supported platforms. The build is done, so the build type is `none`, and
the rockspec is the same for the rock of every platform. A path dependency
fails the pack: the projects installing the rock have no such directory.
So does a product with no rock of its own, and both fail it before the
build, not after minutes of it. A dependency from a registry of its own is
declared with a warning, since a rockspec cannot name the server.

`--out`, `_build/pack/` by default, is indexed as a rock server once the
rock is written, keeping the rocks already there: a pack on each platform
into the same directory fills one server. Another project then depends on
the rock from it:

```toml
[dependencies]
mylib = { version = ">=1.2.0", registry = "/path/to/dist" }
```

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
its entries carry no time and no mode but the executable bit. A pack on
each platform with `--format rock` and one anywhere with `--format rock-src`
fill a server with every rock of a version.

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
  by side; `--upgrade` installs only a newer one, and `--force` reinstalls
  one that is there, which is refused while an instance of the application
  runs.

`tt package list` shows what a scope holds; `tt package uninstall` removes a
guest from a project, or every version of an application from the user or
the system scope, keeping the data of its instances in `<root>/var/` and
the pair of the host in `<root>/etc/<name>/`.

A with-deps archive installs with no network. See
[Install an application for a user](../how-to/user-scope.md) and
[Run instances under systemd](../how-to/systemd.md).
