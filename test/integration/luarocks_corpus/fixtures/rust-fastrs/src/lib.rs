//! fastrs: a module of Tarantool in Rust on the tarantool crate, the crate
//! of tt's how-to "Build a module in Rust": a C function, fastrs.add, of
//! box.schema.func, and a Lua module of one function, require('fastrs').mul.
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

/// require('fastrs')
#[no_mangle]
pub unsafe extern "C" fn luaopen_fastrs(l: *mut ffi::lua_State) -> c_int {
    ffi::lua_createtable(l, 0, 1);
    ffi::lua_pushcclosure(l, mul, 0);
    ffi::lua_setfield(l, -2, c"mul".as_ptr());
    1
}
