# Build a module in Rust

A component built by cargo makes a native module of Tarantool: the library
of a Rust package, which the code of the application loads with
`require`, and which holds the C functions of `box.schema.func.create`. The
same component may make a program of the package too.
The [`tarantool` crate](https://docs.rs/tarantool/latest/tarantool/) gives
it the API of Tarantool.

The `cargo` backend is one of the standard module `build` of tt, which the
official tt and every distribution that takes the standard modules have:
`tt version --modules` lists `build` in a tt that has it.

## Write the crate

<!-- TestBuildsTheModuleOfTheHowTo of modules/build/cargo builds the first
toml block with name = "fastrs", the rust block of luaopen_myapp_fastrs and
runs the lua block of box.schema.func.create in Tarantool: keep them whole. -->

In `libs/fastrs/` of a package named `myapp`, `Cargo.toml`:

```toml
[package]
name = "fastrs"
version = "0.1.0"
edition = "2021"

[dependencies]
tarantool = "11"
```

and `src/lib.rs`, with a C function, `add`, and a Lua module of one
function, `mul`:

```rust
use std::os::raw::c_int;

use tarantool::proc;
use tarantool::tlua::ffi;

#[proc]
fn add(a: i64, b: i64) -> i64 {
    a + b
}

unsafe extern "C-unwind" fn mul(l: *mut ffi::lua_State) -> c_int {
    let product = ffi::lua_tointeger(l, 1) * ffi::lua_tointeger(l, 2);
    ffi::lua_pushinteger(l, product);
    1
}

/// require('myapp.fastrs')
#[no_mangle]
pub unsafe extern "C" fn luaopen_myapp_fastrs(l: *mut ffi::lua_State) -> c_int {
    ffi::lua_createtable(l, 0, 1);
    ffi::lua_pushcclosure(l, mul, 0);
    ffi::lua_setfield(l, -2, c"mul".as_ptr());
    1
}
```

The crate need not set `crate-type`: tt builds a `cdylib` of it anyway.

tt builds the crate from the versions its `Cargo.lock` pins, as it builds
the project from the versions of its lock, so the crate needs one. Write it
once, and keep it with the sources:

```console
$ cd libs/fastrs
$ cargo generate-lockfile
$ git add Cargo.lock
```

A crate that is a member of a workspace has the `Cargo.lock` of the
workspace, beside the `Cargo.toml` of its root. A build without one fails
before anything is built: `the cargo package has no Cargo.lock in
.../libs/fastrs: the build is made with --locked, from the versions
Cargo.lock pins; run cargo generate-lockfile there, and keep Cargo.lock with
the sources`. One that the `Cargo.toml` no longer matches, a dependency
added since, fails too, as `cargo build --locked` fails on it: `cargo
update` or `cargo generate-lockfile` writes it again.

## Declare the component

```toml
[components.fastrs]
path = "libs/fastrs"

[components.fastrs.build]
backend = "cargo"
```

```console
$ tt package build
$ find .rocks -name 'fastrs*'
.rocks/lib/tarantool/myapp/fastrs.so
```

The build runs `cargo rustc --lib --crate-type cdylib --profile release
--locked` in the directory of the component, with `_build/cargo/` of the project as
its target directory, and takes the library from what cargo reports. The module is named after the
library, `fastrs`; `module = "..."` names it otherwise. It is
`<module>.so`, and `<module>.dylib` on macOS, where Tarantool looks for
that name, and the library is linked with `-undefined dynamic_lookup`
there: its symbols of Tarantool are resolved when Tarantool loads it, as
those of a C module are. `package` selects the package of a workspace,
`features` its features, and `profile` another profile than `release`.

A `target/` of the component, which cargo run there by hand leaves, is not
laid out: the library and the proc-macros in it are no files of the
package. A `*.lua` file of the component is laid out as in any component.

## A module and a program of one crate

A package may have a binary as well as the library: a CLI of the module,
or a process the application runs beside Tarantool. One component builds
both when its block names both. With `[[bin]]` in `Cargo.toml`:

```toml
[[bin]]
name = "fastrs-cli"
path = "src/main.rs"
```

and `src/main.rs`:

```rust
fn main() {
    println!("6 * 7 = {}", 6 * 7);
}
```

the block names the module and the binary:

```toml
[components.fastrs.build]
backend = "cargo"
module = "fastrs"
bin = "fastrs-cli"
```

```console
$ tt package build
$ find .rocks/bin .rocks/lib -name 'fastrs*'
.rocks/bin/fastrs-cli
.rocks/lib/tarantool/myapp/fastrs.so
$ .rocks/bin/fastrs-cli
6 * 7 = 42
```

The build runs `cargo rustc --lib` of the module first, then
`cargo build --bin fastrs-cli`, and copies the binary into `.rocks/bin/`,
where an instance description `{binary: fastrs-cli}` finds it; see
[Run an external process](external-processes.md). `module` alone builds the
module only, `bin` alone the program only, and a block with neither builds
the library under its own name, as above. With `static = true` the binary
is built for musl and needs no shared library, and the module is built as
a module is.

## Call it

```lua
local fastrs = require('myapp.fastrs')
assert(fastrs.mul(6, 7) == 42)

box.schema.func.create('myapp.fastrs.add', {language = 'C', if_not_exists = true})
assert(box.func['myapp.fastrs.add']:call({20, 22}) == 42)
```

- `require('<namespace>.<module>')` calls the function
  `luaopen_<namespace>_<module>` of the library, dots made `_`, and the
  name up to its first `-` dropped: `require('my-app.fastrs')` of a package
  named `my-app` calls `luaopen_app_fastrs`. With `namespace = ""` the
  module is `require('fastrs')`, and the function `luaopen_fastrs`.
- A C function is named `<module>.<function>`: Tarantool takes the module
  from the name up to its last dot, `myapp.fastrs`, and the function from
  the rest, `add`, which `#[proc]` exports.

The archive of `tt package pack` carries the module, built for the machine
it is packed on, and the instances of an installed version load it as in
the project.

## Build offline

cargo downloads the crates of `Cargo.lock` from crates.io, and those of a
`git` dependency from their repository, on the first build of a machine.
`tt package download` with no arguments downloads them ahead, with the
rocks of the lock, into the mirror it makes:

```console
$ tt package download --dir ../mirror
indexed 0 rock(s) in .../mirror
.../mirror/cargo
downloaded what 1 component(s) build with into .../mirror/cargo
```

It runs `cargo vendor --locked --versioned-dirs --no-delete` for each
cargo component: every crate the `Cargo.lock` pins, of crates.io and of
git, goes into `cargo/<name>-<version>/` of the mirror. The components of
the project share it, and so do other projects downloaded into the same
mirror; running it again after a change of `Cargo.lock` adds the new
crates and keeps the others. A `[source]` of the cargo configuration, a
mirror of crates.io say, is what the crates are downloaded from, as cargo
builds with it.

A build whose rock servers have the mirror builds from it, with no network:

```console
$ TT_REGISTRIES=$PWD/../mirror tt package build
```

cargo runs with `--offline`, and with crates.io and every git repository
of `Cargo.lock` replaced by `cargo/` of the mirror through `--config`:
nothing is written into the sources or into the cargo configuration of the
project. The first rock server that is a directory with `cargo/` is the one
taken. A crate that is not in the mirror, of a lock changed since the
download, fails the build, which names the mirror and `tt package
download`. The crates of a registry other than crates.io are not taken
from the mirror, and an offline build of them fails. The cargo components
of a path dependency are downloaded with those of the project.

The source rock of `tt package pack --format rock-src` carries the files
of the project git tracks, `Cargo.lock` among them, and not the crates: a
project that depends on the rock builds it from the crates the lock pins,
which cargo downloads, a mirror of the project or not: its download does
not download them.

## Rust targets and other platforms

A build for the machine tt runs on names no `--target`, so that the
configuration of the project for its host builds applies, but a static
program: `static = true` builds it for `<arch>-unknown-linux-musl`, whose
standard library is rustup's to install. The build checks that every
target it names is installed before anything is built, with `rustc --print
target-libdir --target <triple>` of the toolchain cargo runs, the one
`RUSTC` names or `rust-toolchain.toml` selects:

```console
$ tt package build
   ⨯ building component "fastrs": the Rust target is not installed: x86_64-unknown-linux-musl, no .../lib/rustlib/x86_64-unknown-linux-musl/lib; rustup target add x86_64-unknown-linux-musl installs it
$ rustup target add x86_64-unknown-linux-musl
```

`tt package build` builds for the machine it runs on. The backend builds
for another platform too, when the build it runs in is for one
(`build.Context.Target` of the SDK). A build for another Linux platform,
`linux/arm64` on an `x86_64` machine say, needs the Rust targets of that
platform, `aarch64-unknown-linux-gnu` for the module and
`aarch64-unknown-linux-musl` for a static program, and zig on `PATH`: the
C toolchain of the machine links for the machine alone, so cargo links
with `zig cc -target aarch64-linux-gnu`, as
[cargo-zigbuild](https://github.com/rust-cross/cargo-zigbuild) does. tt
writes the script that runs it into `_build/cargo-zig/` and names it as the
linker of the target with `--config target.<triple>.linker=...`; the build
scripts that compile C with the `cc` crate get it as `CC_<triple>`, and
`zig ar` as `AR_<triple>`. A linker the environment names,
`CARGO_TARGET_<TRIPLE>_LINKER`, and a `CC_<triple>`, `TARGET_CC`,
`AR_<triple>` or `TARGET_AR` of it are kept. The module and the program
link against the C library zig has for the target, glibc 2.31 by default
with zig 0.17, not that of the machine. A build for macOS is made on
macOS, whose SDK cargo links against: one on Linux fails before anything
is built.

The default features of the `tarantool` crate build `openssl-sys`, which
needs the development files of OpenSSL of the target: for another platform
there are none, and the build fails. `tarantool = { version = "11",
default-features = false }` leaves out its network client, and OpenSSL with
it.

## The toolchain

`cargo` and `rustc` are yours to install, from [rustup](https://rustup.rs):
tt installs neither, and looks for `cargo` on `PATH`. The `tarantool`
crate 11 wants rustc 1.89 or newer, its `rust-version`. Its default
features build `openssl-sys`, which needs the development files of OpenSSL,
`libssl-dev` on Debian and Ubuntu, `openssl-devel` on Fedora, `openssl@3`
of Homebrew on macOS; `default-features = false` leaves them out, with the
network client of the crate. The module links against the C library of the
machine it is built on: build it on the oldest system it runs on.
