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
installs the same versions anywhere.

The lock changes only when asked to:

- `tt package add`, `tt package remove` and `tt package resolve` resolve the
  lock again for what the manifest declares, keeping every version the lock
  holds. Only what was added, and what it requires, can move.
- `tt package update [NAME]` takes the newest versions the constraints
  allow, for every dependency or for NAME only.
- `tt package build` and `tt package pack` resolve a lock that no longer
  matches the manifest, keeping its versions. With `--locked` they fail
  instead, which is what CI wants.

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
  `lua-c`, `make` or `shell` - makes a native module first.
- An **executable** component, `kind = "executable"`, builds a program into
  `.rocks/bin/`, with the `go`, `cargo`, `make` or `shell` backend. An
  [external process](../how-to/external-processes.md) of the application
  runs it.

A native module is `<module>.so` in `.rocks/lib/tarantool/<namespace>/`, and
`<module>.dylib` on macOS, where Tarantool looks for no other name: the `c`
and `lua-c` backends name it so, and a `.dylib` a component lays out goes
there as a `.so` does.

A product is a set of components built and packed together. A manifest
without `[products]` has one implicit product, `default`, made of every
component.

## Building

`tt package build` resolves the lock, installs the dependencies into
`.rocks/`, runs the build backends of the components and lays the
components out, with `version.lua` of the package. `tt package fetch` only
installs `.rocks/` from the lock, with no build.

The version of a package is, in order:

1. the line of a `VERSION` file next to the manifest;
2. the nearest `v*` tag of git: `1.2.0` on the tag, `1.2.1-dev.3+gabc1234`
   three commits after it;
3. `0.0.0-dev.<N>+g<sha>` without a tag.

Changes not committed add `dirty` to the build metadata: `1.2.0+dirty`.
The code reads its version as `require('<name>.version').version`; set
`generate_version_lua = false` in `[package]` to leave `version.lua` out.

## Packing

`tt package pack` builds the package and packs it into
`_build/pack/<name>-<version>-<os>-<arch>.tt`, a tar.zst archive, printing
its path. The archive is reproducible: packing the same commit twice gives
the same bytes. It holds:

- the manifest, the lock and `VERSION`;
- `.rocks/`: the components and the dependencies, dev dependencies excepted;
- `.templates/`: the instance templates of the project, each checked;
- `_runtime/`: the Tarantool and the tt the package runs on, and TCM when
  `[platform].tcm` asks for it;
- the files `[package].include` lists, and the license files.

Other files of the project, `init.lua` at its root say, are not in the
archive unless `[package].include` lists them. So the cluster config of a
package loads its code as `app.module: <name>`, from `.rocks/`.

The runtimes come from `~/.cache/tt/runtimes/<tarantool|tt|tcm>/<ce|ee>/<version>/`
when a build there matches `[platform]`, and otherwise from the active
`tarantool` and `tt`, with a warning. `--without-deps` packs the package
alone: no `_runtime/` and no dependencies, which `tt package install`
fetches by the lock. Such an archive is named `...-any.tt` when it holds no
native file.

## Installing

`tt package install ARCHIVE` installs into one of the
[scopes](projects-and-scopes.md#scopes):

- **A project** (the default): the package becomes a guest of the project.
  Its rocks join the project's `.rocks/`; when two packages lock a
  dependency at different versions, the install takes one version both
  accept or fails, explaining why. A project that has no Tarantool of its own
  gets the bundled one.
- **The user or the system scope**: the archive becomes a version of an
  application, `<root>/versions/<name>/<name>_<version>/`, and
  `<root>/apps/<name>` points to it. Its runtime goes into
  `<root>/versions/`, shared like a binary `tt binary` installs, and the
  application runs on it whatever `<root>/bin/` selects. Versions live side
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
