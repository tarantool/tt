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

A rock that depends on `tarantool` is checked against `tarantool`, since no
server has such a rock: one no version of it meets fails the resolution,
and one its lowest version does not meet is a warning to raise the lower
bound. Nothing is locked for it.

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
| `path` | a directory with the rock, relative to the manifest; required for `path`. See [Path dependencies](#path-dependencies) |
| `registry` | a rock server to take this rock from: an http(s) URL, or a directory relative to the manifest |

A rock with a `registry` is resolved and installed from that server only;
the rocks it depends on come from the registries of the project.

`[dev_dependencies]` are installed for the tests and tools of the project,
and are not packed. A component may declare dependencies of its own in
`[components.<name>.dependencies]`.

### Path dependencies

A `path` dependency is built from its directory, by what the directory holds:

- `app.manifest.toml`: the directory is a project of its own, and tt builds
  it the way `tt package build` builds a package - the components of its
  default product, their layout and `version.lua` - and installs it into
  `.rocks/` as the rock of its package. The dependency is named after that
  package, or after the `rock` of another product of it, which is then the
  product built. Its version is the one tt derives in its directory, and its
  dependencies are those of its default product, resolved with the
  project's own: a `path` one is relative to the dependency, a `registry`
  one keeps its server. Its `[dev_dependencies]`, its lock and its hooks are
  not used, and a rockspec beside the manifest is ignored, with a warning.
- a `*.rockspec`: tt builds the rock it describes.
- neither: there is nothing to build, and the lock only pins the directory.

The lock pins a path dependency by a hash of its files. The hash of a
dependency with a manifest leaves out `.git/`, `_build/`, `.rocks/`,
`_runtime/`, its `app.manifest.lock` and what its `.gitignore` files ignore,
so neither its own build nor the project's makes the lock out of date; a
new version of it does, even one that only a commit or a tag makes. tt
builds such a dependency in `_build/path/<name>/` of the project and leaves
its directory as it is, but for what its build backends write there
themselves.

```toml
[dependencies]
memcached-contrib = { source = "path", path = "../memcached-contrib" }
```

## `[components.NAME]`

| Field | |
| --- | --- |
| `path` | the directory of the component, relative to the manifest; required |
| `kind` | `"module"` (the default) or `"executable"` |
| `include`, `exclude` | the files of a module component; `*.lua` and `*.so` by default |
| `namespace` | where a module component goes in `.rocks/share/tarantool/`: the package name by default, `""` for the top level |
| `dependencies` | the dependencies of the component |
| `build` | its build, see below; required for an executable component |

A `.wasm` file a module component lays out has to be a WebAssembly binary: a
file of another kind fails the build, and a core module, which tarantool-wasm
does not load, is laid out with a warning. `tt wasm new` declares the
components tarawasm builds; see
[Run WebAssembly components](../how-to/wasm-components.md).

### `[components.NAME.build]`

| Field | Backends | |
| --- | --- | --- |
| `backend` | | `c`, `lua-c` (module components); `go` (executable components); `cargo`, `cmake`, `make`, `shell` (either) |
| `cwd`, `env`, `output` | all | the directory the build runs in, its environment, the files it makes |
| `incremental` | `make`, `shell` | `true` skips the build when its inputs are those of the last one and what it left is still there |
| `make_target`, `entrypoint`, `flags` | `make` | `make_target` is required |
| `command`, `args` | `shell` | `command` is required |
| `module`, `sources`, `include_dirs`, `libraries`, `library_dirs`, `defines` | `c`, `lua-c` | `module` and `sources` are required |
| `module` | `cargo` | the name of the module a module component builds: the name of the library by default |
| `platforms.<os>` | `c`, `lua-c` | more `include_dirs`, `libraries`, `library_dirs`, `defines` for one OS |
| `package` | `go`, `cargo` | the Go package, `.` by default, or the cargo package |
| `static` | `go`, `cargo` | a program that needs no loader and no shared library; for an executable component |
| `name`, `tags`, `ldflags`, `cgo` | `go` | `name` is the component's name by default; cgo is off by default |
| `bin`, `features`, `profile` | `cargo` | `bin`, of an executable component, is the component's name by default; `profile` is `release` by default |
| `variables`, `targets`, `build_type` | `cmake` | `-D<name>=<value>` of the configuration, the targets to build, all by default, and `CMAKE_BUILD_TYPE`, `RelWithDebInfo` by default; `output` is required |

A build gets its contract in the environment: `TT_OUTPUT_DIR`, where it
puts what it makes, `TT_PROJECT_ROOT`, `TT_PACKAGE`, `TT_COMPONENT_NAME`,
`TT_VERSION`, `TT_PLATFORM_OS` and `TT_PLATFORM_ARCH`. When tt knows the
Tarantool it builds for, a build also gets `TT_TARANTOOL_PREFIX`, the
install prefix of that Tarantool, and `TT_TARANTOOL_INCLUDE_DIR`, the
directory of its `module.h` and `lua.h`, `<prefix>/include/tarantool`, which
the `c` and `lua-c` backends compile against; either is unset when tt does
not know it. `env` of the build cannot override them.

`cmake` builds the CMake project of `cwd`, where its `CMakeLists.txt` is,
out of the source, in `_build/cmake/<component>/`:

```console
cmake -S <cwd> -B _build/cmake/<component> -DCMAKE_BUILD_TYPE=<build_type> \
    -D<name>=<value>... -DCMAKE_PREFIX_PATH=<prefix of Tarantool>
cmake --build _build/cmake/<component> --config <build_type> --parallel <CPUs> \
    --target <targets>...
```

and copies the files `output` lists, relative to the build tree, into
`TT_OUTPUT_DIR`; a file the build did not make fails it. The prefix of
Tarantool comes after the prefixes `variables` gives `CMAKE_PREFIX_PATH`, so
`find_path(... tarantool/module.h)` of the usual `FindTarantool.cmake` finds
its headers. The build tree stays between builds, and cmake rebuilds what
changed in it; `--rebuild` configures a new one, and so does a build of a
project that was moved. `CMAKE_BUILD_PARALLEL_LEVEL`, if set, is the number
of jobs, and `CMAKE_GENERATOR` in `env` picks another generator. On macOS a
`.so` of a module component is copied as `.dylib`, which Tarantool loads
there. An executable component lists its programs in `output`. CMake 3.15
or newer must be on `PATH`.

```toml
[components.memcached]
path = "memcached"
include = ["init.lua"]
[components.memcached.build]
backend = "cmake"
cwd = "."
variables = { ENABLE_BUNDLED_SMALL = "ON" }
targets = ["internalso"]
output = ["memcached/internal.so"]
```

`cargo` builds the binary `bin` of an executable component, and the library
of the package of a module component: a `cdylib`, which is the native
module `<module>.so`, `<module>.dylib` on macOS. `target/` of a component
cargo builds is not laid out. See [Build a module in Rust](../how-to/rust-modules.md).

The inputs of an `incremental` build are its build block and the files of
the component but the hidden ones, `_build/`, `_runtime/` and what the
`.gitignore` files of the project and of the component ignore: what a build
generates and git does not keep is not one. The hash of the inputs of its
last build is in `_build/cache/<component>.json`, and `--rebuild` of
`tt package build` and `tt package pack` builds anyway; see
[Incremental builds](../concepts/packages.md#incremental-builds).

`.tarantool/bin` of the project, where `tt binary` and `tt wasm prepare`
put the programs of the project, comes first on the `PATH` of a build and of
a hook: a `command` names such a program without `.env/activate.sh` sourced.

## `[products.NAME]`

| Field | |
| --- | --- |
| `components` | the components of the product; required |
| `default` | `true` for the product `build` and `pack` take without `--product`; exactly one product is the default |
| `rock` | the name of the rock the product is released as, `[a-z][a-z0-9-]*`; the package name by default for the default product, none for another |

A manifest without `[products]` has one product, `default`, made of every
component.

A package of several rocks, a module and its tools say, gives each product
other projects depend on a `rock` of its own. Two products released as two
rocks share no component, since a tree installing both would hold their
files twice, and a product's `version.lua` goes under the name of its rock,
`<rock>/version.lua`, rather than of the package. `tt package pack --format
rock --product NAME` packs a product as its rock; a product with no rock
cannot be. A path dependency is a product of the package in its directory:
the default one by the package name, another by its rock.

## `[hooks.pre_build]` and `[hooks.post_build]`

A command run before or after the components are built, with the `make` or
`shell` backend and the fields of a build. A hook gets the environment of a
build without `TT_OUTPUT_DIR` and `TT_COMPONENT_NAME`.

<!-- TODO(writers): app.manifest.lock - what it records, such as
bundled_tarantool_version - is not described yet. -->
