# app.manifest.toml

The manifest of a [package](../concepts/packages.md), at the root of its
project. `tt new` writes a skeleton of it.

```toml
manifest_version = "0.1"

[package]
name = "kv-api"
description = "A key-value API"
include = ["init.lua"]

[platform]
tarantool = ">=3.0.0,<4.0.0"
tt = ">=3.0.0"
registries = ["https://rocks.tarantool.org/", "./vendor-rocks"]

[dependencies]
checks = ">=3.0.0"
mylib = { source = "path", path = "../mylib" }

[dev_dependencies]
luatest = "*"

[components.app]
path = "kv"

[components.native]
path = "native"
[components.native.build]
backend = "lua-c"
module = "fast_hash"
sources = ["fast_hash.c"]

[products.default]
components = ["app", "native"]
default = true

[hooks.pre_build]
backend = "shell"
command = "./gen.sh"
```

An unknown field is an error, unless the manifest declares a newer minor
`manifest_version` than tt knows, in which case it is a warning. A newer
major version is refused.

## Top level

| Field | Required | |
| --- | --- | --- |
| `manifest_version` | yes | the format version: `"0.1"` |
| `manifest_kind` | no | `"app"`, the default; a distribution of tt has its own manifest, `tt.manifest.toml` |

## `[package]`

| Field | Required | |
| --- | --- | --- |
| `name` | yes | lowercase letters, digits and dashes, starting with a letter; not `bin` or `manifests` |
| `description` | no | one line, shown by `tt package list` |
| `license` | no | |
| `license_files` | no | files packed with the package |
| `authors` | no | |
| `repository` | no | |
| `include` | no | files of the project put at the root of the archive |
| `generate_version_lua` | no | `false` leaves `version.lua` out of the build; `true` by default |

## `[platform]`

| Field | Required | |
| --- | --- | --- |
| `tarantool` | yes | the versions of Tarantool, with the edition: `">=3.0.0"` or `">=3.0.0[ce]"` for Community, `">=3.3.0[ee]"` for Enterprise |
| `tt` | yes | the versions of tt |
| `tcm` | no | the versions of TCM to bundle into the archive; no edition |
| `platforms` | no | the platforms the package is for: `linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`, or `any` alone |
| `registries` | no | the rock servers, in the order they are asked, instead of the defaults: HTTP(S) URLs or directories, relative to the manifest. See [Rock servers](../concepts/rock-servers.md) |

A version constraint is a comma-separated list of comparisons, such as
`">=3.0.0,<4.0.0"`.

## `[dependencies]` and `[dev_dependencies]`

A dependency is a rock, in the short form:

```toml
checks = ">=3.0.0"           # a constraint; "*" for any version
```

or in the long form:

| Field | |
| --- | --- |
| `source` | `"registry"` (the default) or `"path"` |
| `version` | the constraint; required for `registry` |
| `path` | a directory with the rock, relative to the manifest; required for `path` |
| `registry` | a rock server to take this rock from |

`[dev_dependencies]` are installed for the tests and tools of the project,
and are not packed. A component may declare dependencies of its own in
`[components.<name>.dependencies]`.

## `[components.NAME]`

| Field | |
| --- | --- |
| `path` | the directory of the component, relative to the manifest; required |
| `kind` | `"module"` (the default) or `"executable"` |
| `include`, `exclude` | the files of a module component; `*.lua` and `*.so` by default |
| `namespace` | where a module component goes in `.rocks/share/tarantool/`: the package name by default, `""` for the top level |
| `dependencies` | the dependencies of the component |
| `build` | its build, see below; required for an executable component |

### `[components.NAME.build]`

| Field | Backends | |
| --- | --- | --- |
| `backend` | | `c`, `lua-c` (module components); `go`, `cargo` (executable components); `make`, `shell` (either) |
| `cwd`, `env`, `output` | all | the directory the build runs in, its environment, the files it makes |
| `make_target`, `entrypoint`, `flags` | `make` | `make_target` is required |
| `command`, `args` | `shell` | `command` is required |
| `module`, `sources`, `include_dirs`, `libraries`, `library_dirs`, `defines` | `c`, `lua-c` | `module` and `sources` are required |
| `platforms.<os>` | `c`, `lua-c` | more `include_dirs`, `libraries`, `library_dirs`, `defines` for one OS |
| `package` | `go`, `cargo` | the Go package, `.` by default, or the cargo package |
| `static` | `go`, `cargo` | a program that needs no loader and no shared library |
| `name`, `tags`, `ldflags`, `cgo` | `go` | `name` is the component's name by default; cgo is off by default |
| `bin`, `features`, `profile` | `cargo` | `bin` is the component's name by default; `profile` is `release` by default |

A build gets its contract in the environment: `TT_OUTPUT_DIR`, where it
puts what it makes, `TT_PROJECT_ROOT`, `TT_PACKAGE`, `TT_COMPONENT_NAME`,
`TT_VERSION`, `TT_PLATFORM_OS` and `TT_PLATFORM_ARCH`. `env` of the build
cannot override them.

## `[products.NAME]`

| Field | |
| --- | --- |
| `components` | the components of the product; required |
| `default` | `true` for the product `build` and `pack` take without `--product`; exactly one product is the default |

A manifest without `[products]` has one product, `default`, made of every
component.

## `[hooks.pre_build]` and `[hooks.post_build]`

A command run before or after the components are built, with the `make` or
`shell` backend and the fields of a build. A hook gets the environment of a
build without `TT_OUTPUT_DIR` and `TT_COMPONENT_NAME`.

<!-- TODO(writers): app.manifest.lock - what it records, such as
bundled_tarantool_version - is not described yet. -->
