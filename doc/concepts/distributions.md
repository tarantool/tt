# Distributions and modules

tt is a core and a set of modules. The core parses the command line, finds
projects and scopes, and offers the services modules are built on; the
commands come from modules. There are two kinds of them.

## Compiled-in modules

A tt module is a Go module compiled into tt. Its package exports a
constructor, `func New(sdk.Services) []sdk.Mount`, that returns the commands
it adds, or `func New(sdk.Services) sdk.Module`, that returns the build
backends and the checks of the files of a package it adds as well. tt itself is the core with its **standard modules**: `builtin`,
its own commands, and the modules of its repository the core's package
`standard` lists, `aeon`, `build`, `coredump`, `emmylua` and `wasm`. `build`
has the build backends of tt: the core has none of its own. `wasm` has
`tt wasm` and the check of the `.wasm` files of a package.

A **distribution** of tt is the core and a set of modules built into one
binary, described by `tt.manifest.toml`. It takes the standard modules of
its core by default, at the commit of the core, and adds modules of its own;
it can leave some standard modules out, or put a module of its own in the
place of one. It can present itself under another name and edition, choose
an integrity provider, which
[signs and checks packages](../how-to/sign-packages.md), and leave out tt's
own commands or external modules. `tt version --modules` lists the modules
of a binary.

`tt binary build tt` builds a distribution. See
[Build a distribution of tt](../how-to/build-distribution.md); the API of
modules is described in
[the SDK](https://github.com/tarantool/tt/blob/v3/sdk/README.md).

## External modules

An external module is an executable in a modules directory, which tt runs
as a command of its own. It can add a command or replace one of tt's. See
[External modules](../how-to/external-modules.md).
