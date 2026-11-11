# Build a module in Rust

A module component built by cargo is a native module of Tarantool: the
library of a Rust package, which the code of the application loads with
`require`, and which holds the C functions of `box.schema.func.create`.
The [`tarantool` crate](https://docs.rs/tarantool/latest/tarantool/) gives
it the API of Tarantool.

## Write the crate

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

The build runs `cargo rustc --lib --crate-type cdylib --profile release`
in the directory of the component, with `_build/cargo/` of the project as
its target directory, and takes the library from what cargo reports. The module is named after the
library, `fastrs`; `module = "..."` names it otherwise. It is
`<module>.so`, and `<module>.dylib` on macOS, where Tarantool looks for
that name, and the library is linked with `-undefined dynamic_lookup`
there: its symbols of Tarantool are resolved when Tarantool loads it, as
those of a C module are. `package` selects the package of a workspace,
`features` its features, and `profile` another profile than `release`.

A `target/` of the component, which cargo run there by hand leaves, is not
laid out: the library and the proc-macros in it are no files of the
package. A `*.lua` file
of the component is laid out as in any module component.

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

## The toolchain

`cargo` and `rustc` are yours to install, from [rustup](https://rustup.rs):
tt installs neither, and looks for `cargo` on `PATH`. The `tarantool`
crate wants rustc 1.67.1 or newer. The module links against the C library
of the machine it is built on: build it on the oldest system it runs on.
