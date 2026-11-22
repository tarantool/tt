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

`Context.Jobs`, `-j` of `tt package build`, is how many jobs a build runs
at once: the compiles of `cc`, `--parallel` of cmake, which wins over
`CMAKE_BUILD_PARALLEL_LEVEL`, `-j` of cargo, `-p` of go build, `-j<N>` of
zig build. With `0` the compiles of `cc` and cmake run one a CPU, as they
do without `-j`, and cargo, go and zig are given no option, their own
default. make and shell get none either: their commands read
`TT_BUILD_JOBS`, which the core sets for every build. The jobs are in no
key of the cache of `cc`.

make, shell, cmake, go, cargo and zig are built without tt too
(`Backend.Exports`): the script of the export of a build (the template
`export.sh.tmpl` of each, which `Export` renders) runs the commands `Run`
runs, and `tt package pack` writes it into the rockspec of the rock of the
package, so that LuaRocks builds its source rock. A backend that says so
is to prove it: `export_test.go` checks each case of `exportCases` with
`buildtest.CheckExport`, which runs `Run` and the script on two copies of
one project and compares what they made, and fails for a backend that
exports and has no case there. The cases of cmake, go, cargo and zig
compile, and run under the `integration` build tag alone, with the
toolchains below; `export_internal_test.go` of each checks, with no
toolchain, that its script runs the argv `Run` runs and copies what `Run`
copies, its tools functions of sh that record what they are given. c and
lua-c build with tt alone so far.

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
