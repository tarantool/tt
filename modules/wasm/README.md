# The wasm module

`tt wasm` prepares a project for
[tarantool-wasm](https://github.com/tarantool/tarantool-wasm-rs), the
Tarantool module that runs WebAssembly components, adds components that
[tarawasm](https://github.com/mandesero/tarawasm) builds, reports how far a
project is prepared and packs the rocks of a release for a rock server. The
module also checks the `.wasm` files of every package tt builds. It is a tt
module: `wasm.New` is its constructor, an `sdk.ModuleConstructor`, which
gives tt the commands and the check, and it is one of the standard modules
of the core, so the official tt and every distribution that takes the
standard modules have it. A distribution that excludes it has no `tt wasm`
and checks no `.wasm` file.

This directory is a Go module of its own,
`github.com/tarantool/tt/modules/wasm`. Its `go.mod` requires the tt SDK
(`github.com/tarantool/tt/sdk`, `sdk/build` and `sdk/fetch`) and never tt's
root module, so the compiler refuses any import of tt's internals: the SDK
is all the module sees. Inside the tt repository the SDK modules come from
their directories through `replace`.

- `wasm.go` - the commands. `prepare` declares the binary rock `wasm`, the
  native module of the release, through the project of the SDK
  (`Project.AddDependency`), or, when the manifest declares it already
  (`Project.Manifest`), keeps the declaration and resolves the lock against
  it (`Project.Resolve`), printing how the lock moved; it installs tarawasm
  into `.tarantool` (`Project.StateDir`) and the toolchains of the
  languages, restores the SDK of each component and writes
  `.env/activate.sh` (`Project.PrepareEnvironment`). `new` has tarawasm
  create a component and declares it (`Project.AddComponent`), built by
  `tarawasm all` with the `shell` backend of the standard module `build`,
  says so, and resolves the lock again (`Project.Resolve`), which needs no
  Tarantool. `status` reads the manifest (`Project.Manifest`) and changes
  nothing. `rock` packs the rocks of a release and has the core make the
  directory a rock server (`Rocks().Index`). The commands work on the
  project the directory of the command is in (`Project.Root`).
- `filecheck.go` - the check of the `.wasm` files, a `build.FileCheck` of the
  module: every file of a package whose name ends with `.wasm`, laid out by a
  component or written by its build into `share/`, has to be a WebAssembly
  binary. A component passes, a core module passes with a warning, anything
  else fails the build.
- `internal/wasm` - the releases the module knows (`releases.go`,
  `TT_WASM_RELEASES` names a JSON file of others), the download of their
  assets and of the tools through `sdk/fetch`, the binary rock, tarawasm and
  its virtual environment, the toolchains of the languages, the templates of
  `new --template`, and the components of a project, read from the manifest
  and the `tarawasm.json` of each.

How to use the commands is in
[Run WebAssembly components](../../doc/how-to/wasm-components.md) and
[An HTTP API in a WebAssembly component](../../doc/write-your-app/http-api-in-wasm.md).
The end-to-end tests are in `test/integration/wasm` of the repository.
