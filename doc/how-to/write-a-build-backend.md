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

To build on a checkout of tt instead, put its SDK modules in a Go
workspace. Keep `go.work` out of git:

```sh
go work init . ../tt/sdk ../tt/sdk/build
go test ./...
```

`go mod tidy` ignores `go.work`, so it needs the requirements of the
`go get` above. The distribution build ignores `go.work` and builds the
module on the SDK of its core, whatever the `go.mod` of the module requires
(see [Write a tt module](write-a-module.md#develop-against-a-checkout-of-tt)).

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
		New:         newBuild,
		Incremental: true,
		OutputFrom:  build.OutputNone,
	}
}

// newBuild checks a template build block.
func newBuild(cfg any, _ build.Component, _ build.Common) (build.Build, error) {
	config, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
	}

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
| `Produces` | the types of what its builds make, each once: `build.Module`, `build.Data`, `build.Program` |
| `Config` | returns a fresh, empty configuration: a pointer to a struct of the fields of the block |
| `New` | checks a block and returns the build |
| `Incremental` | allows `incremental = true` in the block |
| `OutputFrom` | whether the block has `output`, and who copies what it lists |
| `Exclude` | gitignore patterns of files the backend's tool leaves in the component, which are never laid out: `/target/` of `cargo` |
| `NoSourceLayout` | switches off the default `include` of the component, the Lua sources and the prebuilt modules: for a backend whose sources are no files of the package, as `go` |
| `Hook` | lets a lifecycle hook, `[hooks.pre_build]` or `[hooks.post_build]`, name the backend |

The core calls `Config` and `New` whenever it checks a manifest: every
command that reads the manifest does, `tt package add` included, and it may
call them more than once for one block. Neither may have side effects.
`New` looks at the block only; a tool the build needs is looked for later,
in `Preflight`.

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
It returns the build, or an error.

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
project, its `Namespace` and its `Package`. `Expects` is what the deprecated
`kind` of a component of `manifest_version = "0.1"` says the build makes,
and nil when there is no `kind`; a backend that picks what to build when the
block does not say may follow it, as `cargo` does. `build.Common` has `Cwd`,
`Env`, `Output` and `Static` of the block.

## Run the build

`Run` builds the component in a `build.Context`. The core makes one for each
component and build.

| Field | |
| --- | --- |
| `Component` | the component |
| `Target` | the platform, as `GOOS` and `GOARCH` name it: always the machine tt runs on; `ModuleExt` is `.so`, or `.dylib` on darwin |
| `Dirs` | the directories of the build, see below |
| `Tarantool` | the Tarantool the component is built against: `Prefix`, `IncludeDir`, `Version`, each empty when tt does not know it |
| `Toolchain` | how tt compiles a C module: `CC`, `LibFlag`, `Rpath`, `IncludeDir` |
| `Env` | the `env` of the block and the `TT_*` variables of the build |
| `Fresh` | set by `tt package build --rebuild`: drop what `Work` keeps and build from scratch |
| `Locked` | set by `--locked`: a tool of the build must not move a version either; `LockedVersion` returns the version the lock pins a rock at |
| `Static` | `static` of the block |
| `Hook` | set for the build of a lifecycle hook |
| `Log`, `Warn` | the logger of the build, tt's with `backend=<name>`, whose debug records `tt -V` shows, and a function that shows a warning |

The directories, all absolute, for a component of the package:

| Directory | |
| --- | --- |
| `Project` | the root of the project: the package, or a path dependency or a source rock being built |
| `Component` | the directory of the component |
| `Cwd` | where the build runs: `cwd` of the block, relative to the project, or `Component` |
| `Work` | `_build/component/<name>/`, `TT_BUILD_DIR`: intermediate files, kept between builds, emptied by `--rebuild` |
| `Output` | where modules go: `_build/rock/<product>/lib/<namespace>/`, `TT_OUTPUT_DIR` |
| `Share` | where data files go: `_build/rock/<product>/lua/<namespace>/` |
| `Programs` | where programs go: `_build/rock/<product>/bin/`, shared by the components |
| `State` | `_build/`, the state of the build of the project |
| `Tools` | `.tarantool/bin/` of the project |
| `Tree` | `.rocks/`, the rocks tree the dependencies are installed into |

The core creates `Work`, `Output`, `Share` and `Programs` before `Run`.

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
`TT_PLATFORM_OS`, `TT_PLATFORM_ARCH`, and `TT_TARANTOOL_PREFIX` and
`TT_TARANTOOL_INCLUDE_DIR` when tt knows them. A template of the example
can use them: `${TT_PACKAGE}`, `${TT_VERSION}`. `envsubst` replaces every
variable it is given, tt's environment included, and a variable that is
not set with nothing.

### Results

What a build makes has a type. The type says where it goes:

| Result | The build puts it into | It is installed into |
| --- | --- | --- |
| `build.Module`, a compiled module Tarantool loads | `Dirs.Output` | `.rocks/lib/tarantool/<namespace>/` |
| `build.Data`, a file beside the Lua sources, such as `.wasm` or a generated Lua file | `Dirs.Share` | `.rocks/share/tarantool/<namespace>/` |
| `build.Program`, a program the instances run | `Dirs.Programs` | `.rocks/bin/`, where `tt instance` finds it |

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
directories with `build.CopyFile`. It writes the copy with no fork of tt in
between, so that a program it copies can be started at once: Linux refuses
to run a file that is open for writing, and a process tt forks while it
writes holds the file open until it runs its own program.

An error of `Run` fails the build with exit code 2:

```text
⨯ building component "config": open /home/me/myapp/config/missing.lua.in: no such file or directory
```

## Output: lib, share and bin

`output` of a build block lists files the build makes. An entry is a path,
which is a module, or a table with `path` and `to`: `to = "lib"` is a
module, `to = "share"` a data file, `to = "bin"` a program. The core gives
them to `New` in `Common.Output`, each a `build.OutputFile` with its `Path`
and its type, `To`. `OutputFrom` says whether the backend has the field:

- `build.OutputNone`: no `output`. The build puts what it makes into the
  directories of the Context itself, as `template`, `cargo` and `zig` do. A
  block with `output` is refused:
  `components.config.build.output: is a field of backend make, shell or cmake, not of "template"`.
- `build.OutputCwd`: the core copies each file `output` lists, relative to
  `Cwd`, into the directory of its type under its base name, after `Run`
  succeeds. `make` and `shell` work so (`modules/build/basic`).
- `build.OutputBackend`: the backend reads `Common.Output` and copies the
  files itself, from where it built them. `cmake` copies them from its
  build tree (`modules/build/cmake`).

The core refuses an entry whose `to` is not among `Produces`, before
anything is built.

## More of the build

A build may implement more interfaces. The core asks for each with a type
assertion, so a method of another signature would never be called: the
core refuses a build whose `Preflight`, `Produces`, `Programs` or `Inputs`
is not the method of the interface, or is a method of a pointer while
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
is empty. `ctx` is that of the build: make the commands `Preflight` runs
with it, so that they stop when the build is cancelled. Wrap
`build.ErrNoTool` for a tool that is not in `PATH`, so that
a missing tool is told from a failed build. A failed `Preflight` fails the
build with exit code 2:

```text
⨯ building component "config": build tool not found: the template backend runs envsubst, which is not in PATH
```

The backends of tt that have a `Preflight` call it at the start of `Run`
too. The other interfaces:

- `build.Producer`: `Produces()` says what this block asks for, each type
  among `Backend.Produces`. The core checks it against the deprecated `kind`
  before anything is built. `cargo` and `zig` make a module, a program or
  both, as `module` and `bin` say.
- `build.ProgramNamer`: `Programs()` names the programs the build makes, and
  says whether the list is exhaustive. The core checks with it that no two
  components make one program, and `tt instance` finds by it the component
  that makes a program. When the list is not exhaustive, the core adds what
  the build wrote into `Programs`.
- `build.InputHasher`: see the next section.

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

## Lifecycle hooks

`[hooks.pre_build]` and `[hooks.post_build]` run around the builds of all
the components. A hook names a backend with `Hook: true`, `make` or `shell`
in tt; another is refused,
`hooks.pre_build: backend "template" is not supported (want make or shell)`.
The core checks the block of a hook as it checks a build block, `New` given
the zero `Component`. The build runs in a Context whose `Hook` is set. It
has no component, and `Dirs` has no `Component`, `Work`, `Output`, `Share`
and `Programs`. `Env` has no `TT_OUTPUT_DIR`, `TT_COMPONENT_NAME` and
`TT_BUILD_DIR`, and the files `output` lists are not copied.

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

// New is the constructor of the module: the template backend and the check
// of the .json files of a package, and no command.
func New(sdk.Services) sdk.Module {
	return sdk.Module{
		Backends:   []build.Backend{Backend()},
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
build goes on. An error fails the build with exit
code 1, and the warnings of the other files are not shown. Both are shown as
they are, so they name the file:

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
		Rest: map[string]any{
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
		rest map[string]any
		want string
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
		_, err := buildtest.Validate(template.Backend(), component, buildtest.Block{Rest: tc.rest})
		assert.EqualError(t, err, tc.want)
	}
}

func TestNoEnvsubst(t *testing.T) {
	bld, err := buildtest.Validate(template.Backend(), component, buildtest.Block{
		Rest: map[string]any{"template": "defaults.lua.in"},
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
  `Incremental`, and `Rest`, the fields of the backend as a TOML decoder
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
  `<tmp>/project` with the component in it, and `work`, `output`, `share`,
  `bin` and `state` beside it. The target is the machine the test runs on,
  the toolchain is `cc` or `CC`, and no Tarantool is known. `Env` has the
  `TT_*` variables of the core: `TT_OUTPUT_DIR`, `TT_PROJECT_ROOT`,
  `TT_PACKAGE`, `TT_COMPONENT_NAME`, `TT_VERSION` (`buildtest.Version`,
  `0.0.0-dev.0`), `TT_PLATFORM_OS`, `TT_PLATFORM_ARCH` and `TT_BUILD_DIR`.
  A test may change the Context before it runs the build: `TestNoEnvsubst`
  empties `PATH`, `Env["TT_VERSION"]` sets another version, and
  `harness.SetTarantool` tells the build a Tarantool as the core does, in
  `Tarantool`, in `Toolchain.IncludeDir` and in `TT_TARANTOOL_PREFIX` and
  `TT_TARANTOOL_INCLUDE_DIR`.
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
   •   standard aeon, build, emmylua, wasm (with the core)
   •   template example.com/tt-template (path ../tt-template)
   • Building tt
   • Built /home/me/dist/tt
/home/me/dist/tt
$ ./tt version --modules
Tarantool CLI version 3.0.0-alpha.2, linux/amd64. commit: 2f4ff650 (v3.0.0-alpha.2-187-g2f4ff650)
  aeon      (devel)  github.com/tarantool/tt/modules/aeon
  build     (devel)  github.com/tarantool/tt/modules/build
  builtin   (devel)  github.com/tarantool/tt/v3
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
- `Produces` that is empty, or names a type that is not `module`, `data` or
  `program`, or one twice;
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
       module "bad": invalid build backend "noprod": the Produces must name module, data or program, once each
       module "bad": invalid build backend "envfield": env is a field of every build block, not one of a backend
       module "bad": invalid build backend "nonew": no New
       module "build": invalid build backend "make": module "bad" provides one of the name too
```

Here the module `bad` provides a backend named `make`. It comes before
`build` by name, so the `make` of `build` is the one refused.

In a manifest, with exit code 1, before anything is built: a field of
another backend, a field no backend has, a value of a wrong type, an error
of `New`, a build of an optional method the core would never call,
`output` or `incremental` of a backend that does not allow them, an
`output` entry or a `Producer` type the backend does not produce, and a
hook that names a backend without `Hook`. During the build, with exit code
2: a failed `Preflight` or `Run`, a file of a type the backend does not
produce, a program that is not a program of the target, and a module or a
program that is not static under `static = true`.

## The backends of tt

The backends of the standard module `build` show the rest of the contract:

| Package | Backends | Shows |
| --- | --- | --- |
| `modules/build/basic` | `make`, `shell` | `OutputCwd`, `Hook`, a `Producer` and a `ProgramNamer` from `output` |
| `modules/build/cc` | `c`, `lua-c` | the `Toolchain` and the `Tarantool` of the Context, `platforms.<os>` fields |
| `modules/build/cargo` | `cargo` | `Exclude`, `Component.Expects`, a module, a program or both |
| `modules/build/cmake` | `cmake` | `OutputBackend`, a build tree in `Work`, `Fresh` |
| `modules/build/gobuild` | `go` | `NoSourceLayout`, `static` |
| `modules/build/zig` | `zig` | `Incremental` with an `InputHasher` |

Their fields are in
[the reference of the application manifest](../reference/app-manifest.md#componentsnamebuild).
