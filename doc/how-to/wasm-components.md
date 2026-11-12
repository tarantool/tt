# Run WebAssembly components (experimental)

[tarantool-wasm](https://github.com/tarantool/tarantool-wasm-rs) is a
Tarantool module that runs WebAssembly components next to the data:
application code written in Python, JavaScript, Go, Rust, C or C# and called
from Lua with `require('wasm')`. The components are built by
[tarawasm](https://github.com/mandesero/tarawasm).

`tt wasm prepare` sets a project up for both until tt builds the components
itself, and `tt wasm new` adds a component to the package. They need Linux
x86-64 or macOS arm64, Tarantool 3.6 or newer, and Python 3.10 or newer for
tarawasm. Linux arm64 is not supported: tarantool-wasm has no build for it, so
no rock server has a `wasm` rock to install there unless one packs a local
build (`tt wasm rock --from`).

This page builds a component in Python, `add(a, b)`, into a package named
`myapp`. [An HTTP API in a WebAssembly component](../write-your-app/http-api-in-wasm.md)
is a whole application: an HTTP server in a component, writing to a space.

## Prepare the project

In a package directory, the one with `app.manifest.toml`:

```console
$ tt wasm prepare --lang python
declared wasm 0.1.0 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
installing tarawasm
...
checking the python toolchain
python toolchain:
  python3          >=3.10.0           3.14.4     ok         /usr/bin/python3
  wasm-tools       1.261.0            1.261.0    ok         .../.tarantool/bin/wasm-tools (installed by tt)
  wkg              0.16.1             0.16.1     ok         .../.tarantool/bin/wkg (installed by tt)
  componentize-py  0.25.1             0.25.1     ok         .../.tarantool/bin/componentize-py (installed by tt)
...
prepared for tarantool-wasm 0.1.0 (linux-x86_64)
  rock:       from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
  tarawasm:   .tarantool/bin/tarawasm
  toolchains: python
```

The output of pip and of tarawasm goes in between, where `...` is. What the
command did:

- declared the `wasm` rock, the native module of the release, in the manifest
  and re-resolved the lock:

    ```toml
    [dependencies]
    wasm = { version = "0.1.0", registry = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/" }
    ```

  The server is the [rock index](../concepts/rock-servers.md#a-rock-index) of
  tarantool-contrib/rocks-repo, which serves the rock of every platform with
  its sha256, so the lock pins them all and the next `tt package build`
  installs the rock of this machine, checked against the lock;
- installed tarawasm into a virtual environment under `.tarantool/versions/`
  and linked it as `.tarantool/bin/tarawasm`;
- put the Python toolchain in place, see below;
- wrote `.env/activate.sh`, which puts `.tarantool/bin` on the `PATH` of
  the shell.

`.tarantool` belongs to this machine and is not committed. After a checkout,
on another machine or in CI, `tt wasm prepare` puts it all back: tarawasm,
the toolchain of every component and the SDK each component pins.

## The toolchains

tarawasm drives the compilers of each language. Of those, tt installs into
the project the tools it can take whole from their releases, each checked
against the sha256 tt knows for it, under `.tarantool/versions/` with a link
in `.tarantool/bin`:

| Tool | For | |
| --- | --- | --- |
| wasm-tools 1.261.0, wkg 0.16.1 | every language | the Bytecode Alliance releases |
| componentize-py 0.25.1 | `python` | its wheel, which pip installs into the environment of tarawasm from the file tt downloaded |
| TinyGo 0.43.0 with Binaryen 133 | `go` | Binaryen is laid over TinyGo, as TinyGo 0.43 needs its `wasm-opt`; `wasm-opt` is linked too |
| wit-bindgen 0.62.0 | `c` | |
| the wasi-sdk 34 | `c` | not linked into `.tarantool/bin`, where its `clang` would stand in for the compiler of the system in every build: each C component tells tarawasm where it is, in its `tarawasm.local.json` |

The other tools are yours to install: tt checks their versions, says which
to install, and installs nothing into the system.

| Language | Tools you install |
| --- | --- |
| `python` | python3 3.10 or newer |
| `go` | Go 1.25 to 1.27 |
| `rust` | rustc and cargo 1.96 or newer with the target `wasm32-wasip1`, cargo-component 0.21.1 |
| `js` | Node 24 or newer, jco 1.37.0 with componentize-js 0.23.0 and preview2-shim 0.17.8 |
| `c` | nothing more |
| `csharp` | the .NET SDK 10.0.402, on Linux x86-64 |

A tool that is missing, or in a version that does not fit, stops the
command with what to install:

```console
$ tt wasm prepare --lang rust
...
   ⨯ the toolchain is not ready for rust; install, tt does not:
       rustc >=1.96.0 (missing): rustup (https://rustup.rs), then rustup target add wasm32-wasip1
       cargo >=1.96.0 (missing): rustup (https://rustup.rs)
       cargo-component >=0.21.1 (missing): cargo install --locked cargo-component --version 0.21.1
```

The toolchain is checked as a build finds it: `.tarantool/bin` first, then
`PATH`. `tarawasm toolchain setup --install`, which installs the tools
itself, runs `sudo apt-get install` on Linux and builds `wasm-tools` and
`wkg` from source; tt never runs it.

## Add a component

```console
$ tt wasm new calc --lang python
...
added component calc in calc
```

tarawasm creates the component in `calc/`, on the SDK of the `wasm`
release the package declares, and `calc/.gitignore` keeps what it generates
out of git. Its WIT world is named after it, and so is what it builds,
`dist/calc.wasm`: the components of a package do not collide. The manifest
declares the component:

```toml
# A WebAssembly component, made by tt wasm new and built by tarawasm.
[components.calc]
path = "calc"
include = ["dist/calc.wasm"]

[components.calc.build]
backend = "shell"
command = "tarawasm"
args = ["all"]
incremental = true
```

`tarawasm all` regenerates the bindings and builds `dist/calc.wasm`; a build
finds tarawasm and the compilers in `.tarantool/bin` with or without
`.env/activate.sh`. `incremental = true` skips the build while the sources,
the WIT and the SDK lock of the component are those of the last one; after
a new compiler, `tt package build --rebuild` builds it anyway. See
[Incremental builds](../concepts/packages.md#incremental-builds). A Lua
component of the project at `path = "."` takes
every `*.so` below it by default: give it `exclude = ["calc/"]`.

The SDK of a component is the one its `tarawasm.tarantool.lock.json` pins,
and has to be of the release the package declares: a component builds
against the WIT of its SDK, and the module serves the WIT of its own.
`tt package build` warns of a component on another SDK, and
`tt wasm prepare` refuses it.

## Write it

`--template` starts a component in Python from an example: `call` is the one
below, `http` the server of
[An HTTP API in a WebAssembly component](../write-your-app/http-api-in-wasm.md);
`tt wasm new` then prints the Lua module that uses the component.

```console
$ tt wasm new calc --lang python --template call
```

Without a template, add an export to the world in `calc/wit/app.wit`, where
tarawasm writes it:

```wit
package example:app@0.1.0;

world calc {
  include tarantool:tarantool/guest@0.2.0;
  import docs:adder/ssl-bio@0.1.3;
  export add: func(a: s32, b: s32) -> s32;
}
```

and implement it in `calc/main.py`, below the initialization tarawasm
wrote:

```python
import twr_python_sdk
twr_python_sdk.install(ports=['ssl', 'asyncio_dns'])

import wit_world


class WitWorld(wit_world.WitWorld):
    def add(self, a: int, b: int) -> int:
        return a + b
```

See the [tarawasm guide](https://github.com/mandesero/tarawasm/blob/master/docs/tarantool.md)
for the other languages and for the Tarantool API of a component.

## Build it and call it from Lua

```console
$ tt package build
$ find .rocks -name wasm.so -o -name '*.wasm'
.rocks/lib/tarantool/wasm.so
.rocks/share/tarantool/myapp/dist/calc.wasm
```

A `.wasm` a component lays out has to be a WebAssembly binary: anything
else fails the build, and a core module, which `require('wasm')` does not
load, is laid out with a warning.

The component is a file of the package, under the namespace of the package.
Load it by its path from the application directory, which is the working
directory of the instances, in the project and in an installed version alike:

```lua
local wasm = require('wasm')

local calc = wasm.load('.rocks/share/tarantool/myapp/dist/calc.wasm')
assert(wasm.call(calc, 'add', 20, 22) == 42)

local bound = wasm.bind(calc)
assert(bound.add(1, 2) == 3)

wasm.drop(calc)
```

```console
$ tt run -e "local wasm = require('wasm') \
    local calc = wasm.load('.rocks/share/tarantool/myapp/dist/calc.wasm') \
    print(wasm.call(calc, 'add', 20, 22)) os.exit()"
42
```

`os.exit()` ends the run: without it Tarantool goes on to read a script
from the standard input, or opens its console in a terminal.

The module keeps the compiled components in `~/.cache/wasmtime` of the user
Tarantool runs as, and goes on without that cache when it cannot write
there.

## Check the project

`tt wasm status` shows what `tt wasm prepare` put in place and a build
needs, changing nothing: the `wasm` rock and its build for this machine,
tarawasm, every component with the SDK it pins, and the toolchains. It fails
on anything missing, as a check in CI does:

```console
$ tt wasm status
wasm:       0.1.0 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
rock:       from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
tarawasm:   .tarantool/bin/tarawasm
component:  calc (python, SDK 0.1.0) in calc
python toolchain:
  ...
ready
```

## Pack it

Pack with the dependencies, the default: the archive carries the module for
the platform it is packed on, and the component.

```console
$ tt package pack
.../_build/pack/myapp-1.0.0-linux-amd64.tt
$ tt package install _build/pack/myapp-1.0.0-linux-amd64.tt --scope user --yes
installed myapp 1.0.0 into user scope
```

An archive packed `--without-deps` is `-any.tt` when the package has no
native files of its own: its installation fetches the `wasm` rock of the
machine it is installed on from the rock server, checked against the lock
of the archive.

## Share the rock

The rock index of tarantool-contrib/rocks-repo serves the rock of every
platform. A rock server of your own does as well: `tt wasm rock` packs the
rock of every build of a release, with the index that makes a directory a
rock server:

```console
$ tt wasm rock --out dist/rocks
.../dist/rocks/wasm-0.1.0-1.linux-x86_64.rock
.../dist/rocks/wasm-0.1.0-1.macosx-aarch64.rock
.../dist/rocks/manifest
.../dist/rocks/tt-rocks-index.json
```

Serve the directory, or upload its files next to the assets of the release,
and declare the dependency from there:

```console
$ tt wasm prepare --rock-server https://github.com/tarantool/tarantool-wasm-rs/releases/download/v0.1.0/
...
  rock:       from https://github.com/tarantool/tarantool-wasm-rs/releases/download/v0.1.0/
```

Any version a server serves is declared, whether tt knows the release or
not. A local build of the module is packed for this machine alone, into
`.tarantool/registry`, a rock server inside the project, and declared from
there:

```console
$ tt wasm prepare --from target/release/libtarantool_wasm_rs.so
reading the build in target/release/libtarantool_wasm_rs.so
packed wasm-0.1.0-1.linux-x86_64.rock into .tarantool/registry
declared wasm 0.1.0 from .tarantool/registry
...
```

The registry is on this machine only: run `tt wasm prepare --from` again
after a checkout, and pack the package with its dependencies to install it
elsewhere.

## Commands and options

| Command | |
| --- | --- |
| `tt wasm prepare` | the `wasm` rock, tarawasm, the toolchains, the SDKs of the components |
| `tt wasm new NAME --lang L` | a component |
| `tt wasm status` | what a build lacks, changing nothing; fails when anything is |
| `tt wasm rock --out DIR` | the rocks of every platform of a release, for a rock server |

- `--version` of `prepare` and `rock` selects the tarantool-wasm release:
  any the rock server serves for `prepare`, one tt knows for
  `prepare --from` and `rock`; `tt wasm prepare --help` lists those.
- `--rock-server URL` of `prepare` declares the rock from that rock server,
  the rock index of tarantool-contrib/rocks-repo by default.
- `--from FILE` of `prepare` packs a local build of the module into
  `.tarantool/registry` and declares the rock from there; of `rock`, packs
  it into `--out` as the rock of `--arch`.
- `--python` of `prepare` names the Python to install tarawasm with,
  `--tarawasm` what pip installs instead of the tarawasm tt pins,
  `--no-tarawasm` leaves it out, `--reinstall` installs it anew.
- `--lang` of `prepare` puts the toolchain of `python`, `go`, `js`, `rust`,
  `c` or `csharp` in place too; `new` requires it.
- `--dir` of `new` is the directory of the component, `--world` the name of
  its WIT world (the name of the component by default), and `--template`
  `http` or `call`, for Python.

The downloads are cached in the cache directory of the user,
`~/.cache/tt/wasm` on Linux. `TT_WASM_RELEASES` names a JSON file of
releases tt uses instead of the ones it knows, with the `base_url` of their
assets: a mirror of them, for a network without the internet; its format is
in [Environment variables](../reference/environment.md#webassembly-components).
