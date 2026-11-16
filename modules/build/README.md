# The build module

The build backends of tt: `make` and `shell`, which the hooks of a package
run with, `c` and `lua-c`, `cargo`, `cmake`, `go` and `zig`. A component whose
`[components.<name>.build]` block names one of them is built by it. It is a
tt module: `build.New` is its constructor, which gives tt the backends
through `sdk.Module.Backends`, and it is one of the standard modules of the
core, so the official tt and every distribution that takes the standard
modules have it. The core itself has no backend: a distribution that
excludes this module builds no component, and a hook there fails with
`no module of this tt provides a backend that runs hooks`.

This directory is a Go module of its own,
`github.com/tarantool/tt/modules/build`. Its `go.mod` requires the tt SDK
and never tt's root module, so the compiler refuses any import of tt's
internals. Each backend is written against the contract of a build backend
alone, `github.com/tarantool/tt/sdk/build`, as the backend of any module is,
and its tests run it through `sdk/build/buildtest`.

- `build.go` - the constructor, and the order tt lists the backends in.
- `basic` - make and shell.
- `cc` - c and lua-c: a compile a source, C or C++ by its extension, into
  an object of the work directory, the compiles at once, then one link,
  with the toolchain it derives from the Context as go-luarocks does for a
  builtin rock: `CC` and `CXX` of tt's environment, the link flags of the
  target, the headers under the prefix of the Tarantool of the build. The
  contract has no toolchain of C: it is this backend's. It compiles with
  `-O2 -g` unless `CFLAGS`/`CXXFLAGS` is set, with the flags that keep the
  paths of the project out of the objects each compiler takes, probed once
  (`probe.go`), and links a module of Linux with
  `-Wl,-Bsymbolic-functions`. It keeps a cache of the objects, with the
  warnings of their compiles, which it shows again, and of the link in the
  work directory (`cache.go`), writes `compile_commands.json` there, and
  checks a module once it is linked: the entry point of a lua-c module
  (`entry.go`), the symbols it exports that the tarantool of the build
  exports too (`exports.go`), and that the module loads into that
  tarantool (`load.go`). The libraries of the link are its own too: the flags pkg-config gives (`pkgconfig.go`), the static
  library of each under `static = true` and the hint of one not built with
  `-fPIC` (`libs.go`), the libraries other components of the package made
  for linking, `Dirs.LinkOf` of the contract (`link.go`), and the shared
  libraries it copies beside the module, which the module finds by
  `$ORIGIN` (`bundle.go`).
- `cargo` - Rust crates: a module, a program or both.
- `cmake` - CMake projects, built out of the source.
- `gobuild` - Go programs.
- `zig` - zig build of a `build.zig`: a module, a program or both.

The fields of each backend are described in
[the reference of the application manifest](../../doc/reference/app-manifest.md);
how to write a backend of your own, in
[Write a build backend](../../doc/how-to/write-a-build-backend.md).

The tests that compile against Tarantool are behind the `integration` build
tag. They need a `tarantool` in `PATH` with its headers under its prefix,
`<prefix>/include/tarantool`, a C compiler, cmake, cargo and zig 0.17.1,
and skip what they cannot run, saying why with `-v`:

```sh
go test -tags integration ./...
```

`TT_CLI_TARANTOOL_PREFIX` names the prefix of the headers instead, as it
does for tt, for a Tarantool whose headers are elsewhere: the c and lua-c
tests then compile against `<prefix>/include/tarantool` and run the
`tarantool` in `PATH`, else `<prefix>/bin/tarantool`:

```sh
TT_CLI_TARANTOOL_PREFIX=/opt/tarantool go test -tags integration -v ./cc/
```

The zig test of a project of
[zig-tarantool](https://github.com/tarantool-contrib/zig-tarantool) builds
it against a checkout of zig-tarantool, which `TT_TEST_ZIG_TARANTOOL`
names: one with the helpers of its `build.zig`, `addModule`,
`installModule` and `addExecutable`, such as commit `c43e6c8`, which pins
zig 0.17.1. Without one, the test is skipped:

```sh
TT_TEST_ZIG_TARANTOOL=/path/to/zig-tarantool go test -tags integration ./zig/
```
