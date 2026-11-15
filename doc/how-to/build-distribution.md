# Build a distribution of tt

A tt module is a Go module that is compiled into tt rather than run next to
it: its package exports a constructor, `func New(sdk.Services) []sdk.Mount`,
that returns the commands it adds, or `func New(sdk.Services) sdk.Module`,
that returns its build backends and checks of the files of a package too (see [Write a tt module](write-a-module.md)
and [the SDK](https://github.com/tarantool/tt/blob/v3/sdk/README.md)). A
distribution of tt is the tt core and a set of such modules built into one
binary, described by `tt.manifest.toml`:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt"

[core]
tt = "v3.0.0-alpha.3"

[distribution]
title = "Tarantool CLI EE"
edition = "ee"
integrity = "rsa"

[dependencies]
migrations = "./plugins/migrations"
foo = "github.com/acme/tt-foo@v1.2.0"
```

Only `manifest_version` and `manifest_kind` are required; every field is
in [tt.manifest.toml](../reference/tt-manifest.md). Without `[core]` the
distribution is built on the core of the tt that builds it; `tt` takes a Go
version query for the core module - a version, a commit, `latest` - and
`path` a local checkout of the core instead. Pin a version or a commit: Go
reads `v3` as "the newest v3.x.y release", not as the branch `v3`, and while
tt 3 has pre-releases only it matches nothing. `[distribution]` sets how the binary
presents itself (`title`, `edition`), the integrity provider (`none` or
`rsa`), whether it has the standard modules of the core (`standard`, see
[Standard modules](#standard-modules)), tt's own commands (`builtin`) and
external modules (`external_modules`), all on by default, and where
`tt binary install` finds prebuilt products (`bundle_sources`: by default
`customer-zone`, the
customer zone of tarantool.io, for Tarantool Enterprise, the SDK, TCM and
tt-ee, `rws`, the RPMs of rws.tarantool.org, for Tarantool Community, and
`github`, the releases of tt on GitHub, for tt; `[]` for none, and then
Tarantool Community and tt are built from source). `[build]` sets the TLS
implementation (`ssl`: `default`, `openssl` or `openssl_static`, overridden
by `TT_CLI_BUILD_SSL`), whether to build with `cgo` (by default only for
the OpenSSL ones), extra build `tags` and `ldflags`.

A dependency is a local directory (`./...`, `../...` or an absolute path), a
module path, or a module path with a version after `@`; the long form is a
table with `module`, `version`, `path`, `package` and `symbol`: the
constructor is `New` in the root package of the module unless `package` and
`symbol` say otherwise. A module of the core's repository, such as `aeon`,
is built at the commit of the core; any other module without a version at
its latest. The standard modules need no entry here: the distribution above
has `aeon`, `build`, `emmylua` and `wasm` with its two modules of its own.

```sh
tt binary build tt                  # build ./tt from ./tt.manifest.toml
tt binary build tt --update         # take the newest core and modules
tt binary build tt --locked -o tt   # build exactly what the lock pins
```

The Go toolchain must be in `PATH`. The build generates a main module that
requires the core and the modules, compiles it with the build tags tt itself
is built with, runs `tt version` of the result - `tt --help` of one without
`builtin`, which has `version` - and writes `tt.manifest.lock` and
`tt.manifest.sum` next to the manifest. Commit both: while the manifest
says the same, the next build takes its versions from the lock, and
`--locked` fails when the lock or the Go modules differ. The lock names the
manifest by a hash of its tables, keys and values, as the lock of a package
does: a comment or a reformat of the manifest leaves it current. The lock
pins what the binary is built from, not how it is compiled: the TLS
implementation, the tags and the Go toolchain are the choice of each build,
so one lock builds every one of them, and a build that pins nothing new - by
another tt, say - leaves both files as they are.

A local module, a local core or a directory of `[replace]` has no version to
pin, so the lock records `content_hash`, a hash of its files by path and
content, as Go hashes a module for `go.sum`. They are the files the bundle of
`--vendor` copies: every regular file of its directory but those in VCS and
`vendor` directories and in nested modules, and those `.gitignore` ignores.
The `.gitignore` files are the module's own and those of the directories
above it up to the root of its git repository - outside a repository, up to
the manifest's directory when the module is in it. They are read as git reads
them, but without git, so a tarball of a checkout hashes as the checkout
does; `.git/info/exclude` and `core.excludesFile`, which differ from machine
to machine, are not read, and a file git tracks that `.gitignore` ignores
does not count. The files Go may build the module from count even when
ignored: the Go and cgo sources, `go.mod`, `go.sum` and the files
`//go:embed` embeds. `--locked` fails when the files changed; an ordinary
build records them again. A binary built in a checkout of the core, `./tt`
say, is no change: the `.gitignore` of tt ignores it.

`tt version` of the result shows the version of the distribution and of the
tt core it is built on; `tt version --modules` lists its modules with the
versions Go recorded for their Go modules, `(devel)` for a local one:

```sh
$ ./tt version --modules
Tarantool CLI EE version 3.0.0, linux/amd64. commit: 1a2b3c4
Tarantool CLI core version 3.0.0. commit: 9031d7ad96d9 (v3.0.1-0.20261028190206-9031d7ad96d9)
  aeon        v0.0.0-20261028190206-9031d7ad96d9    github.com/tarantool/tt/modules/aeon
  build       v0.0.0-20261028190206-9031d7ad96d9    github.com/tarantool/tt/modules/build
  builtin     v3.0.1-0.20261028190206-9031d7ad96d9  github.com/tarantool/tt/v3
  emmylua     v0.0.0-20261028190206-9031d7ad96d9    github.com/tarantool/tt/modules/emmylua
  foo         v1.2.0                                github.com/acme/tt-foo
  migrations  (devel)                               example.com/tt-ee/plugins/migrations
  wasm        v0.0.0-20261028190206-9031d7ad96d9    github.com/tarantool/tt/modules/wasm
```

## Standard modules

The standard modules are the modules of the core's repository that the
official tt is built with, `aeon`, `build`, `emmylua` and `wasm` now. The
core lists them in its package `standard`, and a distribution takes them all
by default, at the commit of the core, so it gets the ones a newer core adds
without an edit of its manifest. `build` has the build backends of tt, from
`make` and `shell` to `cargo` and `go`: the core has none of its own, and a
distribution without `build` lays out the Lua of a package but builds no
component with a build block and runs no hook. `wasm` has `tt wasm` and the
check of the `.wasm` files of a package; the components `tt wasm new` adds
are built by the `shell` backend of `build`. `[distribution].exclude`
leaves some out, by name:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt-lite"

[core]
path = "../tt"

[distribution]
exclude = ["emmylua", "wasm"]
```

With a checkout of tt in `../tt`, the build takes `aeon` and `build`, and
not `emmylua` and `wasm`:

```sh
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3 (path ../tt)
   •   standard aeon, build (with the core)
   • Building tt-lite
   • Built /src/lite/tt-lite
/src/lite/tt-lite
$ ./tt-lite version --modules
Tarantool CLI version 3.0.0, linux/amd64. commit: 1a2b3c4
  aeon     (devel)  github.com/tarantool/tt/modules/aeon
  build    (devel)  github.com/tarantool/tt/modules/build
  builtin  (devel)  github.com/tarantool/tt/v3
```

The lock names the standard modules the binary has in its `[core]`, and
`--locked` fails when a build would take others:

```toml
[core]
module = 'github.com/tarantool/tt/v3'
path = '../tt'
content_hash = 'sha256:4367f681...'
standard = ['aeon', 'build']
```

The build reads the list from the source of the package `standard` and
imports each standard module it takes into the generated `main`, as it does
the modules of `[dependencies]`, so an excluded module is not compiled in at
all:

```sh
$ go version -m tt-lite | grep 'dep.*tt/modules'
	dep	github.com/tarantool/tt/modules/aeon	v0.0.0-0
	dep	github.com/tarantool/tt/modules/build	v0.0.0-0
```

`builtin`, tt's own commands, counts as a standard module here:
`exclude = ["builtin"]` is `builtin = false`. A name the core does not list
is an error that names the ones it does:

```sh
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3 (path ../tt)
   ⨯ distribution.exclude: "tcm" is not a standard module of the core ../tt: they are aeon, build, emmylua, wasm, builtin
```

A module of `[dependencies]` with the name of a standard module takes its
place - a fork of `aeon`, or a checkout of it in `./aeon`. Name it in
`exclude` too, to say that you mean it; otherwise the build warns, and a
manifest that lists `aeon` from the core's repository itself, as manifests
did before, builds the same binary as one that does not:

```sh
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3 (path ../tt)
   ⚠ dependencies.aeon takes the place of the standard module aeon of the core: add "aeon" to distribution.exclude to say so, or drop the dependency to take the core's
   •   standard build (with the core)
   •   aeon github.com/tarantool/tt/modules/aeon (with the core)
   • Building tt-lite
```

A core before v3.0.0-alpha.3 has no package `standard`. A distribution
built on it fails until it sets `standard = false` and lists its modules:

```sh
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3@v3.0.0-alpha.2
   ⨯ distribution.standard: the core github.com/tarantool/tt/v3@v3.0.0-alpha.2 has no package github.com/tarantool/tt/v3/standard that lists the standard modules: standard needs core v3.0.0-alpha.3 or later; set standard = false and list the modules in [dependencies]
```

```toml
[core]
tt = "v3.0.0-alpha.2"

[distribution]
standard = false

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
```

## Replacing Go modules

A tt module is built with the dependencies its own `go.mod` names, but Go
applies the `replace` directives of the main module only, so a module that
depends on a library that is not published, or not at the version it needs,
cannot carry the replacement itself. `[replace]` puts it in the `go.mod` that
the build generates, before it runs `go mod tidy`:

```toml
[replace]
"gitlab.corp.mail.ru/tarantool/go-migration" = "../go-migration"
"github.com/tarantool/go-storage/v2" = "github.com/tarantool/go-storage/v2@v2.1.0-rc1"
"github.com/acme/old@v1.2.3" = "github.com/acme/new@main"
```

A key is the path of a Go module, with `@version` to replace that version
only, as in a `go.mod`. A value is either a local directory - it starts with
`./`, `../` or `/`, and is relative to the manifest - that holds the module
the key names, or `module@version`, where the version may be any query (a
branch, a commit, `latest`); the build resolves it to an exact version, as it
does for the modules, and the lock pins it. The core, the modules of its
repository and the tt modules listed in `[dependencies]` are not replaced
here, which is an error: the core has `[core].path` and `--core`, and a tt
module has `path` and `version` of its own. A replacement that no package of
the build comes from has no effect, and the build warns of it.

The lock records every entry in `[replace]`: a directory by `content_hash`,
computed as for a local module (see above), and a module by its exact version. `--locked`
fails when a replaced directory changed, like it does for a local module.
`--vendor` copies the replaced directories into `local/replace/` of the
bundle, which `go.mod` replaces them with by a relative path, so the bundle
builds offline.

## Building on another core

`--core VALUE`, or the `TT_DIST_CORE` environment variable (the flag wins),
builds a distribution on another core than `[core]` names, without editing
the manifest: a local checkout - an existing directory, or a path that starts
with `./`, `../` or `/`, taken from the project directory - or else a version
query, as `[core].tt` takes. It is for building a distribution against
changes of the core not yet released, and for the CI of tt itself, which
builds the distributions from its working tree:

```sh
tt binary build tt --core ../tt
TT_DIST_CORE=. tt binary build tt --manifest dist/tt.manifest.toml
```

Such a build is a development one. Its core and the modules of the core's
repository are not taken from the lock, and the lock and the sum are not
written; the other modules still come from the lock while the manifest
matches it. `--locked` refuses it.

## Offline build

`--vendor DIR` makes a bundle instead of a binary: everything a distribution
is built from, which the Go toolchain alone turns into the binary on a
machine with no network, no module cache and no tt.

```sh
tt binary build tt --locked --vendor dist/bundle
```

The bundle holds the generated `go.mod`, `go.sum` and `main.go`, `vendor/`
with every package the binary is built from (the core, the modules of its
repository, the tt modules, third-party modules, and the files they embed),
`local/` with a copy of each local module - a local core, the path
dependencies and the directories of `[replace]` - which `go.mod` replaces them
with by a relative path, and the build parameters: `build.env` sets the tags,
the linker flags and cgo of each TLS implementation, the implementation the
bundle was made with, the toolchain and the version, and `build.sh` builds
with them. The bundle names no directory of the machine that made it, so it
can be moved anywhere; it is built once offline when it is made.

On the offline machine, with the Go toolchain `build.env` names
(`TT_DIST_TOOLCHAIN`) or a newer one in `PATH`:

```sh
sh bundle/build.sh                         # the TLS the bundle was made with
sh bundle/build.sh openssl_static ./tt     # or default, openssl; and the output
```

`build.sh` is no more than:

```sh
cd bundle
. ./build.env
GOFLAGS=-mod=vendor GOPROXY=off GOWORK=off GOTOOLCHAIN=local \
CGO_ENABLED=$TT_DIST_CGO_DEFAULT \
    go build -trimpath -tags "$TT_DIST_TAGS_DEFAULT" \
        -ldflags "$TT_DIST_LDFLAGS_DEFAULT" -o tt .
```

The OpenSSL implementations need a C compiler and the OpenSSL headers on the
offline machine; on macOS `build.sh` takes `TT_DIST_LDFLAGS_DARWIN_*`.

