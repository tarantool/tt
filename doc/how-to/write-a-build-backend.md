# Write a build backend

A build backend builds the components of a package. The build block of a
component names it:

```toml
[components.native.build]
backend = "cargo"
module = "fast"
```

The core of tt has no backend of its own. Every backend comes from a tt
module, a Go module compiled into a
[distribution of tt](build-distribution.md). tt's own backends - `make`,
`shell`, `c`, `lua-c`, `cargo`, `cmake`, `go` and `zig` - are the standard
module `build`, in `modules/build` of the repository of tt. They are
written against the same contract as the backend of any other module,
`github.com/tarantool/tt/sdk/build`, and they are examples of every kind of
backend.

This page writes a backend from scratch, `template`. It renders a template
of a component with `envsubst` into a data file of the package. The page
then tests it, builds it into tt and builds a package with it. How a module
is put together - its constructor, its commands, its distribution - is in
[Write a tt module](write-a-module.md).

The SDK is alpha: its API may change in any release of tt.

## Set up the Go module

```sh
mkdir tt-template && cd tt-template
go mod init example.com/tt-template
go get github.com/tarantool/tt/sdk@<commit> \
    github.com/tarantool/tt/sdk/build@<commit> \
    github.com/tarantool/tt/sdk/connect@<commit>
go get github.com/stretchr/testify
```

Require the SDK modules at one commit of tt, as
[Write a tt module](write-a-module.md#set-up-the-go-module) explains.
`sdk/build` is not in a release yet: `v3.0.0-alpha.2` does not have it, so
take a later commit of the core. Give `sdk/connect` too, even though the
module does not import it: the `go.mod` of `sdk` requires it at a
placeholder version that only tt's own repository resolves.

To build the module on a checkout of tt instead, with changes of the SDK
that are not pushed yet, run `tt binary workspace` in the directory of the
distribution below, whose `[core].path` names the checkout: it writes a
`go.work` that builds the module on the SDK of the checkout whatever its
`go.mod` requires, and `tt binary workspace --remove` deletes it once the
changes are pushed. [Develop together with the core](write-a-module.md#develop-together-with-the-core)
shows the cycle and how to require an SDK module no pushed commit has yet.
`go mod tidy` ignores `go.work`, so it needs the requirements of the
`go get` above. The distribution build ignores `go.work` too and builds the
module on the SDK of its core, whatever the `go.mod` of the module requires.

## Write the backend

`backend.go`:

```go
// Package template is a tt module with one build backend, template: it
// renders a template of a component with envsubst into a data file of the
// package.
package template

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// Name is the name of the backend: backend = "template" in a build block.
const Name = "template"

// tool is the program the backend runs.
const tool = "envsubst"

// Config are the fields of a template build block.
type Config struct {
	// Template is the template, relative to the working directory of the
	// build. Its name ends with .in. Required.
	Template string `toml:"template"`
	// Vars are the variables the template is rendered with.
	Vars map[string]string `toml:"vars"`
}

// Backend returns the template backend.
func Backend() build.Backend {
	return build.Backend{
		Name:        Name,
		Produces:    []build.Result{build.Data},
		Config:      func() any { return new(Config) },
		New:         build.Typed(newBuild),
		Incremental: true,
		OutputFrom:  build.OutputNone,
	}
}

// newBuild checks a template build block.
func newBuild(config *Config, _ build.Component, _ build.Common) (build.Build, error) {
	switch {
	case config.Template == "":
		return nil, build.FieldErrorf("", "template is required for backend %q", Name)
	case !strings.HasSuffix(config.Template, ".in"):
		return nil, build.FieldErrorf("template", "%q does not end with .in", config.Template)
	}

	for name := range config.Vars {
		if strings.HasPrefix(name, "TT_") {
			return nil, build.FieldErrorf("vars."+name, "the TT_* variables are tt's")
		}
	}

	return templateBuild{config: *config}, nil
}

// templateBuild is a checked template build block.
type templateBuild struct {
	config Config
}

// The core finds these interfaces of a build by type assertions.
var (
	_ build.Preflighter = templateBuild{}
	_ build.InputHasher = templateBuild{}
)

// Preflight checks that envsubst is in the PATH of the build.
func (b templateBuild) Preflight(_ context.Context, c *build.Context) error {
	_, err := c.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %s, which is not in PATH",
			build.ErrNoTool, Name, tool)
	}

	return nil
}

// Inputs are what an incremental build reads besides the build block and
// the files of the component: the version of envsubst, and the template,
// which is outside the component when cwd is.
func (b templateBuild) Inputs(ctx context.Context, c *build.Context) ([]string, error) {
	version, err := c.Output(c.Command(ctx, tool, "--version"))
	if err != nil {
		return nil, err
	}

	firstLine, _, _ := bytes.Cut(version, []byte("\n"))

	data, err := os.ReadFile(filepath.Join(c.Dirs.Cwd, b.config.Template))
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(data)

	return []string{
		string(firstLine),
		b.config.Template + " sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

// Run renders the template into the share directory of the component.
func (b templateBuild) Run(ctx context.Context, c *build.Context) error {
	source, err := os.Open(filepath.Join(c.Dirs.Cwd, b.config.Template))
	if err != nil {
		return err
	}

	defer source.Close()

	cmd := c.Command(ctx, tool)

	cmd.Stdin = source
	for name, value := range b.config.Vars {
		cmd.Env = append(cmd.Env, name+"="+value)
	}

	out, err := c.Output(cmd)
	if err != nil {
		return err
	}

	name := strings.TrimSuffix(filepath.Base(b.config.Template), ".in")

	return os.WriteFile(filepath.Join(c.Dirs.Share, name), out, 0o644)
}
```

The rest of this page explains each part.

## What the backend says of itself

A `build.Backend` is a value. Its fields tell the core what the backend is:

| Field | |
| --- | --- |
| `Name` | the value of `backend` that selects it: a lowercase letter, then lowercase letters, digits and dashes; no two backends of a tt share one |
| `Produces` | the types of what its builds make, each once: `build.Module`, `build.Data`, `build.Program`, `build.Link` (see [Results](#results)) |
| `Config` | returns a fresh, empty configuration: a pointer to a struct of the fields of the block |
| `New` | checks a block and returns the build; `build.Typed(newBuild)` makes it of a function that takes the configuration as its own type, `*Config` |
| `Incremental` | allows `incremental = true` in the block |
| `OutputFrom` | whether the block has `output`, and who copies what it lists |
| `Exclude` | gitignore patterns of files the backend's tool leaves in the component, which are never laid out: `/target/` of `cargo` |
| `NoSourceLayout` | switches off the default `include` of the component, the Lua sources and the prebuilt modules: for a backend whose sources are no files of the package, as `go` |
| `Hook` | lets a lifecycle hook, `[hooks.pre_build]`, `[hooks.post_build]` or `[hooks.clean]`, name the backend |
| `Cross` | lets its builds be for another platform than the machine tt runs on, `tt package build --target` (see [Other platforms](#other-platforms)) |
| `Exports` | says that its builds are made without tt too, by a script of sh, so that LuaRocks builds the source rock of a package of it (see [Build without tt](#build-without-tt)) |

The core calls `Config` and `New` whenever it checks a manifest: every
command that reads the manifest does, `tt package add` included, and it may
call them more than once for one block. Neither may have side effects, and
both are to be cheap. `New` looks at the block only, and is given no
directory of the project; a tool or a file the build needs is looked for
later, in `Preflight`.

## The fields of the block

The core decodes the fields of the block into the struct `Config` returns.
A field's key is its `toml` tag, or the Go name of the field when it has no
tag; `toml:"-"` leaves a field out, and the fields of an embedded struct are
fields of the block. A field is a string, a boolean, a number, a slice, a
map with string keys, a struct of such fields, a pointer to one of these, or
`any`. A pointer tells a field that is not set from its zero value.

The fields every block has - `backend`, `cwd`, `env`, `output`,
`incremental` and `static` - are the core's. They are not in `Config`: the
core gives them to `New` as `build.Common`.

The decoding is strict. A block of the `template` backend with a mistake
fails every command that reads the manifest, with exit code 1:

```text
⨯ invalid manifest: components.config.build.vars.PORT: must be a string, not an integer
⨯ invalid manifest: components.config.build.templat: unknown field in manifest version "0.1"
⨯ invalid manifest: components.config.build.make_target: is a field of backend make, not of "template"
```

The first is `vars = { PORT = 3301 }`. The second is a key no backend has.
The third is a key another backend has: the error names the backends that
have it.

## Check the block

`New` gets the decoded configuration, the component and the common fields.
It returns the build, or an error. The field `New` of `build.Backend` takes
the configuration as `any`, since backends of different configurations sit
in one list; `build.Typed` hands it to `newBuild` as the `*Config` it is.

An error of a field is a `build.FieldError`, made by `build.FieldErrorf`.
Its `Field` is the path of the field from the block, as the block writes
it: `template`, `vars.TT_PACKAGE`, `platforms.linux.cflags`, `sources[1]`.
An empty `Field` is the block itself. The core puts the path of the block
in front:

```text
⨯ invalid manifest: components.config.build: template is required for backend "template"
⨯ invalid manifest: components.config.build.template: "defaults.lua" does not end with .in
⨯ invalid manifest: components.config.build.vars.TT_PACKAGE: the TT_* variables are tt's
```

Any other error of `New` is reported for the block. There are no tags for
required fields or defaults: `New` checks them.

`build.Component` is the component: its `Name`, its `Path` relative to the
project, its `Namespace` and its `Package`. `Wants` are the types of
results the manifest asks the component for besides its build block, nil
when it asks for none, as a manifest is to. The core checks the build
against them; a backend that picks what to build when the block does not
say may follow them, as `cargo` and `zig` build the program named after the
component when `Wants` is `build.Program` alone. The one form of a manifest
that asks so far is deprecated, so a new backend may leave them to the
core. `build.Common` has `Cwd`, `Env`, `Output` and `Static` of the block.

## Run the build

`Run` builds the component in a `build.Context`. The core makes one for each
component and build.

| Field | |
| --- | --- |
| `Component` | the component |
| `Target` | the platform, as `GOOS` and `GOARCH` name it: the machine tt runs on, `build.HostTarget()`, or for a backend with `Cross` the platform of `--target` (see [Other platforms](#other-platforms)); `ModuleExt` is `.so`, or `.dylib` on darwin, `String` is `<os>-<arch>` and `Cross` tells another platform than the machine |
| `Dirs` | the directories of the build, see below |
| `Tarantool` | the Tarantool the component is built against: `Prefix`, `IncludeDir` (its C headers, `module.h` and `lua.h`), `Version`, `Executable` (the `tarantool` tt found, to check what the build made, a module that loads say, never needed to build), each empty when tt does not know it |
| `Env` | the `env` of the block and the `TT_*` variables of the build |
| `Fresh` | set by `tt package build --rebuild`, and for a component whose block changed since its last build: drop what `Work` keeps and build from scratch |
| `Locked` | set by `--locked`: a tool of the build must not move a version either; `LockedVersion` returns the version the lock pins a rock at |
| `Static` | `static` of the block |
| `Args` | the arguments a command of the module passed through to this build, for its tool (`sdk.BuildOptions.Args`: `tt wasm build calc -- -opt=2`); none in a build of `tt package build`. They are an input of an incremental build |
| `Jobs` | the most jobs the build runs at once, set by `-j`, `--jobs` (`sdk.BuildOptions.Jobs`); `0` leaves it to the backend, one a CPU. Hand it to the tool only when it is set, `cargo -j`, so that the tool's own default stays otherwise; `JobsOrCPUs` is `Jobs`, or the number of CPUs when it is `0`, as `TT_BUILD_JOBS` has it. It is not an input of an incremental build: a build with other `Jobs` is skipped all the same |
| `Hook` | set for the build of a lifecycle hook |
| `Log`, `Warn` | the logger of the build, tt's with `backend=<name>`, whose debug records `tt -V` shows, and a function that shows a warning |

The directories, all absolute, for a component of the package:

| Directory | |
| --- | --- |
| `Project` | the root of the project: the package, or a path dependency or a source rock being built |
| `Component` | the directory of the component |
| `Cwd` | where the build runs: `cwd` of the block, relative to the project, or `Component` |
| `Work` | `_build/component/<name>/`, `TT_BUILD_DIR`: intermediate files, kept between builds, emptied by `--rebuild` and when the block changed |
| `Modules` | where modules go: `_build/rock/<product>/lib/<namespace>/`, `TT_OUTPUT_DIR` |
| `Share` | where data files go: `_build/rock/<product>/lua/<namespace>/` |
| `Programs` | where programs go: `_build/rock/<product>/bin/`, shared by the components |
| `State` | `_build/`, the state of the build of the project |
| `Tools` | `.tarantool/bin/` of the project |
| `Tree` | `.rocks/`, the rocks tree the dependencies are installed into |
| `Link` | where libraries for the components built after this one to link go: `_build/link/<product>/<name>/`, emptied before each build of the component, never installed nor packed; `Dirs.LinkOf(name)` is that of another component |

The core creates `Work`, `Modules`, `Share`, `Programs` and `Link` before
`Run`.

The Context says which Tarantool the build is for, not how to compile for
it: a backend that compiles C takes the headers from `Tarantool.IncludeDir`
(`TT_TARANTOOL_INCLUDE_DIR`) and links a module as Tarantool loads one,
`-shared`, or `-bundle -undefined dynamic_lookup` on darwin, with the
compiler it chooses. `c` and `lua-c` (`modules/build/cc`) do so with `CC`
and `CXX` of tt's environment, as LuaRocks builds a builtin rock, an object
a source in `Work`.

### Commands

`c.Command(ctx, name, args...)` returns an `*exec.Cmd` that runs in `Cwd`.
Its environment is tt's, then the `env` of the block, then `PATH` with
`.tarantool/bin` of the project first when it exists, then the `TT_*`
variables, so that the block cannot change them. The backend may append to
`cmd.Env`: what it appends wins. `build.LookupEnv` reads a variable from
`cmd.Env`. The example appends `vars` so.

- `c.Run(cmd)` runs the command as tt runs the commands of a build: what it
  prints is shown as it runs with `tt -V`, and only when it fails otherwise.
- `c.Output(cmd)` runs it the same way and returns its stdout, which is not
  shown: for a tool that answers in JSON, or `envsubst`, whose stdout is the
  result.
- `c.LookPath(name)` finds a program in the `PATH` of the build, and only
  there: a `PATH` the `env` of the block sets is the whole `PATH` of the
  build, and a program of tt's own `PATH` is not found then. A program it
  does not find is an error wrapping `exec.ErrNotFound`; `c.Command` of
  it makes a command that fails to start with that error.

The `TT_*` variables are `TT_OUTPUT_DIR`, `TT_BUILD_DIR`,
`TT_COMPONENT_NAME`, `TT_PROJECT_ROOT`, `TT_PACKAGE`, `TT_VERSION`,
`TT_PLATFORM_OS`, `TT_PLATFORM_ARCH`, `TT_BUILD_JOBS` (`JobsOrCPUs`, never
`0`), and `TT_TARANTOOL_PREFIX` and `TT_TARANTOOL_INCLUDE_DIR` when tt
knows them. A template of the example
can use them: `${TT_PACKAGE}`, `${TT_VERSION}`. `envsubst` replaces every
variable it is given, tt's environment included, and a variable that is
not set with nothing.

### Results

What a build makes has a type. The type says where it goes:

| Result | The build puts it into | It is installed into |
| --- | --- | --- |
| `build.Module`, a compiled module Tarantool loads | `Dirs.Modules` | `.rocks/lib/tarantool/<namespace>/` |
| `build.Data`, a file beside the Lua sources, such as `.wasm` or a generated Lua file | `Dirs.Share` | `.rocks/share/tarantool/<namespace>/` |
| `build.Program`, a program the instances run | `Dirs.Programs` | `.rocks/bin/`, where `tt instance` finds it |
| `build.Link`, a library, or a header of one, for the `c` and `lua-c` components built after it to link (`link` of their block) | `Dirs.Link`, a header into `include/` of it (`Dirs.OutputPath`) | nowhere |

The first three types are the places of a rock, `lib`, `share` and `bin`,
which the core lays out, records and cleans, and which `to` of `output`
names; `build.Link` is the place of a build that is not in the rock,
`to = "link"`, whose files the core does not record as made. The set is
the core's: a backend says which of the types it makes and adds none of its
own, and `Validate` refuses a backend that names another. What is no module
and no program, a `.wasm` say, is a data file. A type a later version of
the SDK adds comes with a directory of `Dirs` and an entry of
`build.Results()`, as `build.Link` came, last; the values of the types,
`module`, `data`, `program` and `link`, never change, so a backend that
handles the types it produces is not broken by a new one, and one whose
`Produces` is `build.Results()` makes the new one at once.

One build may make all three. The core takes as made what `Run` left in
these three directories, each by its type, and removes from them what no
build made. A file in the directory of a type the backend does not
produce fails the build with exit code 2, `build.ErrNotProduced`, and so
does a program a `ProgramNamer` names for a backend without
`build.Program`:

```text
⨯ building component "config": the build made what its backend does not produce: backend "template" makes no modules: lib/myapp/defaults.so
```

What an earlier build left there, of another backend the block named
before, is not taken as made by this one, and leaves the package.

The core then checks what was made. Each program has to be an executable
ELF or Mach-O program of the target, and a static one when the block says
`static = true`; a component of the deprecated `kind` has to make what its
`kind` says. It records what each component made in `_build/results.json`:

```json
{
  "components": {
    "config": {
      "data": [
        "defaults.lua"
      ]
    }
  }
}
```

The lists are `modules`, `data` and `programs`, each relative to its
directory. A build of one component knows by the record what the others
made, and `tt instance` tells by it which component makes a program.

A backend whose tool writes elsewhere copies what it built into these
directories with `build.CopyFile`, each file where `Dirs.OutputPath(to,
name)` says. It writes the copy with no fork of tt in
between, so that a program it copies can be started at once: Linux refuses
to run a file that is open for writing, and a process tt forks while it
writes holds the file open until it runs its own program.

An error of `Run` fails the build with exit code 2:

```text
⨯ building component "config": open /home/me/myapp/config/missing.lua.in: no such file or directory
```

## Output: lib, share and bin

`output` of a build block lists files the build makes, for a backend whose
tool writes them where it likes rather than into the directories of the
Context. An entry is a path, or a table with `path` and `to`: `to = "lib"`
is a module, `to = "share"` a data file, `to = "bin"` a program,
`to = "link"` a library for another component to link. One entry
may be written alone, `output = "dist/banner.txt"` for
`output = ["dist/banner.txt"]`. An entry with no `to` is of the one type of
a backend whose `Produces` has one type only - a data file of a backend of
data files - and a module of any other backend. The core gives the entries
to `New` in `Common.Output`, each a `build.OutputFile` with its `Path` and
its type, `To`. `OutputFrom` says whether the backend has the field:

- `build.OutputNone`: no `output`. The build puts what it makes into the
  directories of the Context itself, as `template`, `cargo` and `zig` do. A
  block with `output` is refused:
  `components.config.build.output: is a field of backend make, shell or cmake, not of "template"`.
- `build.OutputCwd`: the core copies each file `output` lists, relative to
  `Cwd`, into the directory of its type under its base name
  (`Dirs.OutputPath`), after `Run` succeeds. `make` and `shell` work so
  (`modules/build/basic`).
- `build.OutputBackend`: the backend reads `Common.Output` and copies the
  files itself, from where it built them. `cmake` copies them from its
  build tree (`modules/build/cmake`).

The core refuses an entry whose `to` is not among `Produces`, before
anything is built.

### A backend whose output the core copies

`render` is `template` for a tool that writes its file into the directory
it runs in: the build renders the template into the file `output` names, in
`Dirs.Cwd`, and the core copies it into the package. `render.go`:

```go
package template

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/build"
)

// RenderConfig are the fields of a render build block.
type RenderConfig struct {
	// Template is the template, relative to the working directory of the
	// build. Required.
	Template string `toml:"template"`
}

// RenderBackend returns the render backend: it renders a template with
// envsubst into the one file output names, relative to the working
// directory of the build, which the core copies into the package.
func RenderBackend() build.Backend {
	return build.Backend{
		Name:       "render",
		Produces:   []build.Result{build.Data},
		Config:     func() any { return new(RenderConfig) },
		New:        build.Typed(newRender),
		OutputFrom: build.OutputCwd,
	}
}

// newRender checks a render build block: a template, and one file of output.
func newRender(config *RenderConfig, _ build.Component, common build.Common) (
	build.Build, error,
) {
	switch {
	case config.Template == "":
		return nil, build.FieldErrorf("", "template is required for backend %q", "render")
	case len(common.Output) != 1:
		return nil, build.FieldErrorf("output", "names %d files; render makes one",
			len(common.Output))
	}

	return renderBuild{template: config.Template, file: common.Output[0].Path}, nil
}

// renderBuild is a checked render build block.
type renderBuild struct {
	template string
	file     string
}

// Run renders the template into the file of output, in the working
// directory of the build.
func (b renderBuild) Run(ctx context.Context, c *build.Context) error {
	source, err := os.Open(filepath.Join(c.Dirs.Cwd, b.template))
	if err != nil {
		return err
	}

	defer source.Close()

	cmd := c.Command(ctx, tool)

	cmd.Stdin = source

	out, err := c.Output(cmd)
	if err != nil {
		return err
	}

	path := filepath.Join(c.Dirs.Cwd, b.file)

	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, 0o644)
}
```

`New` of the module (`module.go`, below) provides both backends:
`Backends: []build.Backend{Backend(), RenderBackend()}`. Once the module is
in tt ([Build it into tt](#build-it-into-tt)), a package uses it; in
`myapp/app.manifest.toml` of [Use it in a package](#use-it-in-a-package):

```toml
[components.banner]
path = "banner"

[components.banner.build]
backend = "render"
template = "banner.txt.in"
output = "dist/banner.txt"
```

`myapp/banner/banner.txt.in`:

```text
${TT_PACKAGE} ${TT_VERSION}
```

The block says no `to`, and `render` makes data files only, so the file is
a data file. The build runs in `banner/`, the directory of the component,
which is the working directory of the build since the block has no `cwd`:
it writes `banner/dist/banner.txt`, and the core copies it under its base
name into `share/` of the namespace of the component, the package name,
which `tt package build` installs as `.rocks/share/tarantool/myapp/`:

```console
$ ../dist/tt package build
$ cat banner/dist/banner.txt
myapp 0.0.0-dev.0
$ cat .rocks/share/tarantool/myapp/banner.txt
myapp 0.0.0-dev.0
$ ../dist/tt run -e "local fio = require('fio') \
    local file = fio.open('.rocks/share/tarantool/myapp/banner.txt') \
    io.write(file:read()) os.exit()"
myapp 0.0.0-dev.0
```

The Lua code of the package reads it beside itself, as
`fio.pathjoin(debug.sourcedir(), 'banner.txt')` in a module of the
namespace `myapp`; other code reads it from the directory of the
application, the working directory of its instances, as above. Neither
`banner.txt.in` nor `dist/` is laid out: the default `include` takes
`*.lua` and `*.so`. The core refuses what the block asks for and `render`
does not make before anything is built, with exit code 1, as for
`output = { path = "dist/banner.txt", to = "lib" }`:

```text
⨯ invalid manifest: components.banner.build.output[0].to: backend "render" makes no modules
```

A build whose `Run` succeeds and leaves no file where `output` says fails
with exit code 2, `building component "banner": copy declared output
"dist/banner.txt": opening the source: ...: no such file or directory`. A
path of `output` is relative to `Dirs.Cwd` for `build.OutputCwd`: a block
with `cwd = "."` names `output = "banner/dist/banner.txt"` and a template
relative to the project. [The `output` field](../reference/app-manifest.md#output)
of the manifest reference has where every `to` is installed.

## More of the build

A build may implement more interfaces. The core asks for each with a type
assertion, so a method of another signature would never be called: the
core refuses a build whose `Preflight`, `Produces`, `Programs`, `Links`,
`Inputs`, `Clean`, `Vendor` or `Export` is not the method of the interface, or is a method of a pointer while
`New` returns a value, when it checks the manifest, with exit code 1:

```text
⨯ invalid manifest: components.config.build: invalid build backend "template": Preflight of its build template.templateBuild is func(*build.Context) error, not func(context.Context, *build.Context) error of build.Preflighter: the core would never call it
```

`buildtest.Validate` refuses it too. The `var _ build.Preflighter =
templateBuild{}` lines of the example make the compiler say so first.

A `build.Preflighter` checks what the build needs before any component is
built, so that a missing tool fails the build at once, not after the first
long compile. `Preflight(ctx, c)` runs before the version of the package is
known: the directories of the Context may not exist yet, and `TT_VERSION`
is empty. It runs before the lock is resolved, too: `c.LockedVersion(rock)`
answers from `app.manifest.lock` when the lock is fresh, and says the rock
is not pinned when the lock is stale or missing, which the build resolves
afterwards; with `--locked` such a lock fails the build, so the answer is
exact there. `ctx` is that of the build: make the commands `Preflight` runs
with it, so that they stop when the build is cancelled. Wrap
`build.ErrNoTool` for a tool that is not in `PATH`, so that
a missing tool is told from a failed build, and `build.ErrNoHeaders` for a
build that compiles against the headers of Tarantool when
`Tarantool.IncludeDir` is empty: tt then adds the directories it looked
into for them, and how to point it at some. A failed `Preflight` fails the
build with exit code 2:

```text
⨯ building component "config": build tool not found: the template backend runs envsubst, which is not in PATH
```

The backends of tt that have a `Preflight` call it at the start of `Run`
too. The other interfaces:

- `build.Producer`: `Produces()` says what this block asks for, each type
  among `Backend.Produces`. The core checks it against `Component.Wants`
  before anything is built. `cargo` and `zig` make a module, a program or
  both, as `module` and `bin` say.
- `build.ProgramNamer`: `Programs()` names the programs the build makes, and
  says whether the list is exhaustive. The core checks with it that no two
  components make one program, and `tt instance` finds by it the component
  that makes a program. When the list is not exhaustive, the core adds what
  the build wrote into `Programs`.
- `build.Linker`: `Links()` names the components of the package whose
  libraries the build links, from `Dirs.LinkOf` of each. The core checks,
  with the manifest, that each is a component every product of this one
  builds before it, and refuses the manifest otherwise, naming both.
  `c` and `lua-c` are linkers by their `link`.
- `build.InputHasher`: see the next section.
- `build.Cleaner`: see [Clean](#clean).
- `build.Vendorer`: see [Offline builds](#offline-builds).
- `build.Exporter`: see [Build without tt](#build-without-tt).

## Other platforms

`tt package build --target linux/arm64` builds the components for another
platform than the machine tt runs on (see
[Other platforms](../reference/app-manifest.md#other-platforms)). A
backend says whether its builds can be made for one with `Cross: true`;
without it, the default, a component of the backend fails such a build
before a hook or anything else runs, with exit code 1, naming the
component, the backend and both platforms:

```text
⨯ building component "config": the build cannot be made for the target platform: the template backend builds for the machine tt runs on alone, linux-amd64, and the build is for linux-arm64
```

`build.Backend.CheckTarget(target)` is that check, an error wrapping
`build.ErrTarget`. A build of a backend with `Cross` makes what it makes for
`Context.Target`, `TT_PLATFORM_OS` and `TT_PLATFORM_ARCH` of `Env`: `go`
sets `GOOS` and `GOARCH`, `zig` passes `-Dtarget`, `cargo` builds for the
Rust target of the platform, and `make` and `shell` leave it to the
Makefile or the command. `c.Target.Cross()` tells a build for another
platform from one for the machine. A build that cannot be made for the
target it is given - `go` with `cgo = true`, whose C compiler is the
machine's, a `zig` build.zig that takes no `-Dtarget` - refuses it in
`Preflight` with an error wrapping `build.ErrTarget`, which the core reports
with exit code 1, as its own refusal, rather than 2.

`template` makes text, which runs on any platform, and could say `Cross:
true`. A backend that compiles with the toolchain of the machine, as `c`,
`lua-c` and `cmake` do, says nothing: a compiler of another platform is
not one tt knows how to find.

What a build for another platform made is checked against it as what one
for the machine made is against the machine: a module or a program of
another platform fails the build with exit code 2. Such a build has a state
of its own, `Dirs.State` being `_build/target/<os>-<arch>/`, and with it
`Work`, `Link`, the stage of `Modules`, `Share` and `Programs`, and what a
backend keeps in `State`, as the target directory of cargo: nothing of a
build for one platform is taken for that of another. `Tarantool.Executable`
is empty there: the tarantool of the machine cannot load what the build
made. The package is installed into `_build/target/<os>-<arch>/tree/`
rather than into `Dirs.Tree`, `.rocks/`, which stays the tree of the
dependencies, built for the machine.

`buildtest.Harness.SetTarget(build.Target{OS: "linux", Arch: "arm64"})`
sets the target of a test, in `Context.Target` and in `TT_PLATFORM_OS` and
`TT_PLATFORM_ARCH`. The harness runs the build for it whether the backend
says `Cross` or not, so that a test checks what a backend does for a
platform it has no toolchain of, with a fake compiler; the core refuses it.

## Incremental builds

`Incremental: true` allows `incremental = true` in a block. The core then
skips the build of a component whose inputs are those of its last build,
when what that build left is still there. The inputs are the build block and
the files of the component, without those its `.gitignore` files ignore.
The hash of the last build is in `_build/cache/<component>.json`.

A build that reads more is a `build.InputHasher`. `Inputs(ctx, c)` returns
strings the core adds to the inputs. `template` returns the version of
`envsubst` and the hash of the template, which is outside the component when
`cwd` is; `zig` returns its version and the hashes of `build.zig` and
`build.zig.zon`. `Inputs` runs on every build, the skipped ones too. A
skipped build does not call `Run`, and `--rebuild` skips nothing:

```console
$ tt -V package build
   ...
   · running envsubst --version dir=/home/me/myapp/config
   · component is up to date component=config
```

A backend without `Incremental` refuses the field:
`components.native.build.incremental: is a field of backend make, shell, zig or template, not of "cargo"`.

## Clean

`tt package clean` removes what the builds of a package made. The core
removes what is its own: for a clean of the whole package, all of
`Dirs.State`, `_build/` - `Dirs.Work` of every component, the records of the
builds, the stage that `Dirs.Modules`, `Dirs.Share` and `Dirs.Programs` are
in - but the lock of the project, `_build/tt.lock`; for
`tt package clean NAME`, `Dirs.Work` of the component, its records and its
files in the stage. Either way the rock of the package leaves `Dirs.Tree`.
A backend that writes only into these directories, as `template` does, has
nothing more to clean.

What a tool leaves anywhere else - the directory of the component,
`Dirs.Cwd`, `Dirs.Tools` - the core does not remove: it cannot tell a file a
build made there from one the project keeps, and a file `output` lists may
be a prebuilt one in git. A build that leaves files there is a
`build.Cleaner` and removes them in `Clean(ctx, c)`. `render` writes the
file of `output` into `Dirs.Cwd`; add to `render.go`, with `errors` and
`io/fs` imported:

```go
// The core finds these interfaces of a build by type assertions.
var _ build.Cleaner = renderBuild{}

// Clean removes the file the build rendered into its working directory:
// the core removes the copy in the package, not what is beside the sources.
func (b renderBuild) Clean(_ context.Context, c *build.Context) error {
	err := os.Remove(filepath.Join(c.Dirs.Cwd, b.file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return err
}
```

`Clean` runs for every component of the backend the clean cleans, before
the core removes anything, so that `Dirs.Work` is still there for a tool
that cleans its build tree itself. `c` is the Context of a build of the
component, without `Args`. Its directories may not exist, for a component
that was never built, so a file that is not there is no error. `ctx` is that
of the clean: make the commands `Clean` runs with it. A failed `Clean` fails
the clean with exit code 2, and nothing is removed. `--dry-run` runs no
`Clean`, and says which would run:

```console
$ ../dist/tt package build
$ ../dist/tt package clean --dry-run
would run the clean of component banner
would remove _build/cache
would remove _build/component
would remove _build/results.json
would remove _build/rock
would remove myapp 0.0.0.dev.0-1 from .rocks (5 files)
  .rocks/share/tarantool/rocks/myapp/0.0.0.dev.0-1/myapp-0.0.0.dev.0-1.rockspec
  .rocks/share/tarantool/myapp/_rocks.lua
  .rocks/share/tarantool/myapp/_version.lua
  .rocks/share/tarantool/myapp/banner.txt
  .rocks/share/tarantool/myapp/defaults.lua
$ ../dist/tt package clean
ran the clean of component banner
removed _build/cache
removed _build/component
removed _build/results.json
removed _build/rock
removed myapp 0.0.0.dev.0-1 from .rocks (4 files)
$ ls banner
banner.txt.in  dist
$ ls banner/dist
$ ls -A _build
tt.lock
```

`dist/` stays, empty: `Clean` removes the one file it knows. In a test,
`harness.Clean(ctx, bld)` runs the `Clean` of a build as the core does, and
nothing after it:

```go
func TestRenderClean(t *testing.T) {
	banner := build.Component{Name: "banner", Path: "banner", Namespace: "app", Package: "app"}
	block := buildtest.Block{
		Common: build.Common{Output: []build.OutputFile{{Path: "dist/banner.txt", To: build.Data}}},
		Fields: map[string]any{"template": "banner.txt.in"},
	}

	bld, err := buildtest.Validate(template.RenderBackend(), banner, block)
	require.NoError(t, err)

	harness := buildtest.New(t, template.RenderBackend(), banner)
	require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Cwd, "banner.txt.in"),
		[]byte("${TT_PACKAGE}\n"), 0o600))
	require.NoError(t, harness.Run(t.Context(), bld))

	rendered := filepath.Join(harness.Context.Dirs.Cwd, "dist", "banner.txt")
	require.FileExists(t, rendered)

	require.NoError(t, harness.Clean(t.Context(), bld))
	assert.NoFileExists(t, rendered)
	require.NoError(t, harness.Clean(t.Context(), bld), "a file that is not there is no error")
}
```

What the project itself generates beside the sources, which no backend
knows of, is for the `[hooks.clean]` of its manifest, which a clean of the
whole package runs after the backends; see
[the reference](../reference/app-manifest.md#hookspre_build-hookspost_build-and-hooksclean).

## Offline builds

`tt package download --dir DIR`, with no arguments, makes DIR a mirror of
what an offline build of the project needs: the rocks of its lock, and what
the builds of its components download themselves, as cargo downloads the
crates of a package. A backend whose tool downloads is a `build.Vendorer`:
`Vendor(ctx, c, dir)` downloads into `dir`, `DIR/<backend>`, what the build
of the component would, as the lock of the sources pins it. The components
of the backend share `dir`, those of other projects downloaded into the same
mirror too: name what you put there by its name and version, and keep what
is there, since a mirror is extended in place. `c` is the Context of a build
of the component with `Locked` set, and a failed `Vendor` fails the download
with exit code 2.

A build whose rock servers have the mirror, `TT_REGISTRIES=DIR tt package
build`, is given `DIR/<backend>` in `c.Vendored`, of the first server that
is a directory and has one: take from it what the tool would download, and
download nothing. Without one `c.Vendored` is empty and the build downloads
as ever. `cargo` vendors the crates of `Cargo.lock` with `cargo vendor`,
and builds from them with `--offline`; `go` downloads the modules of
`go.sum` with `go mod download` into the layout of a module proxy, and
builds with it for `GOPROXY`; `zig` fetches the packages of
`build.zig.zon` with `zig build --fetch=all`, each into the directory of
its hash, and builds with the mirror for `--pkg-dir`. In a test,
`harness.Vendor(ctx, bld, dir)` runs the `Vendor` of a build as the
download does, and setting `harness.Context.Vendored` makes a build from a
mirror.

## The lock of the project

The core builds the components of a package one after another, and holds
the lock of the project, `_build/tt.lock`, through the whole build, the
clean, and every other command that changes the project. No other tt
changes `_build/`, `.rocks/`, the manifest or the lock meanwhile: a second
`tt package build` of the project waits for the first. A backend needs no
lock of its own for what it writes into the directories of its Context.

A tt that a build runs on the same project - a `shell` block whose command
is `tt package fetch`, say - works under the lock of the tt that runs it:
tt names the locks it holds in `TT_PROJECT_LOCK`, which every command of
the build inherits, through `c.Command` or not. A tt that a build starts in
the background and does not wait for would work beside the build, so a
build waits for what it runs.

## Lifecycle hooks

`[hooks.pre_build]` and `[hooks.post_build]` run around the builds of all
the components, and `[hooks.clean]` in `tt package clean` of the whole
package, after the `Clean` of the backends. A hook names a backend with
`Hook: true`, `make` or `shell` in tt; another is refused,
`hooks.pre_build: backend "template" is not supported (want make or shell)`.
The core checks the block of a hook as it checks a build block, `New` given
the zero `Component`. The build runs in a Context whose `Hook` is set. It
has no component, and `Dirs` has no `Component`, `Work`, `Modules`, `Share`
and `Programs`. `Env` has no `TT_OUTPUT_DIR`, `TT_COMPONENT_NAME` and
`TT_BUILD_DIR`, and the files `output` lists are not copied.

## Build without tt

tt builds the source rock of a package, `tt package pack --format
rock-src`, from its manifest. LuaRocks - `tt rocks`, that of tt 2 among
them - knows no manifest and no backend: it builds the source rock by the
rockspec, which runs `tt-rock.sh`, a script of POSIX sh the rockspec carries
(see [Packing a rock](../concepts/packages.md#packing-a-rock)). The script
is the frame of a build of tt - the variables, the order of the components,
the hooks, the copy of `output`, the layout - and the builds are the
backends': a backend whose builds are made by a script of sh too says so
with `Exports: true`, and each of its builds is a `build.Exporter`, whose
`Export` returns the script. A package builds without tt when every build of
its product and of its `pre_build` and `post_build` hooks exports; otherwise
the rockspec fails under LuaRocks, saying which builds need tt and why, and
the pack warns so. `make` and `shell` export, the command each runs, and
`c` and `lua-c` the compiles and the link of their `Run`, with the compiler
of LuaRocks, but for `static = true` and `bundle`, which are made with tt
alone, and `cmake`, `go`, `cargo` and `zig` the commands of their tool `Run`
runs (see [Without tt](../reference/app-manifest.md#without-tt)).

`template` runs `envsubst`, which a script runs as well. Add
`Exports: true` to `Backend()`, and to `backend.go`, with `maps`, `regexp`
and `slices` imported:

```go
// The core finds these interfaces of a build by type assertions.
var _ build.Exporter = templateBuild{}

// shellName is the shape of the name of a variable of sh.
var shellName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Export renders the template as Run does, by a line of sh: envsubst with
// the variables of the block before it, each value a word of sh. A variable
// whose name sh cannot set makes the build one of tt alone.
func (b templateBuild) Export(*build.ExportContext) (build.Export, error) {
	var line []string

	for _, name := range slices.Sorted(maps.Keys(b.config.Vars)) {
		if !shellName.MatchString(name) {
			return build.Export{}, build.NoExportf("vars."+name,
				"sh sets no variable of the name %q", name)
		}

		line = append(line, name+"="+build.ShellQuote(b.config.Vars[name]))
	}

	name := strings.TrimSuffix(filepath.Base(b.config.Template), ".in")
	line = append(line, tool, "<", build.ShellQuote(b.config.Template), ">",
		`"$TT_SHARE_DIR"/`+build.ShellQuote(name))

	return build.Export{Script: strings.Join(line, " "), Tools: []string{tool}, Lost: nil}, nil
}
```

- `Export(e)` is given what is known when the package is packed, a
  `build.ExportContext`: the `Component`, the zero one for a hook, `Hook`,
  and `LockedVersion`, which answers from the lock the package is packed
  with, for a script that pins a tool to the version of a rock. What only
  the machine that builds knows - its directories, its platform, its
  Tarantool, its compiler - the script reads from its variables.
- `Script` is run with `set -eu`, in a subshell whose working directory is
  that of the build, after `pre_build` and the builds of the components
  before it in the product. It puts what it makes where `Run` does:
  modules into `"$TT_OUTPUT_DIR"`, data files into `"$TT_SHARE_DIR"`,
  programs into `"$TT_PROGRAMS_DIR"`, libraries for linking into
  `"$TT_LINK_DIR"`. For a backend of `OutputCwd` the script of the rock
  copies the files `output` lists after it, as the core does after `Run`;
  one of `OutputBackend` copies them in its own script. A literal of the
  block goes in through `build.ShellQuote`, which quotes it as one word of
  sh whatever it holds: `it's $HOME` is `'it'\''s $HOME'`. The script is
  written into the rockspec as it is, its lines not indented, so a
  here-document in it ends as it would alone.
- `Tools` are the programs the script runs: the script of the rock looks for
  each in `PATH` with `command -v` before the first build, as `Preflight`
  does, and fails naming the tool and the component. A program the machine
  names, `CC` of the environment say, is none of them: its value may be the
  words of a wrapper, `ccache cc`, which `command -v` does not take, so the
  script looks for its first word itself, as that of `c` and `lua-c` does.
- `Lost` says what a build by the script lacks against `Run`, a sentence
  each: the cache of a compiler, a check of what it made. The pack warns of
  them once, and the script has them in its comments.
- A build whose field has no counterpart in a script - a value sh cannot
  take, a check only tt can make - returns `build.NoExportf(field, ...)`, an
  error wrapping `build.ErrNoExport` and the `build.FieldError` of the
  field. The package then builds with tt alone, and the pack names the
  field from the manifest. Any other error of `Export` fails the pack.

The core refuses a build of a backend that says `Exports` and has no
`Export`, and an `Export` of a backend that does not say it, as it refuses
an optional method of another signature: what `Exports` says is what the
builds do.

The script sees the variables of a build of tt, by the same names and of the
same meaning, and some of its own:

| Variable | |
| --- | --- |
| `TT_PROJECT_ROOT`, `TT_PACKAGE`, `TT_VERSION` | as in tt |
| `TT_PLATFORM_OS`, `TT_PLATFORM_ARCH` | the machine that builds, by `uname`, as tt names a platform: `linux`, `darwin`; `amd64`, `arm64` |
| `TT_BUILD_JOBS` | one a CPU |
| `TT_OUTPUT_DIR`, `TT_COMPONENT_NAME`, `TT_BUILD_DIR` | as in tt, of a component, not of a hook; `TT_BUILD_DIR` is empty when the build begins |
| `TT_TARANTOOL_INCLUDE_DIR` | `LUA_INCDIR` of LuaRocks, the headers of Tarantool, when `module.h` is there |
| `TT_TARANTOOL_PREFIX` | the prefix of `LUA_INCDIR` when it ends with `/include/tarantool`; unset otherwise |
| `TT_SHARE_DIR`, `TT_PROGRAMS_DIR`, `TT_LINK_DIR` | of the script alone, of a component: `Dirs.Share`, `Dirs.Programs`, `Dirs.Link`, created before it runs |
| `TT_STATE_DIR`, `TT_LINK_ROOT` | of the script alone: `Dirs.State`, and the directory of the `TT_LINK_DIR` of every component, so that `"$TT_LINK_ROOT/<component>"` is `Dirs.LinkOf` |
| `TT_MODULE_EXT`, `TT_MODULE_LDFLAGS` | of the script alone: `.so`, `.dylib` on macOS, as `Target.ModuleExt` says, and what tt links a module with besides `LIBFLAG`, `-Wl,-Bsymbolic-functions` on Linux |
| `CC`, `CFLAGS`, `LIBFLAG` | the toolchain of LuaRocks; `LIBFLAG` links a module, `-shared`, or `-bundle -undefined dynamic_lookup -all_load` on macOS |
| `CXX`, `CXXFLAGS`, `LDFLAGS` | when the environment has them |

The `env` of the block is there too, and the variables above win over it, as
those of tt do. Under `set -u` a script reads a variable that may be unset
as `"${CXX-}"`.

`buildtest.CheckExport` checks that the script makes what `Run` makes: it
runs the build on two harnesses of one project, `Run` on one and the script
on the other, copies what `output` lists after either for a backend of
`OutputCwd`, and compares what each made in `Modules`, `Share`, `Programs`
and `Link`: by path, by whether it is executable, and by its bytes but for
a native file, which two compiles make of other bytes. `setup` prepares
each harness alike. A tool of the export that is not in `PATH` skips the
check. Add to `backend_test.go`:

```go
func TestExport(t *testing.T) {
	block := buildtest.Block{Fields: map[string]any{
		"template": "defaults.lua.in", "vars": map[string]any{"GREETING": "it's $HOME"},
	}}

	export := buildtest.CheckExport(t, template.Backend(), component, block,
		func(h *buildtest.Harness) {
			require.NoError(t, os.WriteFile(filepath.Join(h.Context.Dirs.Cwd, "defaults.lua.in"),
				[]byte("return { greeting = [[${GREETING}]], package = '${TT_PACKAGE}' }\n"),
				0o600))
		})
	assert.Equal(t, `GREETING='it'\''s $HOME' envsubst < defaults.lua.in > `+
		`"$TT_SHARE_DIR"/defaults.lua`, export.Script)
	assert.Equal(t, []string{"envsubst"}, export.Tools)
}

func TestExportOfTtAlone(t *testing.T) {
	bld, err := buildtest.Validate(template.Backend(), component, buildtest.Block{
		Fields: map[string]any{"template": "a.in", "vars": map[string]any{"NOT-SH": "x"}},
	})
	require.NoError(t, err)

	_, err = buildtest.New(t, template.Backend(), component).Export(bld)
	require.ErrorIs(t, err, build.ErrNoExport)
	assert.EqualError(t, err, `the build is made with tt alone: vars.NOT-SH: sh sets no `+
		`variable of the name "NOT-SH"`)
}
```

`harness.Export(bld)` asks a build for its export as the pack does, and
`harness.RunExport(ctx, export)` runs its script as the script of a rock
runs it; `buildtest.CheckExportHook` checks the export of a hook, by the
files of the project.

`myapp` of [Use it in a package](#use-it-in-a-package), with
`repository = "https://github.com/example/myapp"` in its `[package]`, a
`.gitignore` of `.rocks/` and `_build/`, and its files committed at the tag
`v1.0.0`, packs a source rock LuaRocks builds with no tt and no manifest:

```console
$ ../dist/tt package pack --format rock-src --out ../dist-src
.../dist-src/myapp-1.0.0-1.src.rock
.../dist-src/myapp-1.0.0-1.rockspec
$ sed -n '/^# The script of the export/,/^}/p' ../dist-src/myapp-1.0.0-1.rockspec
# The script of the export of the tt backend "template" for the component "config".
tt_export_1() {
PORT=3301 envsubst < defaults.lua.in > "$TT_SHARE_DIR"/defaults.lua
}
$ cd .. && mkdir lr && cd lr
$ ../dist/tt rocks install myapp --only-server ../dist-src
Installing ../dist-src/myapp-1.0.0-1.src.rock

myapp 1.0.0-1 depends on tarantool >= 3.0.0 (3.9.0-1 provided by VM)
CC='gcc' CFLAGS='-O2 -fPIC' LIBFLAG='-shared' LUA_INCDIR='.../include/tarantool' sh ./tt-rock.sh build
LUADIR='.../lr/.rocks/share/tarantool/rocks/myapp/1.0.0-1/lua' LIBDIR='.../lr/.rocks/share/tarantool/rocks/myapp/1.0.0-1/lib' BINDIR='.../lr/.rocks/share/tarantool/rocks/myapp/1.0.0-1/bin' sh ./tt-rock.sh install
No existing manifest. Attempting to rebuild...
myapp 1.0.0-1 is now installed in .../lr/.rocks

$ cat .rocks/share/tarantool/myapp/defaults.lua
return { port = 3301, package = 'myapp', version = '1.0.0' }
```

With `vars = { PORT = "3301", "NOT-SH" = "x" }`, which tt builds and sh
cannot, the source rock builds with tt only, and the pack says why:

```text
⚠ the source rock builds with tt only, which builds it from its manifest: the component "config" is built by the tt backend "template" with tt alone, for components.config.build.vars.NOT-SH: sh sets no variable of the name "NOT-SH"; LuaRocks, tt rocks included, fails to build it, saying so
```

## Check the files of a package

A module may check the files of every package tt builds, whatever backend
made them. A `build.FileCheck` names a suffix. Every build gives its
`Check` each file of the package whose name ends with it: the files the
components lay out, and the data files their builds make in `Share`, those
of a skipped build included. Modules and programs are not given to it: the
core checks them itself.

`template` may render JSON, and a variable that is not set leaves the JSON
broken. `module.go` holds the constructor of the module and a check of the
`.json` files:

```go
package template

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/build"
)

// New is the constructor of the module: the template and render backends
// and the check of the .json files of a package, and no command.
func New(sdk.Services) sdk.Module {
	return sdk.Module{
		Backends:   []build.Backend{Backend(), RenderBackend()},
		FileChecks: []build.FileCheck{{Suffix: ".json", Check: checkJSON}},
	}
}

// checkJSON checks a .json file of a package, whatever made it: an empty
// one is a warning, one that is not JSON fails the build.
func checkJSON(_ context.Context, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	switch {
	case len(data) == 0:
		return path + " is empty", nil
	case !json.Valid(data):
		return "", fmt.Errorf("%s is not valid JSON", path)
	default:
		return "", nil
	}
}
```

`Check` gets the context of the build, for a tool it runs, and the absolute
path of the file in the stage of the package. A warning is shown and the
build goes on. An error fails the build with exit code 1, and the warnings
of the other files are not shown. Both are shown as they are, so they name
the file:

```text
⨯ /home/me/myapp/_build/rock/default/lua/myapp/settings.json is not valid JSON
```

The standard module `wasm` checks the `.wasm` files so
(`modules/wasm/filecheck.go`). It has a check and commands, and no backend.

## Test it

`sdk/build/buildtest` checks a block and runs a build as the core does.
`backend_test.go`:

```go
package template_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	template "example.com/tt-template"
	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// component is the component the tests build.
var component = build.Component{Name: "config", Path: "config", Namespace: "app", Package: "app"}

func TestBuild(t *testing.T) {
	bld, err := buildtest.Validate(template.Backend(), component, buildtest.Block{
		Fields: map[string]any{
			"template": "defaults.lua.in", "vars": map[string]any{"PORT": "3301"},
		},
	})
	require.NoError(t, err)

	harness := buildtest.New(t, template.Backend(), component)
	require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Cwd, "defaults.lua.in"),
		[]byte("return { port = ${PORT}, package = '${TT_PACKAGE}' }\n"), 0o600))
	require.NoError(t, harness.Run(t.Context(), bld))

	data, err := os.ReadFile(filepath.Join(harness.Context.Dirs.Share, "defaults.lua"))
	require.NoError(t, err)
	assert.Equal(t, "return { port = 3301, package = 'app' }\n", string(data))
}

func TestBlockErrors(t *testing.T) {
	for _, tc := range []struct {
		fields map[string]any
		want   string
	}{
		{map[string]any{}, `template is required for backend "template"`},
		{map[string]any{"template": "defaults.lua"},
			`template: "defaults.lua" does not end with .in`},
		{map[string]any{"template": "a.in", "var": "x"}, `var: unknown field`},
		{map[string]any{"template": "a.in", "vars": map[string]any{"PORT": int64(3301)}},
			`vars.PORT: must be a string, not an integer`},
		{map[string]any{"template": "a.in", "vars": map[string]any{"TT_PACKAGE": "x"}},
			`vars.TT_PACKAGE: the TT_* variables are tt's`},
	} {
		_, err := buildtest.Validate(template.Backend(), component,
			buildtest.Block{Fields: tc.fields})
		assert.EqualError(t, err, tc.want)
	}
}

func TestNoEnvsubst(t *testing.T) {
	bld, err := buildtest.Validate(template.Backend(), component, buildtest.Block{
		Fields: map[string]any{"template": "defaults.lua.in"},
	})
	require.NoError(t, err)

	harness := buildtest.New(t, template.Backend(), component)
	harness.Context.Env["PATH"] = t.TempDir()

	require.ErrorIs(t, harness.Run(t.Context(), bld), build.ErrNoTool)
}

func TestCheckJSON(t *testing.T) {
	checks := template.New(nil).FileChecks
	require.Len(t, checks, 1)

	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(good, []byte(`{"port": 3301}`), 0o600))
	require.NoError(t, os.WriteFile(bad, []byte(`{"port": }`), 0o600))

	warning, err := checks[0].Check(t.Context(), good)
	require.NoError(t, err)
	assert.Empty(t, warning)

	_, err = checks[0].Check(t.Context(), bad)
	require.EqualError(t, err, bad+" is not valid JSON")
}
```

```console
$ go mod tidy
$ go test ./...
ok  	example.com/tt-template	0.010s
```

- `buildtest.Block` is a build block as a test writes it: `Common`,
  `Incremental`, and `Fields`, the fields of the backend as a TOML decoder
  makes them - strings, `int64`, `float64`, `bool`, `[]any` and
  `map[string]any`.
- `buildtest.Validate` checks the backend as the core checks it before it
  adds it, with the same function, `build.Backend.Validate`: what the core
  refuses (see [What the core refuses](#what-the-core-refuses)) fails the
  test. Then it checks the block in the core's order and returns the build
  or the first error, a `*build.FieldError` for a field. The tt of a test
  has no other backend, so a key the configuration has no place for is an
  `unknown field` here, where the core may say that another backend has it.
- `buildtest.New(t, backend, component)` makes a `Context` for the builds
  of `backend` on directories of `t.TempDir()`:
  `<tmp>/project` with the component in it, and `work`, `lib`, `share`,
  `bin` and `state` beside it. The target is the machine the test runs on,
  and no Tarantool is known. `Env` has the
  `TT_*` variables of the core: `TT_OUTPUT_DIR`, `TT_PROJECT_ROOT`,
  `TT_PACKAGE`, `TT_COMPONENT_NAME`, `TT_VERSION` (`buildtest.Version`,
  `0.0.0-dev.0`), `TT_PLATFORM_OS`, `TT_PLATFORM_ARCH`, `TT_BUILD_DIR` and
  `TT_BUILD_JOBS`, the number of CPUs.
  A test may change the Context before it runs the build: `TestNoEnvsubst`
  empties `PATH`, `Env["TT_VERSION"]` sets another version,
  `harness.SetTarantool` tells the build a Tarantool as the core does, in
  `Tarantool` and in `TT_TARANTOOL_PREFIX` and `TT_TARANTOOL_INCLUDE_DIR`,
  `harness.SetJobs(n)` the jobs of `-j n`, in `Jobs` and in
  `TT_BUILD_JOBS`, and `harness.SetTarget(target)` the platform of
  `--target`, in `Target` and in `TT_PLATFORM_OS` and `TT_PLATFORM_ARCH`
  (see [Other platforms](#other-platforms)).
- `harness.Run` runs `Preflight`, when the build has one, with `TT_VERSION`
  empty as the core has it there, then `Run`, and
  fails, as the core does, when `Run` left a file in the directory of a
  type the backend does not produce. It does not copy the files of
  `output`, which the core does for `build.OutputCwd`.
  `Printed` returns what the commands printed, `Warnings` the warnings of
  the build, and `Locked` sets the versions `LockedVersion` answers.
- `buildtest.ValidateHook` and `buildtest.NewHook(t, backend)` do the same
  for a backend that runs hooks.

## Build it into tt

Next to the module and a checkout of tt, `dist/tt.manifest.toml`:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[core]
path = "../tt"   # a checkout of tt whose SDK has sdk/build

[dependencies]
template = "../tt-template"
```

```console
$ cd dist
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3 (path ../tt)
   •   standard aeon, build, coredump, emmylua, wasm (with the core)
   •   template example.com/tt-template (path ../tt-template)
   • Building tt
   • Built /home/me/dist/tt
/home/me/dist/tt
$ ./tt version --modules
Tarantool CLI version 3.0.0-alpha.2, linux/amd64. commit: 2f4ff650 (v3.0.0-alpha.2-187-g2f4ff650)
  aeon      (devel)  github.com/tarantool/tt/modules/aeon
  build     (devel)  github.com/tarantool/tt/modules/build
  builtin   (devel)  github.com/tarantool/tt/v3
  coredump  (devel)  github.com/tarantool/tt/modules/coredump
  emmylua   (devel)  github.com/tarantool/tt/modules/emmylua
  template  (devel)  example.com/tt-template
  wasm      (devel)  github.com/tarantool/tt/modules/wasm
```

`[core]` may name a commit instead, `tt = "<commit>"`. The distribution
keeps the standard modules, `build` among them, so it has tt's backends and
`template`. Every field of the manifest is in
[tt.manifest.toml](../reference/tt-manifest.md).

## Use it in a package

`myapp/app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "myapp"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[components.config]
path = "config"

[components.config.build]
backend = "template"
template = "defaults.lua.in"
vars = { PORT = "3301" }
incremental = true
```

`myapp/config/defaults.lua.in`:

```lua
return { port = ${PORT}, package = '${TT_PACKAGE}', version = '${TT_VERSION}' }
```

```console
$ cd myapp
$ ../dist/tt package build
$ cat .rocks/share/tarantool/myapp/defaults.lua
return { port = 3301, package = 'myapp', version = '0.0.0-dev.0' }
$ ../dist/tt run -e "print(require('myapp.defaults').port)"
3301
```

The template itself is not laid out: the default `include` of a component
takes `*.lua` and `*.so` only.

A tt without the module warns of the block for a command that builds
nothing, such as `tt package resolve`, and fails a build, with exit code 1:

```text
⚠ components.config.build: no module of this tt provides backend "template"; its fields are not checked
⨯ building component "config": no module of this tt provides backend "template"
```

## What the core refuses

The core registers the backends and the file checks of every module when tt
starts. It refuses those that are not well formed, naming the module:

- a `Name` that is not a lowercase letter, then lowercase letters, digits
  and dashes, and a name another module provides too: the modules are added
  in the order of their names, and the later one is refused;
- `Produces` that is empty, or names a type that is not `module`, `data`,
  `program` or `link`, or one twice;
- an `OutputFrom` that is none of the three, and an empty pattern in
  `Exclude`;
- no `New`; no `Config`, or a `Config` that does not return a pointer to a
  struct, or whose struct has a field the decoder cannot fill, two fields of
  one key, or a field of a common key such as `env`;
- a `FileCheck` with no `Suffix` or no `Check`.

`build.Backend.Validate` and `build.FileCheck.Validate` are these checks
but the one of a name another module provides; the core and `buildtest`
both call them, so a backend whose tests pass is one the core takes.

Every refusal is reported at once, and the binary then fails on every
command line. `tt binary build tt` runs `tt version` of the binary it built,
so such a distribution fails to build:

```text
⨯ the built binary does not start: .../tt version: exit status 1
  ⨯ module "bad": invalid build backend "Tmpl": the name must match [a-z][a-z0-9-]*
       module "bad": invalid build backend "noprod": the Produces must name module, data, program or link, once each
       module "bad": invalid build backend "envfield": env is a field of every build block, not one of a backend
       module "bad": invalid build backend "nonew": no New
       module "build": invalid build backend "make": module "bad" provides one of the name too
```

Here the module `bad` provides a backend named `make`. It comes before
`build` by name, so the `make` of `build` is the one refused.

In a manifest, with exit code 1, before anything is built: a field of
another backend, a field no backend has, a value of a wrong type, an error
of `New`, a build of an optional method the core would never call, a build
that is no `build.Exporter` of a backend that says `Exports` and one that is
of a backend that does not, `output` or `incremental` of a backend that
does not allow them, an
`output` entry or a `Producer` type the backend does not produce, and a
hook that names a backend without `Hook`. Before anything is built, with
exit code 1: a build for another platform of a backend without `Cross`, and
a `Preflight` that refuses the target, wrapping `build.ErrTarget`. During
the build, with exit code 2: a failed `Preflight` or `Run`, a file of a
type the backend does not produce, a module or a program that is not one
of the target, and a module or a program that is not static under
`static = true`.

## The backends of tt

The backends of the standard module `build` show the rest of the contract:

| Package | Backends | Shows |
| --- | --- | --- |
| `modules/build/basic` | `make`, `shell` | `OutputCwd`, `Hook`, a `Producer` and a `ProgramNamer` from `output`, `Cross` left to the script, `Exports` of the command it runs, checked by `CheckExport` in `modules/build/export_test.go` |
| `modules/build/cc` | `c`, `lua-c` | C and C++ compilers driven against the `Tarantool` of the Context, `platforms.<os>` fields, commands run at once, a cache of its own in `Work` that honours `Fresh`, a `Linker` reading `Dirs.LinkOf`, modules beside the module it makes, `Exports` of the compiles and the link of `Run` with the toolchain of LuaRocks, with `NoExportf` for `static` and `bundle` |
| `modules/build/cargo` | `cargo` | `Exclude`, `Component.Wants`, a module, a program or both, a `Vendorer` and a build from `Vendored`, `Cross` with a linker of the target, `Exports` that finds what cargo built by its place in a target directory of its own, and says what it lacks in `Lost` |
| `modules/build/cmake` | `cmake` | `OutputBackend`, a build tree in `Work`, `Fresh`, `Exports` that copies `output` itself |
| `modules/build/gobuild` | `go` | `NoSourceLayout`, `static`, `Cross` with a `Preflight` that refuses `cgo`, a `Vendorer` and a build from `Vendored`, `Exports` for the machine |
| `modules/build/zig` | `zig` | `Incremental` with an `InputHasher`, `Cross` with `-Dtarget`, a `Vendorer` and a build from `Vendored`, `Exports` that asks the tool its version as `Run` does |

Their fields are in
[the reference of the application manifest](../reference/app-manifest.md#componentsnamebuild).
