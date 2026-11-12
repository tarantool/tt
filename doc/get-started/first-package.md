# Your first package

This tutorial writes a small Tarantool application as a package: it
declares a dependency, builds, runs a test, packs the application into an
archive and installs the archive for the current user.

## Create the manifest

```console
$ mkdir hello && cd hello
$ git init
$ tt new
created /home/me/hello/app.manifest.toml for package hello
$ tt binary install tarantool
```

`tt new` writes `app.manifest.toml`, named after the directory. The package
name is lowercase letters, digits and dashes, starting with a letter; give
another one with `-n`. `tt binary install tarantool` gives the project the
Tarantool it builds and runs on.

## Add dependencies

```console
$ tt package add checks '>=3.0.0'
added checks >=3.0.0 to [dependencies]
  checks 3.4.1-1
$ tt package add luatest --dev
added luatest * to [dev_dependencies]
  luatest 1.4.5-1
```

`tt package add` declares a dependency in the manifest and resolves it into
`app.manifest.lock`, which pins the exact versions; commit both. A
`--dev` dependency is for the tests and tools only: it does not go into the
archive.

## Write the code

The code of a package is its components. Declare one for the `hello`
directory:

```toml
[components.app]
path = "hello"
```

`hello/init.lua`:

```lua
local checks = require('checks')

local function greet(name)
    checks('string')
    return 'Hello, ' .. name .. '!'
end

return {greet = greet}
```

## Build and run

```console
$ tt package build
$ tt run -e "print(require('hello').greet('tt'))" - </dev/null
Hello, tt!
```

`tt package build` installs the dependencies into `.rocks/` and lays out the
files of the components under `.rocks/share/tarantool/hello/`, with a
`version.lua`, so the code is `require('hello')`. `tt run` runs the
Tarantool of the project with the arguments as given; without a script it
starts an interactive console.

## Test

`test/hello_test.lua`:

```lua
local t = require('luatest')
local hello = require('hello')

local g = t.group()

g.test_greet = function()
    t.assert_equals(hello.greet('world'), 'Hello, world!')
end
```

```console
$ tt test
.
Ran 1 tests in 0.001 seconds, 1 succeeded, 0 failed
```

`tt test` builds the package and runs luatest over `test/`. Paths relative to
the project narrow the run, as many as you name:
`tt test test/unit test/integration`. Arguments after `--` go to luatest:
`tt test test/hello_test.lua -- --verbose`.

## Describe the instances

`.templates/default/config.yaml` is the cluster config the package runs
from:

```yaml
app:
  module: hello

groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            iproto:
              listen:
              - uri: 127.0.0.1:3311
```

`app.module` loads the code from `.rocks/`, where it is both in the project
and in an installed package. A template of `config.yaml` alone runs every
instance of its topology; `tt instance start` runs it in the project too.

## Pack

Commit the project and tag the version: the version of a package is the one
of its `VERSION` file, or else of the nearest version tag, `v1.0.0` or
`1.0.0`.

```console
$ printf '.rocks/\n.tarantool/\n.env/\n_build/\n' > .gitignore
$ git add -A && git commit -m 'hello 1.0.0' && git tag v1.0.0
$ tt package pack
/home/me/hello/_build/pack/hello-1.0.0-linux-amd64.tt
```

The archive carries the package, its dependencies and, in `_runtime/`, the
Tarantool and the tt it runs on, so it installs with no network. Changes
not committed, untracked files among them, give the version a `+dirty`
suffix.

> **Preview note.** `tt package pack` bundles the tt that runs it, which must
> match `[platform].tt` of the manifest. See the note in
> [Install](install.md#build-from-source) for a tt built from source.

## Install for the user

```console
$ tt package install _build/pack/hello-1.0.0-linux-amd64.tt --scope user
installed hello 1.0.0 into user scope
$ tt package list --scope user
NAME   ACTIVE  VERSIONS  DESCRIPTION
hello  1.0.0   1.0.0
$ tt instance start hello:instance-001 --scope user
$ echo "return require('hello').greet('user scope')" | tt connect hello:instance-001 --scope user -f-
---
- Hello, user scope!
...
$ tt instance stop hello:* --scope user --yes
```

The user scope is `~/.local/share/tarantool` on Linux. The package becomes
the version `versions/hello/hello_1.0.0/` there, and `apps/hello` points to
it; the instances keep their data under `var/` of the scope. See
[Projects and scopes](../concepts/projects-and-scopes.md) and
[Install an application for a user](../how-to/user-scope.md).

## Next

- [An HTTP API in a WebAssembly component](../write-your-app/http-api-in-wasm.md):
  an application with its HTTP server in Python, built into the package.
- [Packages](../concepts/packages.md): the manifest, the lock and the
  archive.
- [`app.manifest.toml`](../reference/app-manifest.md): every field.
