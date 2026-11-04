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
