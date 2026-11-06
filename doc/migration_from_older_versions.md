# Migration from older versions

This file contains information on how to migrate existing projects from
one `tt` version to a newer one. Additionally to migration hints, it
contains breaking changes descriptions.

## Contents

- [2.x -> 3.0.0](#2x---300)
- [1.3.1 -> 2.0.0](#131---200)

## 2.x -> 3.0.0

`tt` 3.0 has no tt environment: an application is a project, binaries and
installed applications live in [scopes](concepts/projects-and-scopes.md),
and most commands moved into groups. The sections go from what nearly every
setup meets to what few do.

### tt.yaml is not read

`tt` 3.0 reads no `tt.yaml`, neither the one of an environment nor the system
`/etc/tarantool/tt.yaml`. Removed with it are:

- the `-S` (`--system`), `-L` (`--local`), `-c` (`--cfg`) and `-s`
  (`--self`) flags of `tt`;
- the `TT_CLI_CFG` and `TT_SYSTEM_CONFIG_DIR` variables;
- the `-c` (`--config`) flag of `tt backup start` and `tt backup finalize`,
  which named the `tt.yaml` to resolve an `<APP>:<INSTANCE>` target with;
  `--scope` and `--template` find the application now;
- the `tt cfg` and `tt env` commands.

`tt` no longer runs the `tt` of `bin_dir` in its place:
`source .env/activate.sh` puts the `tt` of a project on `PATH`. A command
that finds no project, but finds `tt.yaml`, `tt.yml` or `instances.yml` in
the current directory or a parent of it, says that this is a `tt` 2.x
environment and points to this page.

An application is a project instead: a directory with `app.manifest.toml`
(`tt new` writes one) or with instance templates,
`.templates/<name>/instances.yaml`. `tt` finds the project from the current
directory upward. A tt environment with several applications in
`instances.enabled` becomes one project per application:

```text
<environment>/
├── tt.yaml
├── instances.available/
│   ├── app1/
│   │   ├── init.lua
│   │   └── instances.yml
│   └── app2/
└── instances.enabled/
    ├── app1 -> ../instances.available/app1
    └── app2 -> ../instances.available/app2
```

For every application, with the instances stopped by `tt` 2.x:

1. Use the real application directory, not the `instances.enabled` symlink, as
   the project directory, and run `tt new` there. The application is named by
   `[package].name` of the manifest from then on, not by the directory or the
   symlink. `tt new` takes the directory name only when it is a package name,
   lowercase letters, digits and dashes: for `my_app`, run `tt new -n my-app`,
   and the targets and the data directories use `my-app`.
2. Move the instances description, `instances.yml`, to
   `.templates/default/instances.yaml` and the cluster config, `config.yaml`,
   to `.templates/default/config.yaml`. The records keep their names, and a
   name with no fields, as `tt` 2.x wrote them, is read as it is. A Tarantool
   instance named there must be in the topology of the config. An application
   that calls `box.cfg` itself has no topology and needs no `config.yaml`: see
   [box.cfg applications](#boxcfg-applications-are-script-records). The
   restart policy is the `restart` field of a record, `restart: on-failure`,
   not `env.restart_on_failure`.
3. Move the data of every instance from `var/lib/<instance>` to
   `.tarantool/var/lib/<app>/<instance>`, all of them at once. While `var/lib`
   exists and `.tarantool/var/lib` does not, `tt instance start` refuses an
   instance of the project that would keep its data in `.tarantool/var/lib`,
   rather than start it on an empty database. Once `.tarantool/var/lib`
   exists, the check is off, and an instance whose data is still in `var/lib`
   starts empty. Logs, pid files and sockets go to `.tarantool/var/log` and
   `.tarantool/var/run` the same way.
4. Install the binaries the project runs on with `tt binary install`. It
   keeps every version in `.tarantool/versions/<product>/<product>_<version>/`
   and links the active ones into `.tarantool/bin` and their headers into
   `.tarantool/include/tarantool`, in place of `env.bin_dir` and
   `env.inc_dir`. Without them, the project runs the first `tarantool` on
   `PATH`.
5. Remove `tt.yaml`.

The resulting project:

```text
/srv/apps/app1/
├── app.manifest.toml
├── init.lua
├── .templates/
│   └── default/
│       ├── config.yaml
│       └── instances.yaml
└── .tarantool/
    ├── bin/
    └── var/
```

The other settings of `tt.yaml` are replaced as follows:

| `tt.yaml` | `tt` 3.0 |
|---|---|
| `env.instances_enabled` | none: one project per application, or applications installed into the user or the system scope |
| `env.bin_dir`, `env.inc_dir` | `bin/` and `include/` of the scope `tt binary --scope` installs into |
| `env.restart_on_failure` | the `restart` field of the instance record |
| `env.tarantoolctl_layout` | none: the tarantoolctl layout is not supported |
| `env.output_history_max` | the argument of `\history` in the `tt connect` console, such as `\history 15`; 10 without one |
| `app.run_dir`, `log_dir`, `wal_dir`, `memtx_dir`, `vinyl_dir` | `<root>/var/{run,log,lib}/<app>/<instance>/` under the root of the scope; the cluster config may name other paths |
| `modules.directory` | `TT_CLI_MODULES_PATH`; see [External modules](#external-modules-are-found-by-tt_cli_modules_path) |
| `repo.rocks` | the rock servers of `tt registry`, see below |
| `repo.distfiles` | `--local-repo DIR` and `--local-dir DIR` of `tt binary install` and `tt binary search` |
| `ee.credential_path` | `--credentials-file` of `tt binary` |
| `templates` | the path `tt create` takes a template of your own by |

`repo.rocks` added a rock server that LuaRocks queried before its default
ones. `TT_REGISTRIES`, or `[platform].registries` of the manifest, replace
the default servers instead, for `tt package` and `tt rocks` alike;
`TT_CLI_REPO_ROCKS` still adds a server `tt rocks` queries first. See
[Rock servers](concepts/rock-servers.md).

### The instance commands are under tt instance

The commands that act on instances moved under `tt instance`, and the
top-level ones are gone:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt start`, `stop`, `restart`, `kill`, `status`, `logrotate`, `check`, `clean` | `tt instance start`, `stop`, ... |
| `tt log`, `tt log -f` | `tt instance logs`, `tt instance logs --follow` |
| `tt start -i` | `tt instance start --foreground` |
| `tt kill -f`, `tt clean -f` | `tt instance kill --yes`, `tt instance clean --yes` |
| `tt status -f json`, `tt status --pretty` | `tt instance status -o json`, `tt instance status -o pretty-table` |
| `tt connect` | `tt connect` or `tt instance connect`, the same command |

`-f` is no longer the short form of a confirmation, of follow or of a format
in these commands. A confirmation is asked on a terminal only: a script that
piped `y` into `tt stop` or ran `tt --no-prompt stop` passes `--yes` now, or
the command fails with code 1. `tt instance start --foreground` runs every
instance under a watchdog of its own, as in the background, writes their
output into their `tt.log` as well, and does not restart an instance. It
exits with 0 when the instances were stopped in time, with 137 when one did
not stop within its `stop_timeout` and was killed, and otherwise with the
exit code of the first instance that ended on its own with another code. A
command over several instances goes on after an instance it fails for and
exits with 3 when some of them failed.

### A target names an instance

An instance command takes `<app>:<instance>`, `<app>:*` or a comma-separated
list of them, or no target for every instance of the current project. A bare
`<app>`, which `tt` 2.x took for every instance of the application, is
refused, so that a forgotten instance name does not stop them all:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt start app` | `tt instance start app:*`, or `tt instance start` in the project |
| `tt start app:storage-001` | `tt instance start app:storage-001` |
| `tt start`: every application of `instances.enabled` | none: `tt instance start` in each project, or `tt instance start app1:*,app2:*` for applications installed into the system scope |
| `tt connect app` | `tt connect app:<instance>` |

`<app>` is `[package].name` of the manifest of the project, or the name of
the project directory when there is no manifest. Outside the project of that
name, `<app>` is the application installed into the system scope, and
`--scope project|user|system` picks the scope outright. See
[Targets](concepts/instances.md#targets).

### Removed support

- Tarantool 1.10 and 2.x. `tt binary` neither lists nor installs Tarantool,
  Enterprise or the Enterprise SDK below 3, and installs Enterprise under its
  version, `3.8.1`, rather than the `gc64-` and `nogc64-` names of 2.x. An
  Enterprise 2.x installed earlier is listed, switched to and removed under
  its old name.
- The tarantoolctl layout and applications of tarantoolctl.
- Cartridge: `tt cartridge`, `tt replicaset --cartridge`, the `cartridge`
  template of `tt create` and `tt pack --cartridge-compat`.

### Binaries are managed by tt binary

The commands that install binaries moved under `tt binary`:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt install tarantool 3.2.0` | `tt binary install tarantool 3.2.0` |
| `tt install tarantool-ee gc64-3.2.0-0-r40` | `tt binary install tarantool gc64-3.2.0-0-r40`, or `3.2.0ee` for the latest build of 3.2.0 |
| `tt install tarantool-dev ~/src/tarantool/build` | `tt binary install tarantool --from-build ~/src/tarantool/build` |
| `tt uninstall tarantool 3.2.0` | `tt binary uninstall tarantool 3.2.0` |
| `tt search tarantool` | `tt binary search tarantool` |
| `tt binaries list` | `tt binary list` |
| `tt binaries switch tarantool 3.2.0` | `tt binary switch tarantool 3.2.0` |
| `tt download <version>` | `tt binary install tarantool-sdk <version>`, which installs the SDK rather than downloads it |
| `tt install --local-repo tarantool` | `tt binary install tarantool --local-repo DIR`: the Git sources in `DIR/tarantool` |
| `tt install --local-repo tarantool-ee` | `tt binary install tarantool 3ee --local-dir DIR`: the SDK archives in `DIR` |

`--local-repo` was a switch that read `repo.distfiles` of `tt.yaml`; it
takes the directory now, as `--local-dir` does, relative to the current
directory. Every `tt binary` command acts on one scope, `--scope project`
(the default), `user` or `system`, and never falls back to another. See
[Manage binaries](how-to/binaries.md).

### tt pack, tt build and tt init are replaced by tt package

An application is built and packed as a [package](concepts/packages.md),
described by `app.manifest.toml`. Every `tt package` command needs the
manifest; `tt new` writes it.

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt init` | `tt new`; nothing converts a Cartridge or tarantoolctl application |
| `tt build` | `tt package build` |
| `tt pack tgz`, `deb`, `rpm` | `tt package pack`: a `.tt` archive, `tt package install` installs it |
| `tt pack --with-integrity-check KEY` | `tt package pack --with-integrity-check KEY` in a distribution built with `integrity = "rsa"`; see [Sign packages](how-to/sign-packages.md) |
| `tt pack --unit-params-file` | `tt service systemd install`; see [systemd units](#systemd-units-come-from-tt-service-systemd) |
| `tt pack --use-docker`, `--cartridge-compat` | none |

The archive holds `.rocks/`, `.templates/`, the runtimes and the files
`[package].include` lists. An `init.lua` at the root of the project is not
in it unless it is listed there: a cluster config that loads it with
`app.file` either lists it, or loads the code of a component with
`app.module`.

`tt run` runs Tarantool for the package in the current directory, which must
hold `app.manifest.toml`; it read `tt.yaml` before. The arguments reach
Tarantool untouched, as before, `--` included: `tt run script.lua a -- -b`
gives the script `a -- -b`, whatever the help of `tt` 2.x said about `--`.

### box.cfg applications are script records

`tt` 2.x ran an application that calls `box.cfg` itself from `init.lua`,
`<instance>.init.lua` or a single `<name>.lua` in `instances.enabled`.
`tt` 3.0 recognizes none of them: a record of `instances.yaml` with `script`
runs the script instead.

```yaml
legacy-001:
  script: init.lua          # relative to the application directory
```

Such records are not in the topology of a cluster config, and an application
of them alone needs no `config.yaml`. `TARANTOOL_CFG`, which pointed to
`instances.yml`, is no longer set; `env` of the record sets what the script
needs. `box.cfg` takes the data directory and the binary port of the
layout from `TT_WAL_DIR`, `TT_MEMTX_DIR`, `TT_VINYL_DIR` and `TT_LISTEN` for
the options the script does not pass. See
[Run a box.cfg application](how-to/script-instances.md).

### The system environment is the system scope

`tt -S` ran the environment of `/etc/tarantool/tt.yaml`: the applications of
`/etc/tarantool/instances.enabled`, with their data, logs and pid files in
the `sys_env` directories of `/var/lib/tarantool`, `/var/log/tarantool` and
`/var/run/tarantool`. `tt` 3.0 has the system scope instead, `/opt/tarantool`
unless `TT_SYSTEM_ROOT` says otherwise, which holds applications installed
from [packages](#tt-pack-tt-build-and-tt-init-are-replaced-by-tt-package):

```console
# tt package install --scope system myapp-1.0.0-linux-amd64.tt
# tt instance start myapp:*
```

The installed application is `/opt/tarantool/apps/<app>`, a link to the
active version. Its instances keep their files in
`/opt/tarantool/var/{lib,log,run}/<app>/<instance>/`, and the pair of this
host, its own `config.yaml` and `instances.yaml`, goes into
`/opt/tarantool/etc/<app>/`. Move the data of each instance from
`/var/lib/tarantool/sys_env/<app>/<instance>` to
`/opt/tarantool/var/lib/<app>/<instance>` before the first start: `tt` does
not look for the old place outside a project.

The deb and rpm packages of `tt` 3.0 install neither `/etc/tarantool/tt.yaml`
and the `sys_env` directories, nor `/etc/logrotate.d/tt.conf`. An upgrade of
the deb package leaves the old files in place until the package is purged,
and the old logrotate rule runs `tt -S logrotate`, which fails now: remove
it. A rule of your own reopens the logs with
`tt instance logrotate <app>:*`; see [Logs](how-to/systemd.md#logs) for one.

### systemd units come from tt service systemd

The units `tt pack` of `tt` 2.x put into packages ran `tt -L <environment>
start` as forking services, and do not work with `tt` 3.0. For an application
installed in the system scope, `tt service systemd install <app>` writes a
template unit `<app>@.service`, which runs one instance with
`tt instance start --foreground`, and a drop-in for every instance with the
`restart` and `stop_timeout` of its record; `systemctl` starts and stops the
instances then, and `tt instance start`, `stop`, `restart` and `kill` refuse
them.

`tt` 2.x named the unit of an application of several instances
`<app>@.service` as well, in `/usr/lib/systemd/system`; the unit of `tt` 3.0
goes into `/etc/systemd/system` and hides it. Stop and disable the old units
and remove the package that installed them first. Then pack the
application, install it into the system scope and install the units; the
command refuses a project and the user scope.

The parameters of `--unit-params-file` map as follows:

| `systemd-unit-params.yml` | `tt` 3.0 |
|---|---|
| `FdLimit` | `LimitNOFILE=65535` in the unit; another value in a drop-in of your own, `<app>@.service.d/override.conf` |
| `instance-env` | `env` of the instance record, or of `"*"` |
| `TT`, `ConfigPath`, `ExecArgs` | none: the unit runs the `tt` of the scope on the installed application |

See [Run instances under systemd](how-to/systemd.md).

### tt cluster publish asks for a confirmation

`tt cluster publish` asks for a confirmation before it writes a
configuration, on a terminal only: a script that publishes passes `--yes`
(`-y`), or the command fails with code 1 and writes nothing. `--force`
skips the validation, not the confirmation.

`tt cluster show --validate` and `tt cluster publish` validate against the
JSON schema of the Tarantool cluster config. It is stricter than the check
of `tt` 2.x and refuses unknown keys at the top level and in a scope; the
errors read `<path> [code] <message>`.

### Removed commands

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt env`, `. <(tt env)` | `source .env/activate.sh`, which the first `tt binary install` of a project writes |
| `tt cfg dump` | none; `tt registry list` shows the rock servers |
| `tt enable` | none: a project is found from the current directory, `tt package install` installs an application |
| `tt instances` | `tt instance status` for the instances, `tt package list --scope system` for installed applications |
| `tt cartridge` | none |

### tt create

`tt create` needs no `tt.yaml`. It creates the application in the current
directory, or under `--dst`, and makes no symlink in `instances.enabled`. A
template of your own is given by its path, a directory or a `.tgz` archive:
`tt create ./templates/basic`. The `rundir` template variable is gone. The
built-in templates put `instances.yaml` and `config.yaml` into
`.templates/default/`, `single_instance` names them `.yaml` rather than
`.yml`, and the `cartridge` template is removed. A template that writes
`app.manifest.toml` refuses a name that is not a package name:
`vshard_cluster` turns `--name my_app` into `my-app` and refuses `My_App`.

### Exit codes

`tt` 2.x exited with 1 on every error but those of `tt backup` and
`tt restore`. `tt` 3.0 exits with:

- 1 when the request cannot be carried out as given: a usage error, a file or
  a project that is not there, a confirmation without a terminal;
- 2 when the system the command ran on failed: a server that cannot be
  reached or does not answer in time, a host name that does not resolve, a
  filesystem that refuses an operation for want of permission or space.
  `tt connect` to a port nothing listens on exits with 2, not 1;
- 3 when a command over several targets succeeded for some of them only.

A script that tests for 1 tests for a code other than 0 now. `tt backup` and
`tt restore` keep codes of their own from 3 up. `tt help errors` describes
the codes.

### tt backup-id is tt backup id

The command that prints an identifier for a new backup is a subcommand of
`tt backup` now: a backup script that ran `id=$(tt backup-id)` runs
`id=$(tt backup id)`. It prints the same identifier.

### External modules are found by TT_CLI_MODULES_PATH

External modules are found only in the directories `TT_CLI_MODULES_PATH`
lists, separated by colons. `modules.directory` of `tt.yaml` and the default
`modules` directory next to it are not searched: a module kept there is not
found, and a command it replaced runs as `tt`'s own again. A relative
directory in `TT_CLI_MODULES_PATH` is taken from the working directory.

A module named like a group of commands, such as `cluster`, takes over the
whole group: `tt cluster publish ...` runs the module with `publish ...`.
`tt modules`, `tt help`, `tt completion` and `tt version` cannot be replaced.
`-I` runs `tt`'s own command. See
[External modules](how-to/external-modules.md).

### tt daemon runs the commands of tt 3.0

A request to `tt daemon` names a command of `tt` 3.0: `tt start test_app`
was `{"command_name":"start", "params":["test_app"]}` and is
`{"command_name":"instance", "params":["start", "test_app:*"]}` now. The
global flags of `tt` 2.x, such as `-L`, are gone, and a command that asks for a
confirmation needs `--yes`, as the daemon runs it without a terminal. See
[Working with tt daemon](https://github.com/tarantool/tt/blob/v3/doc/examples.md#working-with-tt-daemon-experimental).

### Building tt and importing its packages

`TT_CLI_BUILD_SSL` takes `default`, `openssl` and `openssl_static`; `no`,
`shared` and `static` are refused:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| unset or `no`: no TLS | unset or `default`: TLS in pure Go, no cgo |
| `shared` | `openssl` |
| `static` | `openssl_static` |

For Go code that imports `tt`:

- the module path is `github.com/tarantool/tt/v3`;
- `lib/cluster`, `lib/connect`, `lib/dial` and `lib/integrity` are
  `github.com/tarantool/tt/sdk/cluster`, `sdk/connect`, `sdk/dial` and
  `sdk/integrity`, each a module of its own; their API is alpha;
- `InjectedCmds` of `cli/cmd` is removed: a distribution adds its commands as
  tt modules and chooses its integrity provider with `core.WithIntegrity`.
  See [Distributions and modules](concepts/distributions.md).

### tt cat and tt play are tt data cat and tt data play

`tt cat` and `tt play` are removed without aliases: they are `tt data cat` and
`tt data play`, which read the files in Go and need no local Tarantool. The
flags keep their names, and these differ:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `--format yaml\|json\|lua` | `-o`/`--format`; `json` is JSON Lines, one object a row |
| `--to N` excludes N | `--to N` includes N, as `--from N` does |
| `--from`/`--to` compare one LSN with every replica's | they bound the LSNs of one replica: `--replica`, or the only one in the files; `--from-vclock`/`--to-vclock` bound several |
| a bound may cut a transaction | a transaction inside a bound is taken whole |
| every `.snap` and `.xlog` of a directory, snapshots first | the newest snapshot within the bounds, then the logs by vclock, without what the snapshot contains |
| `--show-system` filters data changes only | it hides RAFT, synchronous replication and NOP rows too |
| `play`: one request a row, no transactions | one IPROTO stream, `--batch` in flight, `--txn-mode`, `--on-error`, `--dry-run` |

A snapshot is not read when `--from` or `--from-vclock` is given, and its row
numbers are not LSNs: the bounds apply to write-ahead logs only. The output
of `cat` keeps the `HEADER` and `BODY` of each row, and the rows of RAFT and
synchronous replication have decoded bodies now. A snapshot holds no data of
vinyl spaces: it is in `.run` files, which `tt data cat` reads when they are
named or found with `-r`, and `tt data play` refuses. `tt data play` exits with
3 when the target refused some of the transactions, and with 2 when the
connection failed. `tt coredump` stays where it is.

## 1.3.1 -> 2.0.0

### New format of tt config file

`tt` 2.0.0 configuration file format is incompatible with previous version:

- Root `tt` section is removed.
- Common environment configuration is moved to the `env` section.
All relative paths in this section are relative to the config file location.
- Relative path in `app` section are relative to the application directory.

### New runtime artifacts layout

Runtime artifacts layout is changed:

- Relative paths are relative to the application directory. In case of
single script instance, a directory is created in the instances
enabled directory.
- Since relative paths already contains an application name,
only instance name is appended to the result directory. Here is an
example of 2.0.0 default layout for a local environment:

```text
instances.enabled/app/
├── init.lua
├── instances.yml
└── var
    ├── lib
    │   ├── inst1
    │   └── inst2
    ├── log
    │   ├── inst1
    │   └── inst2
    └── run
        ├── inst1
        └── inst2
```

Moving artifacts from 1.* versions:

- Create artifacts directories in application dir: var/lib, var/log, var/run.
- Copy instance sub-directories from 1.* environment to application
dir. Data artifacts copying example:
`cp -r <env_dir>/var/lib/app/* <instances_enabled>/app/var/lib/`

Absolute paths are not affected by these layout changes, because an application
name is always appended for them.

### Working directory is changed

Instance process working directory is changed to the application directory.
It was `tt` current working directory previously. So, if the instance
code work with files using relative paths, these files must be moved/copied
to the application directory.
