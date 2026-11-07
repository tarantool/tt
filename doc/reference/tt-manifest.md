# tt.manifest.toml

The manifest of a [distribution of tt](../how-to/build-distribution.md): the
tt core and the tt modules that `tt binary build tt` compiles into one
binary. The build writes `tt.manifest.lock` and `tt.manifest.sum` next to
it.

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
builtin = true
external_modules = true

[build]
ssl = "openssl_static"
tags = ["netgo"]

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
probe = "../tt-probe"
foo = "github.com/acme/tt-foo@v1.2.0"
bar = { module = "github.com/acme/tools", version = "v0.3.0", package = "github.com/acme/tools/ttbar", symbol = "Commands" }

[replace]
"example.com/lib" = "./libs/lib"
"github.com/acme/lib/v2" = "github.com/acme/lib/v2@main"
```

Only `manifest_version` and `manifest_kind` are required: the smallest
manifest builds tt itself on the core of the tt that runs the build. An
unknown field is an error, unless the manifest declares a newer minor
`manifest_version` than tt knows, in which case it is a warning. Another
major version is refused.

## Top level

| Field | Required | |
| --- | --- | --- |
| `manifest_version` | yes | the format version: `"0.1"` |
| `manifest_kind` | yes | `"tt-distribution"`; a manifest without it is the manifest of an application, `app.manifest.toml` |

## `[package]`

| Field | Default | |
| --- | --- | --- |
| `name` | `tt` | the name of the binary: letters, digits, `.`, `_` and `-`, starting with a letter or a digit |

## `[core]`

The tt core the binary is built on. `tt` and `path` exclude each other.

| Field | Default | |
| --- | --- | --- |
| `tt` | the version of the core of the tt that runs the build | a Go version query for the core module |
| `path` | | a local checkout of tt, relative to the manifest's directory |
| `module` | `github.com/tarantool/tt/v3` | the Go module of the core, for a fork; a `path` checkout must hold it |

`tt` is resolved by the go command, as `go list -m <module>@<tt>`:

- a version, such as `v3.0.0-alpha.2`;
- a commit hash, for a commit no tag points to;
- `latest`: the newest release, or the newest pre-release while there is
  none;
- a prefix, such as `v3` or `v3.1`: the newest release with that prefix. Go
  reads `v3` as a version prefix, never as the branch `v3`, and a prefix
  matches no pre-release: while tt 3 has pre-releases only, `v3` matches
  nothing;
- a comparison, such as `<v3.1.0`;
- a branch or a tag whose name is not a version.

The lock pins what the query resolved to until `--update`. Pin an exact
version or a commit anyway, so that `--update` and a build with no lock
take what the manifest says.

Without `tt`, a build by a tt whose version Go cannot fetch - a development
build - fails: set `tt` or `path`. `--core` and `TT_DIST_CORE` override
`[core]` for one build (see
[Building on another core](../how-to/build-distribution.md#building-on-another-core)).

## `[distribution]`

How the binary presents itself and what it has besides its modules.

| Field | Default | |
| --- | --- | --- |
| `title` | `Tarantool CLI` | the name the binary presents itself under, in the help and in `tt version` |
| `edition` | none | semver build metadata after the short version: `ee` makes it `3.1.0+ee` |
| `integrity` | `none` | the integrity provider: `none` or `rsa` (see [Sign packages](../how-to/sign-packages.md)) |
| `builtin` | `true` | tt's own commands, the module `builtin` |
| `external_modules` | `true` | [external modules](../how-to/external-modules.md) and `tt modules` |

The version of the distribution is taken from `git describe` in the
manifest's directory; `VERSION_LABEL` follows it.

## `[build]`

How the binary is compiled. The lock does not record these: one lock
builds the binary any way.

| Field | Default | |
| --- | --- | --- |
| `ssl` | `default` | the TLS implementation: `default`, `openssl` or `openssl_static`; `TT_CLI_BUILD_SSL` overrides it |
| `cgo` | on for `openssl` and `openssl_static` only | build with cgo |
| `tags` | none | build tags added to the ones of the TLS implementation; no commas or spaces |
| `ldflags` | none | linker flags added after tt's own, one flag per entry |

## `[dependencies]`

The tt modules, by name. The name is the module's name in tt - in
`tt version --modules`, in its log records and in errors about its
commands: a letter followed by letters, digits, `-` and `_`. `builtin` is
taken while `[distribution].builtin` is on.

A module is a string or a table. The string is one of:

- a local directory: `.`, `..`, or a path that starts with `./`, `../` or
  `/`, relative to the manifest's directory;
- a Go module path: `github.com/tarantool/tt/modules/aeon`;
- a Go module path and a version query after `@`:
  `github.com/acme/tt-foo@v1.2.0`.

The table has the fields:

| Field | Default | |
| --- | --- | --- |
| `module` | from the `go.mod` of `path` | the Go module path; required without `path`, and must match the `go.mod` with it |
| `version` | the commit of the core for a module of the core's repository, `latest` for any other | a Go version query; not allowed with `path`, nor for a module of the core's repository, which follows the core |
| `path` | | a local directory with the module, relative to the manifest's directory |
| `package` | the root package of the module | the import path of the package with the constructor; a package of `module` |
| `symbol` | `New` | the constructor, an exported identifier of type `func(sdk.Services) []sdk.Mount` |
| `kind` | `tt-module` | the only kind there is |
| `source` | `path` with `path`, `registry` without | |

The SDK modules a module requires are taken from the core - at its commit,
or from its checkout - whatever the module's `go.mod` says (see
[Write a tt module](../how-to/write-a-module.md#set-up-the-go-module)).

## `[replace]`

Go modules of the build taken from elsewhere, as `replace` directives of
the main module the build generates, which a tt module cannot carry itself.
A key is a Go module path, with `@version` to replace that version only. A
value is a local directory (it starts with `./`, `../` or `/`, relative to
the manifest's directory) that holds the module, or `module@version`, where
the version may be any query; the lock pins what it resolves to. The core,
the modules of its repository and the modules of `[dependencies]` cannot be
replaced here. A replacement no package of the build comes from has no
effect, with a warning. See
[Replacing Go modules](../how-to/build-distribution.md#replacing-go-modules).
