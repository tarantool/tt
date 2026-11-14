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
- `cc` - c and lua-c: one compiler call per module.
- `cargo` - Rust crates: a module, a program or both.
- `cmake` - CMake projects, built out of the source.
- `gobuild` - Go programs.
- `zig` - zig build of a `build.zig`: a module, a program or both.

The fields of each backend are described in
[the reference of the application manifest](../../doc/reference/app-manifest.md);
how to write a backend of your own, in
[Write a module](../../doc/how-to/write-a-module.md).

The tests that compile against Tarantool are behind the `integration` build
tag. They need a `tarantool` in `PATH` with its headers under its prefix,
`<prefix>/include/tarantool`, a C compiler, cmake, cargo and zig 0.16.0,
and skip what they cannot run:

```sh
go test -tags integration ./...
```

The zig test of a project of
[zig-tarantool](https://git.srht.bigb.es/~bigbes/zig-tarantool) builds it
against a checkout of zig-tarantool that has the helpers of its
`build.zig`, `addModule`, `installModule` and `addExecutable`, which
`TT_TEST_ZIG_TARANTOOL` names; without one, the test is skipped:

```sh
TT_TEST_ZIG_TARANTOOL=/path/to/zig-tarantool go test -tags integration ./zig/
```
