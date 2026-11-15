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
| `license_files` | no | files packed with the package: at the root of the archive, in `doc/` of the rock `--format rock` packs, at their paths in the project, and into the source rock whether git tracks them or not |
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
| `registries` | no | the rock servers, in the order they are asked, instead of the defaults: HTTP(S) URLs or directories, relative to the manifest or absolute. See [Rock servers](../concepts/rock-servers.md) |

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
| `path` | a directory with the rock, relative to the manifest or absolute; required for `path`. See [Path dependencies](#path-dependencies) |
| `registry` | a rock server to take this rock from: an http(s) URL, or a directory, relative to the manifest or absolute |

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
  `.rocks/` as the rock of its package, with its `license_files` in `doc/`
  of the rock, as its binary rock has them; one that is not there is a
  warning. The dependency is named after that
  package, or after the `rock` of another product of it, which is then the
  product built. Its version is the one tt derives in its directory, and its
  dependencies are those of its default product, resolved with the
  project's own: a relative `path` one is relative to the dependency, a
  `registry` one keeps its server. Its `[dev_dependencies]` and its lock
  are not used; its hooks are not run and a rockspec beside the manifest is
  ignored, each with a warning when the lock of the project is resolved.
- a `*.rockspec`: tt builds the rock it describes.
- neither: there is nothing to build or install, and the lock only pins the
  directory, at no version; the rock of the project does not depend on it.

The lock pins a path dependency by its path, relative to the project or
absolute as the manifest has it, and by a hash of its files. An absolute
path ties the lock to the machine: it holds only where the directory is at
that path. The hash of a dependency with a manifest leaves out `.git/`,
`_build/`, `.rocks/`, `_runtime/`, its `app.manifest.lock` and what its
`.gitignore` files ignore, so neither its own build nor the project's makes
the lock out of date; a new version of it does, even one that only a commit
or a tag makes. A dependency in a subdirectory of a repository, the
project's own among them, is versioned by its directory alone (see
[Building](../concepts/packages.md#building)): a commit or a change
elsewhere in the repository moves no version of it. tt builds such a
dependency in `_build/path/<name>/` of the project - its stage, the
`TT_BUILD_DIR` of its components and its `CARGO_TARGET_DIR` - and leaves
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
| `include`, `exclude` | the files of the component that are laid out; `*.lua` and `*.so` by default, nothing by default for `go` |
| `namespace` | where the files and the modules of the component go in `.rocks/share/tarantool/` and `.rocks/lib/tarantool/`: the package name by default, `""` for the top level |
| `dependencies` | the dependencies of the component |
| `build` | its build, see below |
| `kind` | deprecated: `"module"` or `"executable"`, what the component says its build makes; see below |

A component makes what its build makes, and lays out its files. The build
may make modules, data files and programs, any of them at once:

| Result | Where the build puts it | Where it is installed |
| --- | --- | --- |
| a module | `TT_OUTPUT_DIR`, `lib/<namespace>/` of the package | `.rocks/lib/tarantool/<namespace>/` |
| a data file, such as `.wasm` | `share/<namespace>/` of the package | `.rocks/share/tarantool/<namespace>/` |
| a program | `bin/` of the package | `.rocks/bin/`, where `tt instance` runs it from |

What a build makes is told by the fields of its backend: `module` and `bin`
of `cargo` and `zig`, or `output` of `make`, `shell` and `cmake`; `go`
always makes a program, `c` and `lua-c` always a module. tt records what
each component made, by type, in `_build/results.json`. One component of a
cargo package with a library and a CLI makes both:

```toml
[components.fast]
path = "fast"

[components.fast.build]
backend = "cargo"
module = "fast"
bin = "fastctl"
```

`kind` is still accepted in `manifest_version = "0.1"`, with a warning,
`components.<name>.kind is deprecated: a component makes what its build
makes; remove it`, as what the component says of its build, which tt
checks. `"executable"` says the build makes programs and nothing else: the
component lays out none of its files, so `include`, `exclude` and
`namespace` are errors, a `build` is required, `TT_OUTPUT_DIR` of its build
is the directory of the programs, a plain string of `output` is a program,
and `cargo` or `zig` with neither `module` nor `bin` builds the program
named after the component. `"module"` says the build makes no program. A backend that
cannot make what `kind` says, or a block that asks for what it rules out,
is an error before anything is built, at `components.<name>.kind`: `is
"executable", but backend "c" makes no programs`, `is "module", but the
build makes programs`. A build that makes it anyway fails with exit code 2:
`kind = "executable", but the build made more than programs: <files>`,
`kind = "module", but the build made programs: <files>`.
`manifest_version = "0.2"` has no `kind`.

A `.wasm` file a component lays out, or its build writes into `share/`, has
to be a WebAssembly binary: a file of another kind fails the build, and a
core module, which tarantool-wasm does not load, is laid out with a warning.
The check is that of the standard module `wasm`. `tt wasm new` declares the
components tarawasm builds; see
[Run WebAssembly components](../how-to/wasm-components.md).

### `[components.NAME.build]`

| Field | Backends | |
| --- | --- | --- |
| `backend` | | `c`, `lua-c` (modules); `go` (programs); `cargo`, `zig` (modules and programs); `cmake`, `make`, `shell` (modules, data files and programs) |
| `cwd`, `env` | all | the directory the build runs in, its environment |
| `output` | `make`, `shell`, `cmake` | the files the build makes, which tt copies where each goes, see below; required for `cmake` |
| `incremental` | `make`, `shell`, `zig` | `true` skips the build when its inputs are those of the last one and what it left is still there |
| `make_target`, `entrypoint`, `flags` | `make` | `make_target` is required |
| `command`, `args` | `shell` | `command` is required |
| `module`, `sources`, `include_dirs`, `libraries`, `library_dirs`, `defines` | `c`, `lua-c` | `module` and `sources` are required |
| `cflags` | `c`, `lua-c` | flags of the compiler, one per entry, after tt's own and before `CFLAGS` of the environment; see below |
| `module` | `cargo` | builds the library of the package as the module of this name; a block with neither `module` nor `bin` builds the library under its own name |
| `platforms.<os>` | `c`, `lua-c` | more `include_dirs`, `libraries`, `library_dirs`, `defines`, `cflags`, `ldflags` for one OS, `linux` or `darwin`, after those of the build |
| `package` | `go`, `cargo` | the Go package, `.` by default, or the cargo package |
| `static` | all | `true`: what the build makes needs nothing of the machine it is installed on, and the build checks it; see below |
| `ldflags` | `go`, `c`, `lua-c` | flags of the linker, one per entry: of `go build -ldflags`, after tt's own; of the link of `c` and `lua-c`, before the sources; see below |
| `name`, `tags`, `cgo` | `go` | `name` is the component's name by default; cgo is off by default |
| `bin`, `features`, `profile` | `cargo` | `bin` builds the binary of this name, the program of the same name; `profile` is `release` by default |
| `module`, `bin` | `zig` | the module and the program the build takes from what `zig build` installed; a block names one or both |
| `steps`, `optimize`, `options` | `zig` | the steps of `zig build`, its default one by default; the mode, `Debug`, `ReleaseSafe`, `ReleaseFast` or `ReleaseSmall`, `ReleaseSafe` by default; `-D<name>=<value>` of the `build.zig` |
| `variables`, `targets`, `build_type` | `cmake` | `-D<name>=<value>` of the configuration, the targets to build, all by default, and `CMAKE_BUILD_TYPE`, `RelWithDebInfo` by default; `output` is required |

#### `output`

`output` lists the files a build makes that are to go into the package. It
is for a build whose tool writes its files where it likes - a Makefile, a
script, a CMake project - and tt takes each file from there after the build
and puts it into the package. An entry is a string, the path of the file,
or a table, `{ path = "...", to = "..." }`, that says where it goes. One
entry may be written alone:

```toml
output = "dist/calc.wasm"                       # the same as ["dist/calc.wasm"]
output = { path = "build/tool", to = "bin" }    # the same as [{ ... }]
output = ["build/tool.so", { path = "build/tool", to = "bin" }]
```

`path` is relative to where the backend takes the file from:

| Backend | `path` is relative to |
| --- | --- |
| `make`, `shell` | the working directory of the build: `cwd`, which is relative to the project, or the directory of the component when the block has no `cwd` |
| `cmake` | the build tree, `_build/component/<component>/` of the project (`TT_BUILD_DIR`) |
| a backend of a module | where its documentation says; a backend whose output tt copies takes it from the working directory, as `make` does |

The file is taken after the build succeeds; a file the build did not make
fails it with exit code 2:

```text
⨯ building component "rates": copy declared output "dist/rates.json": opening the source: open /home/me/shop/rates/dist/rates.json: no such file or directory
```

An incremental build that is up to date takes the file again from the same
place, and runs the build again when it is gone from there.

`to` says where the file goes. tt puts it there under its base name - the
directories of `path` are dropped - in the package. `tt package build`
installs the package into `.rocks/` of the project, `tt package pack` packs
that `.rocks/` into the archive, and `tt package install` installs it with
the same paths: into `.rocks/` of a project, or of the version of the
application, `<root>/versions/<name>/<name>_<version>/.rocks/`, in the user
and the system scopes:

| `to` | What the file is | Where it is installed in `.rocks/` |
| --- | --- | --- |
| `lib` | a module, which `require` loads | `lib/tarantool/<namespace>/<file>` |
| `share` | a data file, such as `.wasm` | `share/tarantool/<namespace>/<file>`, beside the Lua files of the package |
| `bin` | a program, a program of the component, which `tt instance` finds by it | `bin/<file>` |

`<namespace>` is the `namespace` of the component, the name of the package
by default. An entry with no `to` is a module (`lib`), but for a backend
that makes files of one type only, whose type it is then - a data file of a
backend of data files - and for a component of `kind = "executable"`,
whose files are programs. A program is checked as every program is.

This differs from `include`, which lays files of the directory of the
component out under their paths: `include = ["dist/calc.wasm"]` installs
`share/tarantool/<namespace>/dist/calc.wasm`, and
`output = { path = "dist/calc.wasm", to = "share" }` installs
`share/tarantool/<namespace>/calc.wasm`. A file `output` names is not laid
out unless `include` takes it as well.

A package whose build makes a JSON file its Lua code reads:

```text
shop/
├── app.manifest.toml
├── app/init.lua
└── rates/build.sh
```

`shop/app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "shop"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[components.app]
path = "app"

[components.rates]
path = "rates"

[components.rates.build]
backend = "shell"
command = "sh"
args = ["build.sh"]
output = { path = "dist/rates.json", to = "share" }
```

`shop/rates/build.sh`, run in `rates/`:

```sh
mkdir -p dist
printf '{"usd": 90}\n' > dist/rates.json
```

`shop/app/init.lua`, the module `shop`, which finds the file beside itself:

```lua
local fio = require('fio')
local json = require('json')

local M = {}

-- rates.json is a data file of the package: tt installs it into the
-- directory of the package, next to this file.
function M.rates()
    local file = assert(fio.open(fio.pathjoin(debug.sourcedir(), 'rates.json')))
    local data = file:read()
    file:close()
    return json.decode(data)
end

return M
```

```console
$ cd shop
$ tt package build
$ find .rocks/share/tarantool/shop -type f | sort
.rocks/share/tarantool/shop/init.lua
.rocks/share/tarantool/shop/rates.json
.rocks/share/tarantool/shop/version.lua
$ tt run -e "print(require('shop').rates().usd) os.exit()"
90
```

`rates/build.sh` and `rates/dist/` are not laid out: the default `include`
takes `*.lua` and `*.so`. `debug.sourcedir()` is the directory of the Lua
file that calls it, so the module finds the file wherever the package is
installed. Code that is not in the package reads it by its path from the
directory of the application, which is the working directory of its
instances and of `tt run`: `.rocks/share/tarantool/shop/rates.json`.

A `to` of another value is an error, `components.rates.build.output[0].to:
"etc" is not lib, share or bin`, and so is a table without `path`; a value
that is neither a string, a table nor an array of them is `output must be
a file, a table { path = ..., to = ... } or an array of them, got integer`.
A backend that makes no file of the type refuses the entry:
`components.a.build.output[0].to: backend "wasm-tool" makes no data files`.
`output` of a backend that has no such field, such as `c`, is refused as
any field of another backend is, below.

A field of a backend other than the one of the build is an error, since it
would do nothing: `cflags` on a `make` build, `make_target` on a `shell`
build or hook, `output` on a `c` one. The field is the block's whatever its
value, `make_target = ""` too. The error names the backends that have the
field, module by module in the order of the names of the modules, those of
a module in its order - for the standard module `build`, `make` and `shell`,
then the others by name: `components.a.build.ldflags: is a field of backend
c, go or lua-c, not of "cargo"`.

The backends above are those of `build`, a standard module of tt
(`modules/build` of its repository): the core has none of its own, and the
official tt, and every distribution that takes the standard modules, has
them. A module tt is built with may provide more (see
[Write a build backend](../how-to/write-a-build-backend.md)),
with fields of their own, which are checked as those of `build` are, and
builds that run through the same steps: before anything is built, in the
environment below, and laid out, recorded and skipped by `incremental` as
those of `build`. A distribution that excludes `build` (see
[Standard modules](tt-manifest.md#standard-modules)) has only the backends
of its other modules.

A build block of a backend no module of the tt provides is a warning, `no module of this tt provides backend "<name>"; its fields are
not checked`, for a command that builds nothing, such as `tt package add`
or `tt package pack --format rock-src`. It is an error once tt is to build
the component - by `tt package build` and `pack`, and by `tt package fetch`
for a path dependency or a source rock it builds - before anything is
built, and the error names the dependency. The fields of a build block are
checked when the manifest is
checked, not when it is read: by the rules of the format version above for
a field no backend has, and with the path of the field for a value of a
wrong type, `components.a.build.cflags: must be an array, not a string`.

A build gets its contract in the environment: `TT_OUTPUT_DIR`, where it
puts the modules it makes, which exists when the build starts - the
programs, for a component of `kind = "executable"` - `TT_PROJECT_ROOT`,
`TT_PACKAGE`, `TT_COMPONENT_NAME`,
`TT_VERSION`, `TT_PLATFORM_OS` and `TT_PLATFORM_ARCH`. When tt knows the
Tarantool it builds for, a build also gets `TT_TARANTOOL_PREFIX`, the
install prefix of that Tarantool, and `TT_TARANTOOL_INCLUDE_DIR`, the
directory of its `module.h` and `lua.h`, `<prefix>/include/tarantool`, which
the `c` and `lua-c` backends compile against; either is unset when tt does
not know it. `env` of the build cannot override them. Where tt finds no
Tarantool at all, a `c` or `lua-c` component fails the build before
anything is built, for want of the headers.

`TT_BUILD_DIR` is a directory of the build's own for what it makes on the
way, out of the sources: object files, a build tree. It is
`_build/component/<component>/`, there when the build starts, and it stays
between builds, so that a `make` build rebuilds what changed only;
`--rebuild` empties it first, and so does a build of a block that changed
since the last build. A `make` or `shell` build that writes there
rather than beside its sources leaves the directory of the component as it
is, and that of a path dependency, another checkout, too.

`c` and `lua-c` compile `sources`, relative to `cwd`, into one module,
`<module>.so`, `<module>.dylib` on macOS, in `TT_OUTPUT_DIR`, with one run
of the C compiler, `$CC`, `cc` by default:

```console
$CC -O2 -fPIC <cflags> <$CFLAGS> -I<include dir of Tarantool> -D<defines> -I<include_dirs> \
    -shared <ldflags> -o <module>.so <sources> \
    -L<library_dirs> -Wl,-rpath,<library_dirs> -l<libraries> <$LDFLAGS>
```

The flags of the compiler are tt's, then the project's, then the user's:
`-O2`, left out when `CFLAGS` is set, even to nothing, and `-fPIC`; then
`cflags`; then `CFLAGS` of the environment, split on blanks, which come last
and so win a conflict, as `CFLAGS` comes after `AM_CFLAGS` in autotools:
`CFLAGS="-O0 -g"` builds for a debugger, and `CFLAGS=-Wno-error` builds a
project whose `cflags` have `-Werror` with a compiler that warns of more.
`ldflags` go before the sources, as make's `$(CC) $(LDFLAGS) -o out objs
$(LDLIBS)` has them, so that `-Wl,--as-needed` acts on the `-l` that follow;
`LDFLAGS` of the environment ends the line. A list of `platforms.<os>` goes
after the one of the build; `<os>` is `linux` or `darwin`, the OSes tt
builds for, and another, `macos` say, is an error. On macOS `-shared` is
`-bundle -undefined dynamic_lookup -all_load`, and no `-Wl,-rpath` is
given. `CC`, `CFLAGS` and `LDFLAGS` are those of the environment tt runs
in, and the compiler reads none of them: in `env` of the build each is an
error, as it would reach nothing, and the flags of the project go into
`cflags` and `ldflags`. So are `CPPFLAGS`, `LIBS` and `LDLIBS`, which
neither tt nor the compiler reads: `defines`, `include_dirs` and `cflags`
take what `CPPFLAGS` would carry, `libraries` and `library_dirs` what
`LIBS` would. `tt -V package build` shows the line.

```toml
[components.memcached.build]
backend = "lua-c"
module = "datapath"
sources = ["datapath.c"]
cflags = ["-std=gnu99", "-Wall", "-Wextra", "-Werror"]

[components.memcached.build.platforms.linux]
ldflags = ["-Wl,--as-needed"]

[components.memcached.build.platforms.darwin]
cflags = ["-Wno-deprecated-declarations"]
```

`cmake` builds the CMake project of `cwd`, where its `CMakeLists.txt` is,
out of the source, in `TT_BUILD_DIR`:

```console
cmake -S <cwd> -B _build/component/<component> -DCMAKE_BUILD_TYPE=<build_type> \
    -D<name>=<value>... -DCMAKE_PREFIX_PATH=<prefix of Tarantool>
cmake --build _build/component/<component> --config <build_type> --parallel <CPUs> \
    --target <targets>...
```

and copies the files `output` lists, relative to the build tree, where each
goes; a file the build did not make fails it. The prefix of
Tarantool comes after the prefixes `variables` gives `CMAKE_PREFIX_PATH`, so
`find_path(... tarantool/module.h)` of the usual `FindTarantool.cmake` finds
its headers. The build tree stays between builds, and cmake rebuilds what
changed in it; `--rebuild` configures a new one, and so does a build of a
project that was moved. `CMAKE_BUILD_PARALLEL_LEVEL`, if set, is the number
of jobs, and `CMAKE_GENERATOR` in `env` picks another generator. On macOS a
`.so` module is copied as `.dylib`, which Tarantool loads there. A program
of the build is an entry of `output` with `to = "bin"`. CMake 3.15 or newer
must be on `PATH`. The component is configured with the policy version its
`cmake_minimum_required` asks for: CMake 4 refuses one older than 3.5, and
the `CMAKE_POLICY_VERSION_MINIMUM` tt sets for the rocks it builds is not
passed on, while one set in `env`, or in the environment tt runs in, is.

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

`cargo` builds the library of the package for `module`: a `cdylib`, which
is the native module `<module>.so`, `<module>.dylib` on macOS; the binary
`bin` for `bin`, the program `bin`; both for both; and the library under
its own name for neither. The build gets
`CARGO_TARGET_DIR`, `_build/cargo/`, which the cargo components of the
project share, as the packages of a workspace share its `target/`: what
they depend on is compiled once. A `target-dir` of the cargo configuration
gives way to it, but a `CARGO_TARGET_DIR` or `CARGO_BUILD_TARGET_DIR` in
`env`, or in the environment tt runs in, is kept. The library and the
binary are taken from where cargo reports it built them. `--rebuild` runs
cargo as every build does, and cargo rebuilds what changed, a new compiler
included. A `target/` of a component cargo builds is not laid out. See
[Build a module in Rust](../how-to/rust-modules.md).

`zig` runs `zig build` of the `build.zig` of `cwd`, installing into
`_build/component/<component>/zig-out/` and keeping its cache in
`zig-cache/` beside it:

```console
zig build <steps>... -p _build/component/<component>/zig-out \
    --cache-dir _build/component/<component>/zig-cache \
    [--pkg-dir _build/component/<component>/zig-pkg] [--release=<mode>] -D<name>=<value>...
```

`--pkg-dir`, where zig 0.17 keeps the packages it fetches for
`build.zig.zon`, `zig-pkg/` beside it by default, is given to zig 0.17
and later, unless the `env` of the block sets `ZIG_LOCAL_PKG_DIR`; zig
0.16 keeps them in its global cache and refuses the flag.

`optimize` is given as `--release=safe`, `fast` or `small`, and `Debug` as
no flag, so a `build.zig` that does not declare `-Doptimize` takes it too;
`options` in the order of their names, and `optimize` is none of them. The
module `module` is taken from `lib/` of the prefix, `<module>.so` or
`.dylib`, as `installModule` of zig-tarantool installs it, or
`lib<module>.so` or `.dylib`, as `b.installArtifact` installs a library,
and copied as `<module>.so`, `<module>.dylib` on macOS; a dotted module,
`a.b`, is `lib/a/b.so` there and `a/b.so` in the namespace of the
component. The program `bin` is taken from `bin/` of the prefix. A block
that names neither is an error, and so is a module or a program the build
did not install, which names what it installed. `--rebuild` empties the
prefix, the cache and the packages. A `zig-out/` and a `zig-pkg/` of a
component zig builds are not laid out. The inputs of an `incremental` build are also the version of zig and
`build.zig` and `build.zig.zon` of `cwd`. zig fetches the dependencies of
`build.zig.zon` itself as it builds. See
[Build a module in Zig](../how-to/zig-modules.md).

`static = true` asks what the build makes to need nothing of the machine
the package is installed on, and the build checks that it does: a file that
needs more fails the build, with exit code 2, naming the file and what it
needs.

- A program needs no loader and no shared library. `go`, `cargo` and
  `zig` are asked to build one so, for Linux only - `cargo` builds the
  binary for musl, and the module of the same block as any module; `zig`
  builds a program of a block without a module for musl, `-Dtarget=<arch>-linux-musl`,
  when the `build.zig` takes `-Dtarget` and `options` names no `target`,
  and one of a block with a module for the target of the module; what
  `cmake`, `make` and `shell` build is only checked.
- A module needs no shared library but those of the C library, which
  Tarantool, loading it, has loaded itself: on Linux `libc.so.6`,
  `libm.so.6`, `libdl.so.2`, `libpthread.so.0`, `librt.so.1`, the loader,
  `ld-linux-*` or, of musl, `ld-musl-*` and `libc.musl-*`, and
  `libgcc_s.so.1`, the GCC runtime every build of Tarantool needs; on macOS
  `/usr/lib/libSystem.B.dylib`, and every slice of a universal file. Its
  native files are those the build writes into `TT_OUTPUT_DIR` and those of
  the component the layout takes. The backend is asked for nothing: a
  library a module needs, such as `libsasl2.so.2`, is linked in statically,
  from its `.a`, or loaded at run time with `dlopen`. A C++ module links
  `libstdc++` in with `-static-libstdc++`; on macOS it needs `libc++`,
  which is not one of them. A module that needs a newer glibc than the
  Tarantool it is built for builds with a warning: it does not load where
  that Tarantool runs on an older glibc.

The inputs of an `incremental` build are its build block and the files of
the component but the hidden ones, `_build/`, `_runtime/` and what the
`.gitignore` files of the project and of the component ignore: what a build
generates and git does not keep is not one. The hash of the inputs of its
last build is in `_build/cache/<component>.json`, and `--rebuild` of
`tt package build` and `tt package pack` builds anyway; so does the next
build after `tt package clean <component>`, which removes the hash and what
the build made. See
[Incremental builds](../concepts/packages.md#incremental-builds) and
[Cleaning](../concepts/packages.md#cleaning).

`.tarantool/bin` of the project, where `tt binary` and `tt wasm prepare`
put the programs of the project, comes first on the `PATH` of a build and of
a hook: a `command` names such a program without `.env/activate.sh` sourced.
A program is looked for in that `PATH` only: a block whose `env` sets
`PATH` gets the programs of that `PATH` and of `.tarantool/bin`, and a
program only tt's own `PATH` has is not found.

`tt -V package build` logs every command a build or a hook runs, and the
directory it runs in, before running it, and shows what the command
prints; without `-V` that is shown, with the command, when the command
fails.

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

## `[hooks.pre_build]`, `[hooks.post_build]` and `[hooks.clean]`

A command run before or after the components are built, with the `shell`
backend, the default, or `make` and the fields of a build. A hook gets the
environment of a build without `TT_OUTPUT_DIR`, `TT_COMPONENT_NAME` and
`TT_BUILD_DIR`, and the files of its `output` are not copied. A hook names
a backend that runs hooks: `make` or `shell` of the standard module
`build`, or one of a module that says it runs them; another is an error,
`hooks.pre_build: backend "cmake" is not supported (want make or shell)`. A
tt without a backend that runs hooks, such as a distribution that excludes
`build`, refuses every hook, the one that names no backend too:
`hooks.pre_build: backend "shell" is not supported: no module of this tt
provides a backend that runs hooks`.

`[hooks.clean]` is run by `tt package clean` of the whole package, with no
component named, after the backends of the components cleaned what their
tools left and before tt removes `_build/`. It is for what the project
generates beside its sources itself, which tt does not remove: what a
`pre_build` hook writes, say.

```toml
[hooks.pre_build]
command = "sh"
args = ["-c", "mkdir -p src/gen && date +'return %Y' > src/gen/year.lua"]

[hooks.clean]
command = "rm"
args = ["-rf", "src/gen"]
```

A hook that fails fails the command with exit code 2: a build before
anything is built for `pre_build`, and a clean before anything is removed
for `clean`. A tt older than the hook skips `[hooks.clean]` with a warning,
`unknown hook "clean" skipped`.

<!-- TODO(writers): app.manifest.lock - what it records, such as
bundled_tarantool_version - is not described yet. -->
