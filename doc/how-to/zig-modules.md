# Build a module in Zig

A component built by zig makes a native module of Tarantool: a library,
which the code of the application loads with `require`, and which holds the
C functions of `box.schema.func.create`. The same component may make a
program too, from the same `build.zig`.
[zig-tarantool](https://github.com/tarantool-contrib/zig-tarantool) gives
the Zig code the API of Tarantool, and the `build.zig` helpers that build a
module and a program.

The `zig` backend is one of the standard module `build` of tt, which the
official tt and every distribution that takes the standard modules have:
`tt version --modules` lists `build` in a tt that has it.

## Get zig

zig is yours to install: tt does not install it, and looks for `zig` on
`PATH`, `.tarantool/bin` of the project first. zig-tarantool and the
examples here need zig 0.17.1:

```console
$ zig version
0.17.1
```

The backend builds with zig 0.16 too, a `build.zig` that does not use
zig-tarantool.

## Write the package

In `libs/hello/` of a package named `myapp`, `build.zig.zon` names the
package, with no dependency yet:

```zig
.{
    .name = .hello,
    .version = "0.1.0",
    .fingerprint = 0x3610a6861c561281,
    .minimum_zig_version = "0.17.1",
    .dependencies = .{},
    .paths = .{ "build.zig", "build.zig.zon", "src" },
}
```

The fingerprint is that of a new package: zig says what to put when it is
missing, `error: missing top-level 'fingerprint' field; suggested value:
0x...`.

`build.zig` builds the module `hello` and the program `hello-client`:

```zig
const std = @import("std");
const tarantool = @import("tarantool");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    // zig-out/lib/hello.so, hello.dylib on macOS.
    const hello = tarantool.addModule(b, .{
        .name = "hello",
        .root_source_file = b.path("src/hello.zig"),
        .target = target,
        .optimize = optimize,
    });
    tarantool.installModule(b, hello);

    // zig-out/bin/hello-client.
    const client = tarantool.addExecutable(b, .{
        .name = "hello-client",
        .root_source_file = b.path("src/client.zig"),
        .target = target,
        .optimize = optimize,
    });
    b.installArtifact(client);
}
```

`addModule` makes a library whose symbols of Tarantool are resolved when
Tarantool loads it, and `installModule` installs it as `lib/hello.so`, the
name Tarantool loads, `lib/hello.dylib` on macOS.

`src/hello.zig`, with a C function, `add`, and a Lua module of one
function, `mul`:

```zig
const t = @import("tarantool");

/// The C function myapp.hello.add.
fn add(a: i64, b: i64) i64 {
    return a + b;
}

/// mul of the Lua module, require('myapp.hello').
fn mul(a: i64, b: i64) i64 {
    return a * b;
}

comptime {
    t.proc.exportProc("add", add);
    t.lua.exportModule("myapp_hello", .{ .mul = mul });
}
```

and `src/client.zig`, a program that calls `add` over the network:

```zig
const std = @import("std");
const client = @import("tarantool-client");

pub fn main(init: std.process.Init) !void {
    const address = init.environ_map.get("TT_URI") orelse "127.0.0.1:3301";
    var conn = try client.Connection.connect(init.gpa, init.io, .{
        .address = address,
        .user = "guest",
        .password = "",
    });
    defer conn.close();

    const sum = try conn.call("myapp.hello.add", .{ 20, 22 }, i64);
    std.debug.print("20 + 22 = {d}\n", .{sum});
}
```

Then, in `libs/hello/`, `zig fetch --save` adds zig-tarantool to
`build.zig.zon` as the dependency `tarantool`, pinned to a commit by its
`.url` and its `.hash`:

```console
$ cd libs/hello
$ zig fetch --save=tarantool git+https://github.com/tarantool-contrib/zig-tarantool#c43e6c84c371b59e89cdc0354b8d377ffdd93dc8
$ grep -A3 '\.tarantool' build.zig.zon
        .tarantool = .{
            .url = "git+https://github.com/tarantool-contrib/zig-tarantool#c43e6c84c371b59e89cdc0354b8d377ffdd93dc8",
            .hash = "tarantool-0.0.0-LczyxTwvEQC19W4vsXUbauN_kO8EABpFyamoek3lPuqV",
        },
$ cd ../..
```

`zig fetch --save` looks for the `build.zig` of the package, so it comes
after it, and zig 0.17 puts what it fetched into `zig-pkg/` beside
`build.zig.zon`. That
directory is no file of the package: tt leaves it out of the layout, and
its build fetches into a directory of its own, out of the sources (see
below). Put `zig-pkg/` and `zig-out/` into `.gitignore`.

## Declare the component

The block names the module and the program the build makes:

```toml
[components.hello]
path = "libs/hello"

[components.hello.build]
backend = "zig"
module = "hello"
bin = "hello-client"
```

```console
$ tt package build
$ find .rocks/bin .rocks/lib -name 'hello*'
.rocks/bin/hello-client
.rocks/lib/tarantool/myapp/hello.so
$ find .rocks -path '*zig-pkg*' | wc -l
0
```

The build runs, in the directory of the component,

```console
zig build -p _build/component/hello/zig-out --cache-dir _build/component/hello/zig-cache --pkg-dir _build/component/hello/zig-pkg --release=safe
```

which installs into a prefix of the build's own, out of the sources, and
keeps the cache of the project and the packages it fetches there too;
`tt -V package build` shows the line, with absolute paths. `--pkg-dir` is
given to zig 0.17 and later, and not when `ZIG_LOCAL_PKG_DIR` is set,
the directory of the packages zig takes then, whether it is exported where
tt runs or set by the `env` of the block: `env =
{ ZIG_LOCAL_PKG_DIR = "/home/me/.cache/zig-pkg" }` shares one between
projects. zig 0.16 keeps the packages in its global cache and is given
none. tt takes the module from `lib/` of the prefix, `hello.so` or
`hello.dylib` as `installModule` installs it, or `libhello.so` or
`libhello.dylib` as `b.installArtifact` installs a library, and copies it
as `<module>.so`, `<module>.dylib` on macOS, where Tarantool looks for that
name. The program comes from `bin/` of the prefix, into `.rocks/bin/`,
where an instance description `{binary: hello-client}` finds it; see
[Run an external process](external-processes.md). `module` alone builds the
module only, `bin` alone the program only, and a block with neither is an
error. A dotted module, `module = "codec.fast"`, is the file
`lib/codec/fast.so` of the prefix, as `installModule` installs a module of
that name, and `codec/fast.so` in the namespace of the component.

A module or a program the build did not install fails it, saying what it
installed:

```console
$ tt package build
   ⨯ building component "hello": zig build installed no module "hullo": .../_build/component/hello/zig-out/lib has none of hullo.so, hullo.dylib, libhullo.so, libhullo.dylib (it installed hello.so); install it with installModule of zig-tarantool, or name the step that installs it in steps
```

A `zig-out/` of the component, which `zig build` run there by hand leaves,
is not laid out, nor is its `zig-pkg/`, which `zig fetch --save` and
`zig build` run there leave, and neither are the `.zig` sources: a
`*.lua` file of the component is laid out as in any component, but one
of `zig-pkg/`, as `examples/kv/init.lua` of zig-tarantool, is not. A
module renamed in the block, `module = "greet"` for `hello`, leaves no
`hello.so` behind: a build of a block that changed starts the component
afresh.

The other fields of the block:

- `steps` are the steps of `zig build` run, its default step, `install`,
  when there are none: a `build.zig` that installs the module from another
  step names it, `steps = ["modules"]`.
- `optimize` is the mode the build is made in, `ReleaseSafe` by default:
  `Debug`, `ReleaseSafe`, `ReleaseFast` or `ReleaseSmall`. tt passes it as
  `--release=safe`, `fast` or `small`, and `Debug` as no flag, which a
  `build.zig` takes whether or not it declares `-Doptimize`; one whose
  `standardOptimizeOption` gets it builds in that mode.
- `options` are the options the `build.zig` declares with `b.option`,
  given as `-D<name>=<value>`, each a string: `options = { tls = "true" }`
  for `b.option(bool, "tls", ...)`. `optimize` is not one of them, as the
  field says it.
- `incremental = true` skips the build when its block, the files of the
  component, the version of zig and `build.zig` and `build.zig.zon` of
  `cwd` are those of the last one. A change of a path dependency outside
  the component is not seen: `--rebuild` builds anyway.
- `static = true` checks that the program needs no shared library, and the
  module none but those of the C library. A block that builds a program
  alone builds it for `<arch>-linux-musl`, `-Dtarget`, when the `build.zig`
  takes `-Dtarget` and `options` names no `target`: a program that links
  the C library links musl in. A block of a module and a program builds
  both for the target of the module. A program of `addExecutable` links no
  C library and is static as it is.

`tt package build --target linux/arm64` builds for another platform than
the machine (see
[Other platforms](../reference/app-manifest.md#other-platforms)): zig has
what it links for every target in itself, and is given
`-Dtarget=aarch64-linux-gnu`, `aarch64-linux-musl` for a static program
alone, or `aarch64-macos` for `darwin/arm64`. The `build.zig` is to take
`-Dtarget`, as `b.standardTargetOptions` declares it: one that does not,
and a block whose `options` name a `target`, fail the build, since zig
would build for the machine or the target of the options.

## Call it

`app.lua` in the project:

```lua
local hello = require('myapp.hello')
assert(hello.mul(6, 7) == 42)

box.cfg({listen = '127.0.0.1:3301'})
box.schema.func.create('myapp.hello.add', {language = 'C', if_not_exists = true})
box.schema.user.grant('guest', 'execute', 'function', 'myapp.hello.add',
    {if_not_exists = true})
assert(box.func['myapp.hello.add']:call({20, 22}) == 42)
```

Run it in one terminal, where Tarantool goes on serving after the script,
and the program in another:

```console
$ tt run app.lua
```

```console
$ .rocks/bin/hello-client
20 + 22 = 42
```

- `require('<namespace>.<module>')` calls the function
  `luaopen_<namespace>_<module>` of the library, dots made `_`: the module
  `exportModule` exports as `myapp_hello`. With `namespace = ""` the module
  is `require('hello')`, exported as `hello`.
- A C function is named `<module>.<function>`: Tarantool takes the module
  from the name up to its last dot, `myapp.hello`, and the function from
  the rest, `add`, which `exportProc` exports.

The archive of `tt package pack` carries the module and the program, built
for the machine it is packed on, and the instances of an installed version
load and run them as in the project.

## Build offline

zig fetches the packages of `build.zig.zon`, of a `.url` each pinned by
its `.hash`, on the first build of a machine. With zig 0.17 and later,
`tt package download` with no arguments fetches them ahead, with the rocks
of the lock, into the mirror it makes:

```console
$ tt package download --dir ../mirror
indexed 0 rock(s) in .../mirror
.../mirror/zig
downloaded what 1 component(s) build with into .../mirror/zig
```

It runs `zig build --fetch=all` for each zig component, into a directory of
its own: every package the `build.zig.zon` pins, of its dependencies too
and the lazy ones among them, checked against its `.hash`, goes into
`zig/<hash>/` of the mirror. The components of the project share it, and
so do other projects downloaded into the same mirror; running it again
after a change of `build.zig.zon` adds the new packages and keeps the
others. A dependency of a `.url` without a `.hash` fails the download, as
it fails `zig build`: `zig fetch --save` writes both. A `.path` dependency
is not fetched, but its own dependencies are. zig 0.16, which keeps the
packages in its global cache, fails the download of a `build.zig.zon`.

A build whose rock servers have the mirror builds from it, with no network:

```console
$ TT_REGISTRIES=$PWD/../mirror tt package build
```

It checks first, with `zig build --fetch=all --system`, which fetches
nothing, that the mirror has every package, then builds with `zig/` of the
mirror for `--pkg-dir`, over a `ZIG_LOCAL_PKG_DIR` too: zig finds each
package in the directory of its hash, as in a `zig-pkg/` it fetched them
into, and fetches none, and nothing is written into the mirror. The build
itself is given no `--system`, which would enable every system
integration of the `build.zig` too: it is made as it is with the network.
A package the mirror lacks, of a `build.zig.zon` changed since the
download, fails the build before zig builds anything, naming the mirror
and `tt package download`. The first rock server that is a directory with
`zig/` is the one taken. The zig components of a path dependency are
downloaded with those of the project.

## The toolchain

zig fetches the dependencies of `build.zig.zon` itself, as it builds, into
its global cache, and zig 0.17 into `_build/component/<component>/zig-pkg/`
from there: `tt package fetch` does not run the build, and
`tt package build --locked` keeps the lock of tt, not zig off the network;
a mirror of `tt package download` does (see [Build offline](#build-offline)).
The hashes of `build.zig.zon` pin what zig fetches. The module links
against the C library of the machine it is built on: build it on the
oldest system it runs on.
