# Build a distribution of tt

A tt module is a Go module that is compiled into tt rather than run next to
it: its package exports a constructor, `func New(sdk.Services) []sdk.Mount`,
that returns the commands it adds (see [Write a tt module](write-a-module.md)
and [the SDK](https://github.com/tarantool/tt/blob/v3/sdk/README.md)). A
distribution of tt is the tt core and a set of such modules built into one
binary, described by `tt.manifest.toml`:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt"

[core]
tt = "v3.0.0-alpha.2"

[distribution]
title = "Tarantool CLI EE"
edition = "ee"
integrity = "rsa"

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
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
`rsa`), whether it has tt's own commands (`builtin`) and external modules
(`external_modules`), both on by default, and where `tt binary install`
finds prebuilt products (`bundle_sources`: by default `customer-zone`, the
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
its latest.

```sh
tt binary build tt                  # build ./tt from ./tt.manifest.toml
tt binary build tt --update         # take the newest core and modules
tt binary build tt --locked -o tt   # build exactly what the lock pins
```

The Go toolchain must be in `PATH`. The build generates a main module that
requires the core and the modules, compiles it with the build tags tt itself
is built with, runs `tt version` of the result and writes `tt.manifest.lock`
and `tt.manifest.sum` next to the manifest. Commit both: while the manifest
stays the same, the next build takes its versions from the lock, and
`--locked` fails when the lock or the Go modules differ. The lock pins what
the binary is built from, not how it is compiled: the TLS implementation,
the tags and the Go toolchain are the choice of each build, so one lock
builds every one of them, and a build that pins nothing new - by another tt,
say - leaves both files as they are.

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
  builtin     v3.0.1-0.20261028190206-9031d7ad96d9  github.com/tarantool/tt/v3
  migrations  (devel)                               example.com/tt-ee/plugins/migrations
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

