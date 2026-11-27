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
| `generate_version_lua` | no | `false` leaves `_version.lua`, `require('<name>._version')`, out of the build; `true` by default |
| `generate_rocks_lua` | no | `false` leaves `_rocks.lua`, `require('<name>._rocks')`, the rocks the package was built with, out of the build; `true` by default, whatever `generate_version_lua` says |

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

### The pinned runtime

`tt package resolve` pins the exact version of each program `[platform]`
names in `app.manifest.lock`, at its top:

```toml
lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0'
manifest_hash = 'toml1-sha256:...'
tarantool_version = '3.9.0'
tt_version = '3.0.0'
tcm_version = '1.3.1'   # with [platform].tcm set
```

The version is the one `tt binary` names the installation by, and the
product is the one the edition of the constraint names: `tarantool` for
`[ce]`, `tarantool-ee` for `[ee]`, `tt` or `tt-ee`, and `tcm`. A pin that
still satisfies `[platform]` is kept. A missing one is chosen in this order:

1. the installation of the project that satisfies the constraint: the
   active one, `.tarantool/bin/tarantool`, first, then the highest under
   `.tarantool/versions/<product>/`;
2. the Community tarantool and tt this tt runs with, when they are releases
   that satisfy it;
3. the newest release the sources of prebuilt archives of
   [`tt binary install`](../how-to/binaries.md) have for this machine; the
   customer zone is asked only when its credentials are set in
   `TT_CLI_EE_USERNAME` and `TT_CLI_EE_PASSWORD`.

A program none of them has a version of is left unpinned with a warning,
and the resolution succeeds. `tt package update` without a name chooses the
pins afresh, by the same order. A build, `tt package add` and `tt package
remove` keep the pins that still satisfy `[platform]` and pin nothing new.

The pins are what [an offline mirror](../concepts/rock-servers.md#a-mirror-for-offline-builds)
carries the programs of. A project whose active tarantool is another
version than the pinned one runs on it, with a warning.

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
  default product, their layout and `_version.lua` - and installs it into
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
| `namespace` | where the files and the modules of the component go in `.rocks/share/tarantool/` and `.rocks/lib/tarantool/`: the package name by default, `""` for the top level, `"a/b"` for `a/b/`; see below |
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

A `namespace` is `""` or segments of letters, digits, `_` and `-` joined
by `/`, a directory each: `namespace = "a/b"` puts the modules into
`.rocks/lib/tarantool/a/b/` and the files into
`.rocks/share/tarantool/a/b/`, and `require('a.b.<module>')` loads a
module, by the function `luaopen_a_b_<module>` of a native one. Several
components may have one namespace: their modules and files go into the same
directory. A namespace of the top level, `native` say, is a directory of
`.rocks/` that every other package may use too; one under the name of the
package, `<package>/native`, keeps clear of them. Another value fails the
manifest check before anything is built, at
`components.<name>.namespace`: `"myapp.native" has a dot, which require
reads as a directory: nest with "/", "myapp/native", for
require('myapp.native.<module>')`, `"../evil" has the segment "..": a
namespace is "" or segments of letters, digits, "_" and "-" joined by "/"`.

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
| `module`, `sources`, `include_dirs`, `libraries`, `library_dirs`, `rpath`, `defines` | `c`, `lua-c` | `module` and `sources` are required; an entry of `libraries` with a `/` or ending in `.a` is a library file |
| `pkg_config` | `c`, `lua-c` | packages whose flags pkg-config gives, `["openssl", "libsasl2 >= 2.1"]`; see below |
| `link` | `c`, `lua-c` | components of the package whose libraries the module links, what their `output` sends `to = "link"`; see below |
| `bundle` | `lua-c` | libraries the module links whose shared libraries tt copies beside it; see below |
| `cflags` | `c`, `lua-c` | flags of the compiles of the C and assembler sources, one per entry, after tt's own and before `CFLAGS` of the environment; see below |
| `cxxflags` | `c`, `lua-c` | flags of the compiles of the C++ sources, one per entry, after tt's own and before `CXXFLAGS` of the environment |
| `module` | `cargo` | builds the library of the package as the module of this name; a block with neither `module` nor `bin` builds the library under its own name |
| `platforms.<os>` | `c`, `lua-c` | more `include_dirs`, `libraries`, `library_dirs`, `rpath`, `pkg_config`, `defines`, `cflags`, `cxxflags`, `ldflags` for one OS, `linux` or `darwin`, after those of the build |
| `package` | `go`, `cargo` | the Go package, `.` by default, or the cargo package |
| `static` | all | `true`: what the build makes needs nothing of the machine it is installed on, and the build checks it; see below |
| `ldflags` | `go`, `c`, `lua-c` | flags of the linker, one per entry: of `go build -ldflags`, after tt's own; of the link of `c` and `lua-c`, before the objects; see below |
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
| `link` | a library, or a header of one, for the `c` and `lua-c` components built after it to link (`link`, below) | nowhere: it goes to `_build/link/<product>/<component>/`, a header to `include/` of it |

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
.rocks/share/tarantool/shop/_rocks.lua
.rocks/share/tarantool/shop/_version.lua
.rocks/share/tarantool/shop/init.lua
.rocks/share/tarantool/shop/rates.json
$ tt run -e "print(require('shop').rates().usd) os.exit()"
90
```

`rates/build.sh` and `rates/dist/` are not laid out: the default `include`
takes `*.lua` and `*.so`. `debug.sourcedir()` is the directory of the Lua
file that calls it, so the module finds the file wherever the package is
installed. Code that is not in the package reads it by its path from the
directory of the application, which is the working directory of its
instances and of `tt run` started in the project root:
`.rocks/share/tarantool/shop/rates.json`.

A `to` of another value is an error, `components.rates.build.output[0].to:
"etc" is not lib, share, bin or link`, and so is a table without `path`; a value
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
`TT_VERSION`, `TT_PLATFORM_OS` and `TT_PLATFORM_ARCH`, the platform it is
built for (see [Other platforms](#other-platforms)), and `TT_BUILD_JOBS`
(see [Jobs](#jobs)). When tt knows the
Tarantool it builds for, a build also gets `TT_TARANTOOL_PREFIX`, the
install prefix of that Tarantool, and `TT_TARANTOOL_INCLUDE_DIR`, the
directory of its `module.h` and `lua.h`, `<prefix>/include/tarantool`, which
the `c` and `lua-c` backends compile against; either is unset when tt does
not know it, `TT_TARANTOOL_INCLUDE_DIR` when `module.h` is not there (see
[Headers for rocks with a C part](../how-to/binaries.md#headers-for-rocks-with-a-c-part)
for where tt looks for them). `env` of the build cannot override them.
Where tt finds no Tarantool at all, or none of its headers, a `c` or
`lua-c` component fails the build before anything is built, for want of
the headers, naming the directories tt looked into.

`TT_BUILD_DIR` is a directory of the build's own for what it makes on the
way, out of the sources: object files, a build tree. It is
`_build/component/<component>/`, or that of `_build/target/<os>-<arch>/` for
a build for another platform (see [Other platforms](#other-platforms)),
there when the build starts, and it stays
between builds, so that a `make` build rebuilds what changed only;
`--rebuild` empties it first, and so does a build of a block that changed
since the last build. A `make` or `shell` build that writes there
rather than beside its sources leaves the directory of the component as it
is, and that of a path dependency, another checkout, too.

`c` and `lua-c` compile each of `sources`, relative to `cwd`, into an
object of `TT_BUILD_DIR`, and link the objects into one module,
`<module>.so`, `<module>.dylib` on macOS, in `TT_OUTPUT_DIR`. The
extension of a source says its language, and so its compiler and its
flags:

| Extension | Language | Compiler | Flags of the language |
| --- | --- | --- | --- |
| `.c` | C | `$CC`, `cc` by default | `cflags`, `$CFLAGS` |
| `.cc`, `.cpp`, `.cxx`, `.c++` | C++ | `$CXX`, `c++` by default | `cxxflags`, `$CXXFLAGS` |
| `.S`, `.s` | assembler, `.S` run through the preprocessor | `$CC` | `cflags`, `$CFLAGS` |

The extensions are case-sensitive, and `.C` is none: on macOS `a.C` and
`a.c` are one file. A source of another extension is an error -
`components.hashkit.build.sources[1]: "fnv.h" is not a source tt compiles:
want a name ending in .c, .cc, .cpp, .cxx, .c++, .S or .s; a prebuilt
library goes into libraries, by name or as a file` - and so is a source listed
twice. Each source is compiled on its own, the sources of a module at
once, as many as the machine has CPUs:

```console
$CC -O2 -g -fPIC -ffile-prefix-map=<project>=. -gno-record-gcc-switches <cflags> <$CFLAGS> \
    -I<include dir of Tarantool> -D<defines> -I<include_dirs> \
    -I<include/ of each of link> <pkg-config --cflags> \
    -MD -MF <object>.d -MQ <object> -c <source> -o <object>
$CXX -O2 -g -fPIC -ffile-prefix-map=<project>=. -gno-record-gcc-switches <cxxflags> <$CXXFLAGS> \
    -I<include dir of Tarantool> -D<defines> -I<include_dirs> \
    -I<include/ of each of link> <pkg-config --cflags> \
    -MD -MF <object>.d -MQ <object> -c <source> -o <object>
```

and the objects are linked with `$CXX` when a source is C++, with `$CC`
otherwise:

```console
$CXX -shared -Wl,-Bsymbolic-functions <ldflags> -o <module>.so <objects> \
    -L<library_dirs> -L<each of link> -l<libraries> -l<the libraries of link> \
    <pkg-config --libs> -Wl,-rpath,<rpath> <$LDFLAGS>
```

The flags of a compile are tt's, then the project's, then the user's:
`-O2 -g`, left out, both, when the variable of the language, `CFLAGS` or
`CXXFLAGS`, is set, even to nothing, and `-fPIC`; then `cflags` or
`cxxflags`; then `CFLAGS` or `CXXFLAGS` of the environment, split on
blanks, which come last and so win a conflict, as `CFLAGS` comes after
`AM_CFLAGS` in autotools: `CFLAGS="-O0 -g"` builds the C sources for a
debugger, and `CFLAGS=-Wno-error` builds a project whose `cflags` have
`-Werror` with a compiler that warns of more. The module has its debug
information by default, as a `cmake` build of `RelWithDebInfo` does, so a
module that crashes Tarantool has a backtrace with its lines;
`tt package pack --debuginfo strip` packs it without
([Packages](../concepts/packages.md#debug-information)).

The objects name no path of the machine: `-ffile-prefix-map=<project>=.`
makes the root of the project `.` in the debug information and in
`__FILE__`, and `-gno-record-gcc-switches` keeps the command line out of
it, so that the same sources built in another directory make the same
module. That is so on Linux; on macOS the debug information stays in the
objects, and the module keeps their paths, under `_build/` of the project,
in its debug map, which `dsymutil` and a debugger read. A debugger finds the sources when it is started in the project, or
told the directory, `set substitute-path . /path/to/project` of gdb. Each
of the two is given only when the compiler takes it: tt compiles a source
of nothing with it once a compiler and a language, which has to succeed
and print nothing, as gcc warns of some flags it does not know rather than
fails, and keeps the answer in `TT_BUILD_DIR` with the identity of the
compiler. `cflags` and `CFLAGS` come after them, and so may map other
directories. The flags of one language do not reach the
sources of the other - `-std=c11` would fail a C++ compile - while
`defines`, `include_dirs`, `-fPIC` and the headers of Tarantool reach both.
The link gets none of the flags of the compilers: a flag both the compiles
and the link need, `-pthread`, `-fsanitize=address`, `-flto`, `-m32` or
`--target=...`, goes into `cflags` (and `cxxflags`) and into `ldflags`.
`ldflags` go before the objects, as make's `$(CC) $(LDFLAGS) -o out objs
$(LDLIBS)` has them, so that `-Wl,--as-needed` acts on the `-l` that follow;
`LDFLAGS` of the environment ends the line. A list of `platforms.<os>` goes
after the one of the build; `<os>` is `linux` or `darwin`, the OSes tt
builds for, and another, `macos` say, is an error. On macOS `-shared` is
`-bundle -undefined dynamic_lookup -all_load`, and no
`-Wl,-Bsymbolic-functions` is given. The linker of `zig cc` and `zig c++`
crashes on `-bundle`, so they link the module as a dylib, which Tarantool
loads as it loads a bundle: `-shared -undefined dynamic_lookup -all_load
-Wl,-install_name,@rpath/<module>.dylib`, the install name in place of the
path of `-o`, which is of the machine the module is built on.

On Linux `-Wl,-Bsymbolic-functions` binds the calls of the module to the
functions it defines itself. Without it the loader resolves a call to a
function by the order of the process, the Tarantool executable first: a
module that defines `curl_easy_init`, which Tarantool Enterprise exports
with the 80 other functions of curl it links in, called the one of
Tarantool even from within itself. The variables of the module are left
as they are. `zig cc` and `zig c++` refuse the flag, and their link goes
without it. On macOS a call of a module to a function it defines is bound
to it at its link, by the two-level namespace, without a flag.

Once the module is linked, tt compares the functions and the variables it
exports with those the `tarantool` of the build exports, and warns of a
module that exports a name Tarantool exports too, naming up to five of
them: the other modules, `ffi`, and the module where its calls are not
bound to its own, get the one of Tarantool. A function that is not of the
API of the module is better `static`, or hidden,
`__attribute__((visibility("hidden")))` or `-fvisibility=hidden` in
`cflags`. The entry point of a `lua-c` module is not compared: `require`
finds it in the module itself. With no `tarantool`, or one whose symbols
cannot be read, nothing is compared.

```text
   ⚠ .../lib/hashkit/fnv.so exports 2 symbols that the tarantool /opt/sdk/tarantool exports too: crc32, curl_easy_init; the loader resolves a name to one symbol, that of Tarantool, for the other modules, ffi, and the module itself where its calls are not bound to its own: make them static, or hidden (__attribute__((visibility("hidden"))) or -fvisibility=hidden)
```

`library_dirs` is where the linker looks for `libraries`, not where the
loader looks when Tarantool loads the module: a directory of the build
machine means nothing on the machine the package is installed on. The
loader looks where `rpath` says, each entry `-Wl,-rpath,<entry>`:
`$ORIGIN`, or `${ORIGIN}`, is the directory of the module, so
`rpath = ["$ORIGIN"]` finds a library installed beside the module, and is
`@loader_path` on macOS. An absolute entry names a directory of the machine
the module is installed on. An empty entry, a relative one - which the
loader would resolve against the working directory of the process - and
one with a comma are errors. `zig cc` building for the host adds an rpath
of each directory of a shared library it links, as zig does. Tarantool
loads a module of `c`, a module of `box.schema.func`, from a copy in a
temporary directory, so `$ORIGIN` of such a module is that directory.

An entry of `libraries` is the name of a library, `-l<name>`, or, with a
`/` or ending in `.a`, a library file, relative to `cwd`, which the link
takes as it is, in its place among the `-l`: `libraries =
["vendor/libfoo.a"]`. A name of GNU ld's `:<file>` form, `:libfoo.a`,
stays a name.

`pkg_config` lists packages pkg-config knows, each as pkg-config takes
it, `"openssl"` or `"libsasl2 >= 2.1"`. The build runs `pkg-config --cflags`
and `pkg-config --libs` for them, with `--static` for a build with
`static = true`, splits what they print into words as a shell splits
them, and puts the flags of the compiler after `defines` and
`include_dirs` of every compile, C and C++, and those of the libraries
after `libraries`. pkg-config is `PKG_CONFIG` of the environment tt runs
in, split as `CC` is, `pkg-config` by default; it runs with the
environment of the build, so `PKG_CONFIG_PATH` of `env` reaches it, while
`PKG_CONFIG` in `env` is an error. A pkg-config that is not in `PATH`, or
a package it does not know, fails the build before anything is built:

```text
⨯ building component "zcrc": pkg_config[0]: "zlib-ng": pkg-config --exists --print-errors zlib-ng: exit status 1: Package zlib-ng was not found in the pkg-config search path.
  Perhaps you should add the directory containing `zlib-ng.pc'
  to the PKG_CONFIG_PATH environment variable
  Package 'zlib-ng' not found
```

`link` lists components of the package whose libraries the module links:
what the `output` of their builds sends `to = "link"` (`make`, `shell`,
`cmake`), which tt copies into `_build/link/<product>/<component>/`, a
header into `include/` of it. The compiles get `-I` of that `include/`,
and the link `-L` of the directory and `-l<name>` of each
`lib<name>.a`, `.so` or `.dylib` there, after `libraries`; a library
`libraries` names keeps its place there. Every product of the component
builds the components of `link` before it, in the order of its
`components`, or the manifest is invalid:

```text
⨯ invalid manifest: products.default.components: component "hashes" links the libraries of component "fnv", and the product builds it after "hashes": list "fnv" before "hashes"
```

A manifest without `[products]` builds its components in the order of
their names. `tt package build hashes`, which builds one component, needs
the libraries of `fnv` from an earlier build: `link[0]: component "fnv"
has not been built: build it first, as a build of the whole product does
before "hashes"`. A component of `link` whose output sends no library
there is an error too. See
[Link C libraries into a module](../how-to/c-libraries.md).

`bundle` of `lua-c` lists libraries the module links whose shared
libraries go with it: a name of `libraries`, of a shared library file of
`libraries`, of the libraries of `link` or of the `-l` of `pkg_config`.
The build finds the shared library the link takes for it - the file, or
`lib<name>.so` (`.dylib` on macOS) in `library_dirs`, the directories of
`link` and the `-L` of pkg-config, or where the compiler finds it,
`$CC -print-file-name=lib<name>.so` - follows its links, and copies it
beside the module under the name the module asks the loader for, its
SONAME: `bundle = ["z"]` copies `/usr/lib/x86_64-linux-gnu/libz.so.1.3.1`
as `libz.so.1`. The module gets `-Wl,-rpath,$ORIGIN`, unless `rpath` has
`$ORIGIN`, so it loads the copy wherever it is installed: tt installs it
beside the module, into `.rocks/lib/tarantool/<namespace>/`, and packs it.
The copy is there before the module is linked, so the load of the module
into the `tarantool` of the build, once it is linked, finds it as well.
A library of `bundle` is linked shared, with `static = true` as well. A
library the bundled one needs is not bundled with it: tt warns of it,
`.../libfoo.so.1, which bundle copies beside the module, needs libbar.so.1,
which is not bundled`, and a build with `static = true` fails on it. A
name the module does not link, a shared library that is not found, and a
module that does not need the library in the end - its static library
was linked, or `-Wl,--as-needed` dropped it - fail the build. On macOS the
copy gets the install name `@rpath/<name>` and an ad hoc signature before
the module is linked against it, with `-Wl,-rpath,@loader_path`. `bundle`
of `c` is an error: Tarantool loads a module of `box.schema.func` from a
copy in a temporary directory, where the libraries beside the module are
not.

`CC` and `CXX` are split into words as a shell splits a command, with
`'...'`, `"..."` and `\` and without expansions, so a compiler may be run
through a wrapper or with arguments of its own: `CC="ccache cc"`,
`CC="zig cc" CXX="zig c++"`, `CC="'/opt/my tools/gcc'"`. The first word is
looked up in the `PATH` of the build. `CC`, `CXX`, `CFLAGS`, `CXXFLAGS` and
`LDFLAGS` are those of the environment tt runs in, and the compiler reads
none of them: in `env` of the build each is an error, as it would reach
nothing, and the flags of the project go into `cflags`, `cxxflags` and
`ldflags`. So are `CPPFLAGS`, `LIBS` and `LDLIBS`, which neither tt nor the
compiler reads: `defines`, `include_dirs`, `cflags` and `cxxflags` take
what `CPPFLAGS` would carry, `libraries` and `library_dirs` what `LIBS`
would. `tt -V package build` shows each command before it runs it; a
compile or a link that fails shows its command and what it printed in the
error.

What the compiler printed for a compile that succeeded, its warnings, is a
warning of the build, once an object, after a line naming the source, the
output as the compiler printed it, in the order of `sources` once the
compiles are done. It is kept with the object, and shown again by a build
that takes the object from the cache, so that a warning does not vanish
with the second build:

```text
   ⚠ compiling datapath.c:
datapath.c: In function 'mc_set':
datapath.c:12:9: warning: unused variable 'len' [-Wunused-variable]
   12 |     int len;
      |         ^~~
```

A build with `static = true` links each library of `libraries`, of `link`
and of the `-l` of pkg-config from its static library: `lib<name>.a` in
`library_dirs`, the directories of `link` and the `-L` of pkg-config, or
the one the compiler finds, `$CC -print-file-name=lib<name>.a`, the path
of which takes the place of `-l<name>`. This works with every linker -
GNU ld, lld, ld64, `zig cc` - where `-Wl,-Bstatic` is GNU ld's alone; a
linker takes the shared library of `-l<name>` when both are there. The
libraries of the C library and of the GCC runtime, `c`, `m`, `dl`,
`pthread`, `rt`, `gcc_s`, and `System` on macOS, which `static = true`
allows, are linked as they are, and so are the libraries of `bundle` and
a library with no static library found, which the check of `static = true`
then names. What a static library brings into the module the module
exports, and `-Wl,-Bsymbolic-functions` binds the calls of the module to
it: the warning of the symbols Tarantool exports too names those of the
library as well, which `-Wl,--exclude-libs,ALL` in `ldflags` hides (not of
`zig cc`, which refuses it). A module is a shared object, which takes position-independent
code only, so the static library is to be built with `-fPIC`; one that is
not - `libz.a` of Debian and Ubuntu is not - fails the link, and the error
says so:

```text
⨯ building component "zcrc": linking: exit status 1
  cc -shared -Wl,-Bsymbolic-functions -o .../zcrc/_build/rock/default/lib/zcrc/crc.so .../zcrc/_build/component/zcrc/obj/crc.c.de186406.o /usr/lib/x86_64-linux-gnu/libz.a
  /usr/bin/x86_64-linux-gnu-ld.bfd: /usr/lib/x86_64-linux-gnu/libz.a(zutil.o): warning: relocation against `z_errmsg' in read-only section `.text'
  /usr/bin/x86_64-linux-gnu-ld.bfd: /usr/lib/x86_64-linux-gnu/libz.a(zutil.o): relocation R_X86_64_PC32 against symbol `z_errmsg' can not be used when making a shared object; recompile with -fPIC
  /usr/bin/x86_64-linux-gnu-ld.bfd: final link failed: bad value
  collect2: error: ld returned 1 exit status
  hint: the static library /usr/lib/x86_64-linux-gnu/libz.a was not built with -fPIC: a module is a shared object, which takes position-independent code only: build the library with -fPIC (CFLAGS=-fPIC, or -DCMAKE_POSITION_INDEPENDENT_CODE=ON of cmake), or link its shared library
```

The module is linked again when a file it links changed: a file of
`libraries`, a static library a build with `static = true` took, a
library of `link` or of `bundle`, or one of the libraries of `libraries`
in `library_dirs`; and when what pkg-config gives or `PKG_CONFIG_PATH`,
`PKG_CONFIG_LIBDIR` and `PKG_CONFIG_SYSROOT_DIR` changed, which an object
is compiled again for too.

A module of C++ needs the C++ library. On Linux a build with
`static = true` links `libstdc++` into the module, with
`-static-libstdc++ -Wl,--exclude-libs,ALL`, the second keeping its
symbols out of what the module exports: Tarantool has `libstdc++` linked
in, so the machine it runs on may have no `libstdc++.so.6`. The static
`libstdc++` of a new gcc needs the glibc of the machine it was built on,
though - that of gcc 15 needs glibc 2.38, where a module of C needs 2.14 -
so such a module loads only where Tarantool runs on a glibc as new; the
build warns when it is newer than the glibc of the Tarantool it is for.
Without `static` the module needs the `libstdc++.so.6` of the machine. On
macOS the module needs `/usr/lib/libc++.1.dylib`, the C++ library of the
system, which Tarantool needs itself, and `static = true` allows it. `zig
c++` (`CXX="zig c++"`, or any compiler whose words have a `zig` followed by
`cc` or `c++`) links its own `libc++` into every module, statically and its
symbols hidden, so its link gets neither flag - zig refuses
`--exclude-libs` - and its module needs no C++ library, `static` or not.

```toml
[components.memcached.build]
backend = "lua-c"
module = "datapath"
sources = ["datapath.c", "ring.cpp"]
cflags = ["-std=gnu99", "-Wall", "-Wextra", "-Werror"]
cxxflags = ["-std=c++17", "-Wall"]

[components.memcached.build.platforms.linux]
ldflags = ["-Wl,--as-needed"]

[components.memcached.build.platforms.darwin]
cflags = ["-Wno-deprecated-declarations"]
```

A C++ source of `lua-c` includes the Lua API as `#include <lua.hpp>`:
`lua.h`, `lauxlib.h` and `lualib.h` of Tarantool declare no `extern "C"`,
so a C++ source that includes them declares the Lua API by C++ names, and
the module does not load: `undefined symbol:
_Z15luaL_checkstackP9lua_StateiPKc`. `module.h` declares its own functions
`extern "C"`. The entry point of a module of C++ is `extern "C"` too.

`lua-c` checks the module once it is linked: the build fails when the
module exports no `luaopen_<name>`, the function `require('<name>')`
calls - `<name>` is `<namespace>.<module>`, the `/` of the namespace made
`.`, or `<module>` with `namespace = ""`, its dots made `_` and the part
up to its first `-` dropped, as Lua does: `require('my-app.fast')` calls
`luaopen_app_fast`, and `require('myapp.native.fast')`
`luaopen_myapp_native_fast` -
and when the module needs functions of the Lua API by their C++ names. Each
slice of a universal Mach-O file is checked. A module of `c`, whose
functions Tarantool calls by the names of `box.schema.func`, is not.

Then the module of either backend is loaded into the `tarantool` of the
build, when the build is for the machine it runs on: `tarantool -e` runs
`package.loadlib(<module>, "luaopen_<name>")` for `lua-c`, which looks the
entry point up without calling it, and `package.loadlib(<module>, "*")`
for `c`. LuaJIT opens the module with `dlopen(RTLD_NOW)`, so each symbol
it needs is resolved, as `require` and `box.schema.func` resolve them, and
a module that would not load fails its build with what the loader said -
a library forgotten in `libraries`, a function of the headers of a newer
Tarantool than the one it runs in:

```text
⨯ building component "hashkit": .../lib/hashkit/fnv.so: the module does not load into /opt/sdk/tarantool: .../lib/hashkit/fnv.so: undefined symbol: deflate
```

The load runs once a module is linked, not for a module up to date, for 30
seconds at most. With no `tarantool`, or one that does not run the script,
nothing is loaded.

```text
⨯ building component "hashkit": .../hashkit/_build/rock/default/lib/hashkit/fnv.so: the module needs functions of the Lua API by their C++ names: _Z17luaL_checkintegerP9lua_Statei, _Z15lua_pushintegerP9lua_Statel: lua.h, lauxlib.h and lualib.h of Tarantool declare no extern "C", so a C++ source includes them through <lua.hpp>, which does, or within extern "C" { }
⨯ building component "hashkit": .../hashkit/_build/rock/default/lib/hashkit/fnv.so: the module exports no luaopen_hashkit_fnv, the function require("hashkit.fnv") calls: define int luaopen_hashkit_fnv(lua_State *L), extern "C" in C++, or set module to the name the module is for
```

A build compiles only what changed since the last one. An object is
compiled again when its command line, the compiler - its program, the size
and the time of change of that file, what `-dumpmachine` and `--version`
print - an environment variable the compiler reads (`CPATH`,
`SOURCE_DATE_EPOCH`, `SDKROOT`, `MACOSX_DEPLOYMENT_TARGET` and the like,
`C_INCLUDE_PATH` of a C or assembler source, `CPLUS_INCLUDE_PATH` of a C++
one; not `TMPDIR`), its source, or a
file its last compile read changed: each header `-MD` listed, those of
Tarantool and of the system included. The module is linked again when an
object, the link command, the compiler that links, `LIBRARY_PATH` or a
file of `libraries` in `library_dirs` changed, or when the module is gone.
A file a flag alone names, a version script of `ldflags` say, and a
library found elsewhere are not followed: `tt package build --rebuild`
builds everything again, as does a change of the build block. The objects
and what the build records of them stay in
`_build/component/<component>/obj/`.

Each build writes `_build/component/<component>/compile_commands.json`
too, the compilation database of the sources of the component, a command
for each, for clangd and other tools that read one. clangd looks for it
in the directories above a source and in their `build/` only, so it is
named in a `.clangd` file of the project, for each component of C:

```yaml
If:
  PathMatch: memcached/.*
CompileFlags:
  CompilationDatabase: _build/component/memcached
```

`CompilationDatabase` is the directory of the file, relative to the
directory of `.clangd`. For a project of one such component, a link in the
root of the project does as well, which clangd finds above every source:
`ln -s _build/component/memcached/compile_commands.json .`, with
`/compile_commands.json` in `.gitignore`.

LuaRocks builds the source rock of a package of `c` and `lua-c` too, by
the script its rockspec carries (see
[Packing a rock](../concepts/packages.md#packing-a-rock)). The script
compiles each source and links the module as tt does, by the same flags in
the same order and into the same file, with the toolchain of LuaRocks:

- a C or assembler source compiles with `$CC` and `$CFLAGS` of LuaRocks,
  `-O2 -fPIC` by default, which take the place of tt's `-O2 -g` as
  `CFLAGS` of the environment does under tt; a C++ one with `CXX` of the
  environment, `c++` by default, and `CXXFLAGS`, `-O2` by default;
- the module is linked with `$LIBFLAG` of LuaRocks, `-shared`, or
  `-bundle -undefined dynamic_lookup -all_load` on macOS, then
  `-Wl,-Bsymbolic-functions` on Linux, and ends with `LDFLAGS` of the
  environment;
- the headers of Tarantool are `LUA_INCDIR` of LuaRocks, and a build
  where it has no `module.h` fails, saying so;
- the lists of `platforms.<os>` are those of the OS the rock is built on,
  as `uname` names it; pkg-config, `PKG_CONFIG` of the environment, runs
  where the rock is built; the libraries of `link` are those the components
  made, looked for when the build runs; `$ORIGIN` of `rpath` is
  `@loader_path` on macOS;
- `CC`, `CXX`, `PKG_CONFIG`, the flags of the environment and what
  pkg-config prints are split on blanks, a quote being a character of a
  word, and the first word of each compiler and of pkg-config is looked for
  in `PATH` before anything of the component is compiled; tt's handling of
  `zig cc` and `zig c++` is not done.

The script prints each compile and the link before it runs it. It lacks
the cache of the objects and `compile_commands.json`, the checks of the
module once it is linked - the entry point of `lua-c`, the symbols
Tarantool exports too and the load into Tarantool - the debug information
of `-g`, and the flags that keep the paths of the project out of the
objects: its comments say so, and the pack of the source rock warns of it.
A build of `static = true`, whose libraries tt takes from their static
libraries and whose module it checks, and one of `bundle`, whose libraries
tt copies beside the module, are made with tt alone: the source rock of
their package fails under LuaRocks, saying why, and the pack says so:

```text
⚠ the source rock builds with tt only, which builds it from its manifest: the component "hashkit" is built by the tt backend "lua-c" with tt alone, for components.hashkit.build.static: tt links a module of static = true of the static library it finds of each library, and checks that it needs no library of the machine, which a build without tt does not; LuaRocks, tt rocks included, fails to build it, saying so
```

`cmake` builds the CMake project of `cwd`, where its `CMakeLists.txt` is,
out of the source, in `TT_BUILD_DIR`:

```console
cmake -S <cwd> -B _build/component/<component> -DCMAKE_BUILD_TYPE=<build_type> \
    -D<name>=<value>... -DCMAKE_PREFIX_PATH=<prefix of Tarantool>
cmake --build _build/component/<component> --config <build_type> --parallel <jobs> \
    --target <targets>...
```

and copies the files `output` lists, relative to the build tree, where each
goes; a file the build did not make fails it. The prefix of
Tarantool comes after the prefixes `variables` gives `CMAKE_PREFIX_PATH`, so
`find_path(... tarantool/module.h)` of the usual `FindTarantool.cmake` finds
its headers. The build tree stays between builds, and cmake rebuilds what
changed in it; `--rebuild` configures a new one, and so does a build of a
project that was moved. `<jobs>` is that of `-j` (see [Jobs](#jobs)), or
the number of CPUs; without `-j`, a `CMAKE_BUILD_PARALLEL_LEVEL` that is set
is the number of jobs instead. `CMAKE_GENERATOR` in `env` picks another
generator. On macOS a
`.so` module is copied as `.dylib`, which Tarantool loads there. A program
of the build is an entry of `output` with `to = "bin"`. CMake 3.15 or newer
must be on `PATH`. The component is configured with the policy version its
`cmake_minimum_required` asks for: CMake 4 refuses one older than 3.5, and
the `CMAKE_POLICY_VERSION_MINIMUM` tt sets for the rocks it builds is not
passed on, while one set in `env`, or in the environment tt runs in, is.
LuaRocks builds the source rock of the package by the same commands (see
[Without tt](#without-tt)).

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
included. A `target/` of a component cargo builds is not laid out. cargo
builds with `--locked`, from the versions the `Cargo.lock` of the workspace
pins: a package without one fails the build before anything is built,
naming `cargo generate-lockfile`. LuaRocks builds the source rock of the
package by the same commands, into a target directory of its own (see
[Without tt](#without-tt)). See
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
and later, unless `ZIG_LOCAL_PKG_DIR` is set, exported where tt runs or
by the `env` of the block; zig
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
`build.zig.zon` itself as it builds, under LuaRocks too, which builds the
source rock of the package by the same commands (see
[Without tt](#without-tt)). See
[Build a module in Zig](../how-to/zig-modules.md).

`static = true` asks what the build makes to need nothing of the machine
the package is installed on, and the build checks that it does: a file that
needs more fails the build, with exit code 2, naming the file and what it
needs.

- A program needs no loader and no shared library. `go`, `cargo` and
  `zig` are asked to build one so, for Linux only - `cargo` builds the
  binary for musl, the target `<arch>-unknown-linux-musl`, which the build
  checks is installed and `rustup target add` installs, and the module of
  the same block as any module; `zig`
  builds a program of a block without a module for musl, `-Dtarget=<arch>-linux-musl`,
  when the `build.zig` takes `-Dtarget` and `options` names no `target`,
  and one of a block with a module for the target of the module; what
  `cmake`, `make` and `shell` build is only checked.
- A module needs no shared library but those of the C library, which
  Tarantool, loading it, has loaded itself: on Linux `libc.so.6`,
  `libm.so.6`, `libdl.so.2`, `libpthread.so.0`, `librt.so.1`, the loader,
  `ld-linux-*` or, of musl, `ld-musl-*` and `libc.musl-*`, and
  `libgcc_s.so.1`, the GCC runtime every build of Tarantool needs; on macOS
  `/usr/lib/libSystem.B.dylib` and `/usr/lib/libc++.1.dylib`, the C++
  library of the system, which Tarantool needs too, and every slice of a
  universal file - and the libraries it finds beside itself among the
  native files of the build, by an rpath of `$ORIGIN` or, on macOS,
  `@loader_path/<file>` or `@rpath/<file>` with an rpath of `@loader_path`:
  those are installed and packed with it. Its native files are those the
  build writes into `TT_OUTPUT_DIR` and those of the component the layout
  takes, and a library beside it is one of them, checked by the same rules:
  what it needs itself is reported by name. The backend is asked for
  nothing but by `c` and `lua-c`, which link `libstdc++` into a module of
  C++ on Linux and every library from its static library where they find
  one (above): a library a module needs, such as `libsasl2.so.2`, is
  linked in statically, from its `.a`, bundled beside the module
  (`bundle` of `lua-c`), or loaded at run time with `dlopen`, and a C++
  module of another backend links `libstdc++` in with
  `-static-libstdc++`. A module that needs a newer glibc than the
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

#### Jobs

`-j N`, `--jobs N`, of `tt package build`, `tt package pack` and `tt test`
runs at most `N` jobs at once in each component build; `0`, the default,
is one a CPU. Each backend hands it to its tool:

| Backend | With `-j N` | Without |
| --- | --- | --- |
| `c`, `lua-c` | at most `N` compiles at once | one a CPU |
| `cmake` | `cmake --build --parallel N`, whatever `CMAKE_BUILD_PARALLEL_LEVEL` says | `--parallel` with the number of CPUs, or no `--parallel` when `CMAKE_BUILD_PARALLEL_LEVEL` is set: cmake reads it then |
| `cargo` | `cargo build -j N`, `cargo rustc -j N` | no `-j`: cargo's own default, `build.jobs` or `CARGO_BUILD_JOBS` |
| `go` | `go build -p N` | no `-p`: go's own default |
| `zig` | `zig build -jN` | no `-j`: zig's own default |
| `make`, `shell` | `TT_BUILD_JOBS=N` | `TT_BUILD_JOBS`, the number of CPUs |

Every build and hook gets `TT_BUILD_JOBS`, which is never `0`. tt gives
`make` no `-j` of its own, since not every Makefile builds right in
parallel: a build that does runs it itself, with the `shell` backend
`command = 'sh'` and `args = ['-c', 'make -j"$TT_BUILD_JOBS"']`, or a
Makefile that runs `$(MAKE) -j$(TT_BUILD_JOBS)` for its parts. The path
dependencies are built with the same jobs.

The jobs say how fast a component is built, not what is built: they are
not an input of an `incremental` build, nor of the objects of `c` and
`lua-c`, and a build with other jobs skips what the build before made.

#### Other platforms

`--target OS/ARCH` of `tt package build` and `tt package pack` builds the
components for another platform than the machine tt runs on: `linux/amd64`,
`linux/arm64`, `darwin/amd64` or `darwin/arm64`. The token of an archive,
`linux-arm64`, names a platform too, and so do `aarch64` and `x86_64` for
the architecture and `macos` and `macosx` for darwin, in any case; another
platform is a usage error. Without `--target` a build is for the machine.
`TT_PLATFORM_OS` and `TT_PLATFORM_ARCH` of every build and hook are the
platform of the build, as `GOOS` and `GOARCH` spell it. Each backend
builds for it, or refuses it:

| Backend | For another platform | Refused |
| --- | --- | --- |
| `cargo` | the Rust target of the platform, `<arch>-unknown-linux-gnu`, or `-musl` for a static program, `<arch>-apple-darwin` for darwin; a Linux one linked with `zig cc` (see [Rust targets and other platforms](../how-to/rust-modules.md#rust-targets-and-other-platforms)) | darwin off macOS; zig not on `PATH`; a Rust target not installed |
| `go` | `GOOS` and `GOARCH` of the platform | `cgo = true`, which compiles C with the C compiler of the machine |
| `zig` | `-Dtarget=<arch>-linux-gnu`, `<arch>-linux-musl` for a static program alone, `<arch>-macos` for darwin | a `build.zig` that declares no `-Dtarget`, as `b.standardTargetOptions` does; a target of `options` |
| `make`, `shell` | `TT_PLATFORM_OS` and `TT_PLATFORM_ARCH`, which the build reads | |
| `c`, `lua-c`, `cmake` | | always: they compile with the toolchain of the machine |

A refused build fails with exit code 1 before a hook or anything else
runs, naming the component, the backend and the platform:

```text
⨯ building component "cmod": the build cannot be made for the target platform: the c backend builds for the machine tt runs on alone, linux-amd64, and the build is for linux-arm64
```

A component with no build block, Lua alone, is the same for every
platform. The modules and the programs a build made are checked against
the platform of the build, for the machine as well: a module or a program
of another platform fails the build with exit code 2, as a `make` or
`shell` build that compiled for the machine whatever `TT_PLATFORM_OS` and
`TT_PLATFORM_ARCH` said makes one.

A build for another platform keeps its state apart, in
`_build/target/<os>-<arch>/`: the stage, the records of what the components
made and of the incremental builds, `TT_BUILD_DIR`,
`_build/target/<os>-<arch>/component/<component>/`, and the target
directory of cargo. A build for one platform takes nothing a build for
another made as its own, or as up to date, and removes nothing of it. Its
package is installed into `_build/target/<os>-<arch>/tree/`, not into
`.rocks/`, whose modules and programs are those of the machine, which
Tarantool loads and `tt instance` runs:

```console
$ tt package build --target linux/arm64
   • built myapp for linux-arm64 into .../myapp/_build/target/linux-arm64/tree: a build for another platform is not installed into .rocks/
```

The dependencies of the lock are installed into `.rocks/` as for any build,
and built for the machine, as the path dependencies and the source rocks
are: they are what the builds run there. `tt package pack --target` packs
what the build made (see [Packing](../concepts/packages.md#packing)).
`tt package clean <component>` removes what the builds for every platform
made of the component, and the trees of the platforms; `tt package clean`
removes all of `_build/`. `tt package fetch` and `tt test` build for the
machine alone.

#### Without tt

LuaRocks builds the source rock of a package, `tt package pack --format
rock-src`, by `tt-rock.sh`, a script of sh its rockspec carries (see
[Packing a rock](../concepts/packages.md#packing-a-rock)), when every build
of the product and of its `pre_build` and `post_build` hooks is made
without tt too. The script runs each build by the commands tt runs, for the
machine it runs on: there is no `--target` and no `-j`, `TT_BUILD_JOBS`
being one a CPU, `.tarantool/bin` is not on `PATH`, and `TT_BUILD_DIR` is
empty when the build begins. The Tarantool of the build is that of
`LUA_INCDIR` of LuaRocks, `TT_TARANTOOL_INCLUDE_DIR`, and
`TT_TARANTOOL_PREFIX` is its prefix when it ends with `/include/tarantool`.

| Backend | The script | What it lacks against tt, and refuses |
| --- | --- | --- |
| `make`, `shell` | the command of the build, that of `shell` by `env -- <command> <args...>`, which runs the program of `PATH` as tt does, even one named as a word of sh such as `test`; then a copy of each file of `output` | nothing; a `command` with `=` in it, which `env` takes for a variable, is made with tt alone |
| `c`, `lua-c` | the compiles and the link of the build, by the flags of tt in their order, with `$CC`, `$CFLAGS` and `$LIBFLAG` of LuaRocks and `CXX`, `CXXFLAGS` and `LDFLAGS` of the environment (see [`c` and `lua-c`](#componentsnamebuild) above) | the cache, `compile_commands.json`, the checks of the module, `-g` and the flags that keep the paths of the project out of the objects; `static = true` and `bundle` are made with tt alone |
| `cmake` | `cmake -S . -B "$TT_BUILD_DIR"` with the `-D` of the build and the prefix of Tarantool on `CMAKE_PREFIX_PATH`, and `cmake --build` with `--parallel "$TT_BUILD_JOBS"`, or none when `CMAKE_BUILD_PARALLEL_LEVEL` is set; then a copy of each file of `output` from the build tree, a `.so` module as `.dylib` on macOS | nothing; cmake takes `CC` and `CFLAGS` of LuaRocks from the environment, as it takes those of the environment of tt |
| `go` | `go build` of the build, for the `GOOS` and `GOARCH` of `go env GOHOSTOS` and `GOHOSTARCH` | nothing; `static = true` fails on macOS, as with tt |
| `cargo` | `cargo rustc --lib --crate-type cdylib` and `cargo build --bin` of the build, with `CARGO_TARGET_DIR` set to `TT_BUILD_DIR` whatever `env` and the environment say, a static program for `<arch>-unknown-linux-musl`; then a copy of the library and of the binary cargo built there | the crates of a mirror of `tt package download`: cargo downloads those `Cargo.lock` pins; a workspace without `Cargo.lock`, a library of no `package` at the root of a workspace with no package of its own, a static program on macOS and a musl target that is not installed fail, as with tt |
| `zig` | `zig build` of the build, its prefix, cache and packages in `TT_BUILD_DIR`, `--pkg-dir` for zig 0.17 and later, a static program alone for `<arch>-linux-musl` when the `build.zig` takes `-Dtarget`; then a copy of the module and of the program | nothing; a static program fails on macOS, as with tt |

A build of another backend is made with tt alone: the build of the
rockspec fails, saying which component needs tt, and the pack of the source
rock warns of it. So is a build whose `env` sets a variable of a name sh
has no variable of, one the script keeps a value of its own in, of a name
beginning with `tt_` or `cc_`, or one that changes how sh runs the script:
`IFS`, `CDPATH`, `PWD`, `OLDPWD`, `ENV`, `PS1`, `PS2`, `PS4`, `OPTIND`,
`OPTARG`, `LINENO`, `PPID`, `SHELLOPTS`, `BASHOPTS` and `BASH_ENV`. What the script of a backend lacks is in the comments of
`tt-rock.sh`, and the pack warns of it once:

```text
   ⚠ LuaRocks builds the source rock by tt-rock.sh of its rockspec, which lacks what a build of tt has: the component "fastrs": the crates of a mirror of tt package download, which a build of tt takes offline: cargo downloads those Cargo.lock pins
```

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
files twice, and a product's `_version.lua` goes under the name of its rock,
`<rock>/_version.lua`, rather than of the package. `tt package pack --format
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
