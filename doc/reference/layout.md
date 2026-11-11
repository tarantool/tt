# Filesystem layout

## The root of a scope

Every [scope](../concepts/projects-and-scopes.md#scopes) has a root with the
same layout: `.tarantool/` of a project, the root of the user scope, or of
the system scope.

```text
<root>/
├── bin/                                  # the active binaries (links)
│   ├── tarantool
│   ├── tt
│   └── tcm
├── include/tarantool                     # the headers of the active tarantool (link)
├── versions/
│   ├── tarantool/tarantool_3.8.1/        # tarantool, include/tarantool/
│   ├── tarantool-ee/tarantool-ee_3.8.1/
│   ├── tt/tt_3.0.0/
│   ├── tcm/tcm_1.3.1/
│   ├── tarantool-sdk/                    # which SDK components belong together
│   └── kv-api/kv-api_1.1.0/              # an installed application (user, system)
├── registry/                             # the rock server of tt wasm/emmylua prepare --from (project)
├── apps/
│   └── kv-api -> ../versions/kv-api/kv-api_1.1.0
├── etc/
│   └── kv-api/                           # the pair of the host (user, system)
│       ├── config.yaml
│       ├── instances.yaml
│       └── systemd.yaml                  # the instances systemd units manage
└── var/
    ├── lib/<app>/<instance>/             # data; working directory of an external process
    ├── log/<app>/<instance>/tt.log       # the output of the instance and its watchdog
    └── run/<app>/<instance>/             # tt.pid, tarantool.pid, tarantool.control,
                                          # tarantool.sock, process.pid
```

| File | |
| --- | --- |
| `run/.../tt.pid` | the pid of the watchdog of the instance |
| `run/.../tarantool.pid` | the pid file Tarantool writes |
| `run/.../tarantool.control` | the console socket `tt connect <app>:<instance>` uses |
| `run/.../tarantool.sock` | the binary port, unless the cluster config or the script listens elsewhere |
| `run/.../process.pid` | the pid of an external process |
| `log/.../tt.log` | everything the instance writes and the messages of its watchdog |

`tt wasm prepare` adds to the root of a project what WebAssembly components
are built with: under `versions/`, tarawasm and the tools tt installs, each
linked into `bin/`, and, with `--from`, `registry/`, a rock server holding
the `wasm` rock of a local build for this machine:

```text
.tarantool/
├── bin/
│   ├── tarawasm -> <root>/versions/tarawasm/tarawasm_<commit>/bin/tarawasm
│   ├── wasm-tools -> <root>/versions/wasm-tools/wasm-tools_1.261.0/wasm-tools
│   ├── wkg
│   ├── componentize-py                   # in the environment of tarawasm (Python)
│   ├── tinygo, wasm-opt                  # Go
│   └── wit-bindgen                       # C
├── registry/
│   ├── manifest
│   └── wasm-0.1.0-1.linux-x86_64.rock
└── versions/
    ├── tarawasm/tarawasm_<commit>/       # a Python virtual environment
    ├── wasm-tools/wasm-tools_1.261.0/
    ├── wkg/wkg_0.16.1/
    ├── tinygo/tinygo_0.43.0/             # with Binaryen laid over it
    ├── wasi-sdk/wasi-sdk_34/             # not linked: see tarawasm.local.json below
    └── wit-bindgen/wit-bindgen_0.62.0/
```

## A project

```text
myapp/
├── app.manifest.toml          # the manifest of the package
├── app.manifest.lock          # the versions the dependencies resolved to; commit it
├── VERSION                    # optional: the version of the package
├── .templates/
│   └── default/               # an instance template: the pair of the project
│       ├── config.yaml
│       └── instances.yaml
├── .rocks/                    # dependencies and built components (generated)
│   ├── share/tarantool/<name>/
│   └── bin/                   # programs of executable components
├── _build/pack/               # archives tt package pack makes (generated)
├── _build/rock/<product>/     # the components laid out, installed into .rocks/ (generated)
├── _build/cache/              # what the last incremental builds hashed and left (generated)
├── .env/                      # activate.sh, deactivate.sh (generated)
└── .tarantool/                # the root of the project scope (generated)
```

Keep the generated directories out of git:

```text
.rocks/
_build/
.env/
.tarantool/
```

## A WebAssembly component

`tt wasm new router --lang python` makes a tarawasm project in `router/`:

```text
router/
├── .gitignore                    # what tarawasm generates, written by tt wasm new
├── main.py                       # the source
├── wit/
│   ├── app.wit                   # the world of the component, named after it
│   └── deps/                     # the WIT of the SDK
├── tarawasm.json                 # the language, the world, the output
├── tarawasm.tarantool.lock.json  # the SDK release the component is built with
├── tarawasm.local.json           # where the tools of this machine are (C; generated)
├── .tarawasm/                    # the SDK, restored by tt wasm prepare (generated)
├── wit_world/, poll_loop.py ...  # the bindings (generated)
└── dist/router.wasm              # the component (generated)
```

Commit the source, `wit/` with its `deps/`, `tarawasm.json`,
`tarawasm.tarantool.lock.json` and `.gitignore`; the rest is generated, and
`.gitignore` keeps it out.

## An archive

`tt package pack` makes `<name>-<version>-<os>-<arch>.tt`, a tar.zst
archive:

```text
app.manifest.toml
app.manifest.lock
VERSION
.templates/...
.rocks/...
_runtime/tarantool/bin/tarantool
_runtime/tt/bin/tt
```

A signed archive also has `hashes.json`, `env_hashes.json` and their `.sig`
files; see [Sign packages](../how-to/sign-packages.md).
