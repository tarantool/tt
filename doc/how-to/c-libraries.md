# Link C libraries into a module

A `c` or `lua-c` component compiles C and C++ into a native module of
Tarantool, and links it with the libraries it needs. A library of the
system the module finds on every machine it is installed on, as it finds
the C library; any other one goes with the module, linked into it or copied
beside it. This page builds two packages that do both: one with zlib, which
pkg-config describes, and one with a C library the package builds itself.

The fields are those of the
[`c` and `lua-c` backends](../reference/app-manifest.md#componentsnamebuild):

| Field | What it does |
| --- | --- |
| `libraries`, `library_dirs` | the libraries the module links, `-l<name>` or a file, and where the linker looks for them |
| `pkg_config` | packages whose flags of the compiler and of the linker pkg-config gives |
| `link` | the components of the package whose libraries the module links |
| `bundle` | the libraries whose shared libraries go beside the module |
| `rpath` | where the loader looks for the libraries of the module, `$ORIGIN` its directory |
| `static` | `true`: the module needs nothing of the machine, each library linked from its `.a` |

The commands below ran on Ubuntu with gcc 15, zlib 1.3.1, CMake 4.2 and
Tarantool 3.8; the paths of the project are shortened to `...`.

## A library pkg-config knows

`zcrc/app.manifest.toml`, a package of one C module, `zcrc.crc`:

```toml
manifest_version = "0.1"

[package]
name = "zcrc"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

# The C module zcrc.crc: the CRC-32 of zlib.
[components.zcrc]
path = "zcrc"

[components.zcrc.build]
backend = "lua-c"
module = "crc"
sources = ["crc.c"]
pkg_config = ["zlib >= 1.2"]
```

`zcrc/zcrc/crc.c`:

```c
#include <lua.h>
#include <lauxlib.h>
#include <zlib.h>

/* crc(s): the CRC-32 of the string s, as zlib computes it. */
static int
crc(lua_State *L)
{
	size_t len;
	const char *s = luaL_checklstring(L, 1, &len);
	lua_pushinteger(L, crc32(0, (const Bytef *)s, len));
	return 1;
}

/* version(): the version of the zlib the module runs with. */
static int
version(lua_State *L)
{
	lua_pushstring(L, zlibVersion());
	return 1;
}

int
luaopen_zcrc_crc(lua_State *L)
{
	lua_newtable(L);
	lua_pushcfunction(L, crc);
	lua_setfield(L, -2, "crc");
	lua_pushcfunction(L, version);
	lua_setfield(L, -2, "version");
	return 1;
}
```

`pkg_config` asks pkg-config for the flags of zlib, `--cflags` for every
compile and `--libs` for the link, so the block names neither the headers
nor the library:

```console
$ cd zcrc
$ tt package build
$ tt run -e "local z = require('zcrc.crc') print(z.crc('hello'), z.version()) os.exit()"
907060870	1.3.1
$ readelf -d .rocks/lib/tarantool/zcrc/crc.so | grep -E 'NEEDED|RUNPATH'
 0x0000000000000001 (NEEDED)             Shared library: [libz.so.1]
 0x0000000000000001 (NEEDED)             Shared library: [libc.so.6]
```

The module needs `libz.so.1` of the machine it runs on. A package other
machines install, whose module is to load wherever Tarantool runs, says
`static = true`, and its build links each library from its static
library. On Debian and Ubuntu `libz.a` is not built with `-fPIC`, which a
module, a shared object, needs, and the link says so:

```console
$ echo 'static = true' >> app.manifest.toml
$ tt package build
   ⨯ building component "zcrc": linking: exit status 1
     cc -shared -Wl,-Bsymbolic-functions -o .../zcrc/_build/rock/default/lib/zcrc/crc.so .../zcrc/_build/component/zcrc/obj/crc.c.de186406.o /usr/lib/x86_64-linux-gnu/libz.a
     /usr/bin/x86_64-linux-gnu-ld.bfd: /usr/lib/x86_64-linux-gnu/libz.a(zutil.o): warning: relocation against `z_errmsg' in read-only section `.text'
     /usr/bin/x86_64-linux-gnu-ld.bfd: /usr/lib/x86_64-linux-gnu/libz.a(zutil.o): relocation R_X86_64_PC32 against symbol `z_errmsg' can not be used when making a shared object; recompile with -fPIC
     /usr/bin/x86_64-linux-gnu-ld.bfd: final link failed: bad value
     collect2: error: ld returned 1 exit status
     hint: the static library /usr/lib/x86_64-linux-gnu/libz.a was not built with -fPIC: a module is a shared object, which takes position-independent code only: build the library with -fPIC (CFLAGS=-fPIC, or -DCMAKE_POSITION_INDEPENDENT_CODE=ON of cmake), or link its shared library
```

The build took `/usr/lib/x86_64-linux-gnu/libz.a` for `-lz`: pkg-config
was asked with `--static`, and the compiler named the static library,
`cc -print-file-name=libz.a`. Where `libz.a` is built with `-fPIC` - a
zlib the package builds itself, below - the module links it in and needs
`libc.so.6` alone. Here the shared library goes with the module instead.

## Ship a shared library with the module

`bundle` copies the shared library of a library the module links beside
it, under the name the module asks the loader for, and has the module
look for it there, by an rpath of its own directory, `$ORIGIN`:

```console
$ echo 'bundle = ["z"]' >> app.manifest.toml
$ tt package build
$ ls .rocks/lib/tarantool/zcrc
crc.so
libz.so.1
$ readelf -d .rocks/lib/tarantool/zcrc/crc.so | grep -E 'NEEDED|RUNPATH'
 0x0000000000000001 (NEEDED)             Shared library: [libz.so.1]
 0x0000000000000001 (NEEDED)             Shared library: [libc.so.6]
 0x000000000000001d (RUNPATH)            Library runpath: [$ORIGIN]
```

`libz.so.1` is `/usr/lib/x86_64-linux-gnu/libz.so.1.3.1`, which
`libz.so` of the system links to, copied under its SONAME. The build with
`static = true` passes: the module needs `libz.so.1`, which it finds
beside itself, and `libz.so.1` needs `libc.so.6` alone. A library the
bundled one needs is not bundled with it: tt names it, a warning without
`static = true`, an error with it, and `bundle` lists it too.

The copy is a module of the package, so it goes wherever the module goes.
Packed and installed into another directory, the module loads, with no
`LD_LIBRARY_PATH`:

```console
$ echo 1.0.0 > VERSION
$ tt package pack --without-deps
.../zcrc/_build/pack/zcrc-1.0.0-linux-amd64.tt
$ mkdir ../app && cd ../app
$ tt package install ../zcrc/_build/pack/zcrc-1.0.0-linux-amd64.tt
installed zcrc 1.0.0 into project scope
$ find .rocks/lib -type f | sort
.rocks/lib/tarantool/zcrc/crc.so
.rocks/lib/tarantool/zcrc/libz.so.1
$ tarantool -e "print(require('zcrc.crc').crc('hello')) os.exit()"
907060870
```

`bundle` is of `lua-c` only. Tarantool loads a module of `c`, of
`box.schema.func`, from a copy in a temporary directory, where nothing is
beside it; such a module links its libraries in statically.

On macOS the copy gets the install name `@rpath/<name>`, with
`install_name_tool`, and is signed again ad hoc, with `codesign`, before
the module is linked against it with `-Wl,-rpath,@loader_path`.

## A library the package builds

A C library the package carries in its sources is built by a component of
its own, with the tool of its project, and the module links what that
component made. `hashes/app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "hashes"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[products.default]
components = ["fnv", "hashes"]
default = true

# libfnv.a and fnv.h, for the module below to link; not in the package.
[components.fnv]
path = "fnv"

[components.fnv.build]
backend = "cmake"
output = [
    { path = "libfnv.a", to = "link" },
    { path = "fnv.h", to = "link" },
]

# The C module hashes.fnv, linked with libfnv.a.
[components.hashes]
path = "lua"

[components.hashes.build]
backend = "lua-c"
module = "fnv"
sources = ["fnv_lua.c"]
link = ["fnv"]
```

`hashes/fnv/CMakeLists.txt`, a static library of position-independent
code, and its header in the build tree:

```cmake
cmake_minimum_required(VERSION 3.15)
project(fnv C)
add_library(fnv STATIC fnv.c)
set_target_properties(fnv PROPERTIES POSITION_INDEPENDENT_CODE ON)
configure_file(fnv.h fnv.h COPYONLY)
```

`hashes/fnv/fnv.h`:

```c
#include <stddef.h>
#include <stdint.h>

uint32_t fnv1a(const char *s, size_t len);
```

`hashes/fnv/fnv.c`:

```c
#include "fnv.h"

uint32_t
fnv1a(const char *s, size_t len)
{
	uint32_t h = 2166136261u;
	for (size_t i = 0; i < len; i++)
		h = (h ^ (unsigned char)s[i]) * 16777619u;
	return h;
}
```

`hashes/lua/fnv_lua.c`, which includes the header of the library as a
header of the system:

```c
#include <lua.h>
#include <lauxlib.h>
#include <fnv.h>

static int
hash(lua_State *L)
{
	size_t len;
	const char *s = luaL_checklstring(L, 1, &len);
	lua_pushinteger(L, fnv1a(s, len));
	return 1;
}

int
luaopen_hashes_fnv(lua_State *L)
{
	lua_pushcfunction(L, hash);
	return 1;
}
```

`to = "link"` sends a file of `output` to `_build/link/<product>/<component>/`,
a header to `include/` of it: a library for the components built after
it, which is neither installed nor packed. `link = ["fnv"]` gives the
compiles of the module `-I` of that `include/` and its link `-L` of the
directory and `-lfnv`:

```console
$ cd ../hashes
$ tt package build
$ find _build/link -type f | sort
_build/link/default/fnv/include/fnv.h
_build/link/default/fnv/libfnv.a
$ find .rocks/lib -type f | sort
.rocks/lib/tarantool/hashes/fnv.so
$ tt run -e "print(require('hashes.fnv')('hello')) os.exit()"
1335831723
```

The product builds `fnv` before `hashes`, in the order of its
`components`; a product that builds it after is an error of the manifest:

```console
$ sed -i 's/components = \["fnv", "hashes"\]/components = ["hashes", "fnv"]/' app.manifest.toml
$ tt package build
   ⨯ invalid manifest: products.default.components: component "hashes" links the libraries of component "fnv", and the product builds it after "hashes": list "fnv" before "hashes"
```

A manifest without `[products]` builds its components in the order of
their names. A build of one component takes what the components of its
`link` made in an earlier build, and fails when there is none yet:

```console
$ sed -i 's/components = \["hashes", "fnv"\]/components = ["fnv", "hashes"]/' app.manifest.toml
$ rm -rf _build .rocks
$ tt package build hashes
   ⨯ building component "hashes": link[0]: component "fnv" has not been built: build it first, as a build of the whole product does before "hashes"
$ tt package build fnv
$ tt package build hashes
```

The same works with `make` and `shell`, whose `output` is relative to the
working directory of the build. A component of `link` that makes a shared
library, `libfnv.so` with `to = "link"`, is linked with it, and
`bundle = ["fnv"]` ships it beside the module, as `libz.so.1` above.

## Where the loader looks

`library_dirs` is where the linker looks for `libraries`. It is not where
the loader looks when Tarantool loads the module: a directory of the build
machine is not on the machine the package is installed on. `rpath` says
where the loader looks, each entry `-Wl,-rpath,<entry>`: `$ORIGIN` is the
directory of the module (`@loader_path` on macOS), so
`rpath = ["$ORIGIN/../vendor"]` finds a library installed beside the
directory of the module, and an absolute entry is a directory of the
machine the module is installed on, which it is to have.
