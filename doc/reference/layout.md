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
│   ├── tarantool/tarantool_3.8.1/        # tarantool, include/tarantool/, share/tarantool/, LICENSE, AUTHORS
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
                                          # tarantool.sock, process.pid, launch.json,
                                          # watchdog.json, last_exit.json,
                                          # crashes.json
```

| File | |
| --- | --- |
| `run/.../tt.pid` | the pid of the watchdog of the instance, the `tt` that runs its process |
| `run/.../launch.json` | what the watchdog runs, as `tt instance start` resolved it, while the watchdog runs |
| `run/.../watchdog.json` | the state of the watchdog, while it runs: its pid, the pid of the process it runs and when that started, the count of restarts and the last integrity check, for `tt instance status` |
| `run/.../last_exit.json` | how the last process of the instance ended and when, left by its watchdog for `tt instance status --details` |
| `run/.../crashes.json` | the last 10 exits of the process by a signal, with the PID, the signal and whether a core was dumped, kept by the watchdogs across restarts and stops for `tt instance status --details` and `tt coredump list` |
| `run/.../tarantool.pid` | the pid file Tarantool writes, once `box.cfg` runs, unless the cluster config moves it with `process.pid_file` |
| `run/.../tarantool.control` | the console socket `tt connect <app>:<instance>` uses |
| `run/.../tarantool.sock` | the binary port, unless the cluster config or the script listens elsewhere |
| `run/.../process.pid` | the pid of an external process, and of TCM |
| `log/.../tt.log` | everything the instance writes and the messages of its watchdog, which names the process by its PID at every start and exit |

A monitor watches the process, not `tt.pid`: see
[Which pid file to watch](#which-pid-file-to-watch).

TCM, the record with `kind: tcm` (see [Run TCM](../how-to/tcm.md)), has a
few files of its own:

| File | |
| --- | --- |
| `run/<app>/<tcm>/tcm.yaml` | the config of TCM, written anew by every `tt instance start`, mode 0600 |
| `lib/<app>/<tcm>/admin.secret` | the password of the first login of `admin`, generated on the first start, mode 0600 |
| `lib/<app>/<tcm>/etcd/` | the data of the embedded etcd of TCM, for an application without a config storage |
| `lib/<app>/<tcm>/embedded-etcd.json` | the ports tt chose for that etcd |

Nothing of `var/`, or of the rest of `.tarantool/`, goes into a package:
`tt package pack` refuses an `include` entry in `.tarantool/`.

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

## Which pid file to watch

The PID tt shows and prints for an instance is the pid of its process -
tarantool, the program of an external process or TCM - the one `top`,
`gdb`, `perf` and `box.info.pid` name. `tt.pid` is not that process: it
names the watchdog, which runs the process and starts it again as the
`restart` policy says, and lives on while it does. A monitor that watches a
pid file - monit, process-exporter, the `proc.*` items of Zabbix - measures
the watchdog when it is pointed at `tt.pid`. Point it at the process:

- `watchdog.json` names the process of any kind of instance, as
  `.process.pid`, `null` while none runs, as during the delay before a
  restart. It is there only while the watchdog runs:

  ```console
  $ jq .process.pid .tarantool/var/run/demo/demo/watchdog.json
  3598530
  $ kill -9 3598530
  $ jq .process.pid .tarantool/var/run/demo/demo/watchdog.json
  null
  ```

- `tarantool.pid` is the pid file of Tarantool itself, which an instance of
  the cluster config writes once `box.cfg` runs. A config that sets
  `process.pid_file` moves it there, and the run directory has none then;
  nor does it for an instance that runs its script.
- `process.pid` names an external process and TCM.

`tt instance status` shows the same PID, and the messages of
`tt instance start`, `stop`, `kill` and `logrotate` name an instance by it,
or by its watchdog, `(watchdog PID = 3598516)`, while the process is not
known. Under systemd the main process of the unit is neither: see
[Run instances under systemd](../how-to/systemd.md#the-processes-of-a-unit).

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
│   └── bin/                   # programs the components built
├── _build/pack/               # archives tt package pack makes (generated)
├── _build/rock/<product>/     # the components laid out, installed into .rocks/ (generated)
├── _build/rock/<product>.json # which component put each file there (generated)
├── _build/results.json        # what each component built: modules, data files, programs (generated)
├── _build/cache/              # what the last incremental builds hashed and left (generated)
├── _build/component/<component>/ # TT_BUILD_DIR of each component: a cmake build tree, objects and cache of c and lua-c (generated)
├── _build/cargo/              # the target directory of the cargo components (generated)
├── _build/target/<os>-<arch>/ # the same of a build for another platform, --target, with tree/, its package (generated)
├── _build/tt.lock             # the lock of the project, which tt package clean keeps (generated)
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
_runtime/tcm/bin/tcm           # with [platform].tcm set
```

A signed archive also has `hashes.json`, `env_hashes.json` and their `.sig`
files; see [Sign packages](../how-to/sign-packages.md).

## The user cache

`tt wasm` and `tt emmylua` keep what they download under `tt/` of the user
cache: `$XDG_CACHE_HOME`, `~/.cache` by default, `~/Library/Caches` on
macOS. Outside every project, `tt emmylua check`, `doc`, `fmt` and `ls` run
the programs of the `emmylua` rock from there, and write their
configuration there rather than into the directory (see
[Check and format Lua code with EmmyLua](../how-to/emmylua.md#outside-a-project)):

```text
<user cache>/tt/emmylua/
├── <sha256>                          # an asset tt emmylua rock downloaded
├── programs/<sha256>/                # the bin/ of the rock of that sha256
│   ├── emmylua_check
│   ├── emmylua_doc_cli
│   ├── emmylua_ls
│   └── luafmt
├── resolved/<key>.json               # the rock a server, a version and a platform resolved to
└── config/<key>/emmyrc.json          # the configuration of a directory, by its path
```

The first run fetches the rock and says so; the later ones find it from
`resolved/` and ask no server. A rock server that is a directory is asked
every time. Remove the directory to fetch the rocks anew.
