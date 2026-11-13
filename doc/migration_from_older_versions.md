# Migration from older versions

This page describes how to migrate existing projects to a newer version of `tt`
and lists breaking changes.

## Contents

- [2.x -> 3.0.0](#2x---300)
- [1.3.1 -> 2.0.0](#131---200)

## 2.x -> 3.0.0

The main differences between `tt` 3.0 and `tt` 2.x are:

- **No tt environment or `tt.yaml`.** An application is now a
  [project](concepts/projects-and-scopes.md). Binaries and installed
  applications live in [installation scopes](concepts/projects-and-scopes.md):
  project, user or system.
- **Commands are grouped:** `tt instance`, `tt binary`, `tt package`, `tt data`
  and others.
- **Only Tarantool 3 is supported.** Cartridge and the tarantoolctl layout are
  no longer supported.

Migration checklist:

1. [Convert the tt environment into projects](#ttyaml-is-not-read), one per
   application.
2. Replace tt 2.x commands in scripts:
   [instance commands](#the-instance-commands-are-under-tt-instance),
   [targets](#a-target-names-an-instance),
   [binaries](#binaries-are-managed-by-tt-binary), and
   [building and packaging](#tt-pack-tt-build-and-tt-init-are-replaced-by-tt-package).
3. If applications run under systemd,
   [recreate their units](#systemd-units-come-from-tt-service-systemd).
4. Check how scripts handle [exit codes](#exit-codes) and
   [confirmations](#tt-cluster-publish-asks-for-a-confirmation).

The sections below start with changes that affect most users, followed by less
common cases.

### tt.yaml is not read

`tt` 3.0 reads no `tt.yaml`: neither the environment file nor the system
`/etc/tarantool/tt.yaml`. The following are also removed:

- The `-S` (`--system`), `-L` (`--local`), `-c` (`--cfg`) and `-s` (`--self`)
  flags of `tt`.
- The `TT_CLI_CFG` and `TT_SYSTEM_CONFIG_DIR` environment variables.
- The `-c` (`--config`) flag of `tt backup start` and `tt backup finalize`. It
  selected the `tt.yaml` used to resolve an `<APP>:<INSTANCE>` target. Use
  `--scope` and `--template` to find the application instead.
- The `tt cfg` and `tt env` commands.

`tt` no longer delegates to the `tt` binary in `bin_dir`. To use the project's
`tt`, run `source .env/activate.sh` to add it to `PATH`.

If a command finds no project but finds `tt.yaml`, `tt.yml` or `instances.yml`
in the current or a parent directory, it reports a tt 2.x environment and points
to this page.

#### Convert an environment into projects

A project replaces an application in a tt environment. It is a directory
containing `app.manifest.toml` (created by `tt new` ) or instance templates at
`.templates/<name>/instances.yaml`. tt searches upward from the current
directory for a project.

A tt environment with several applications in `instances.enabled` becomes one
project per application. For example, a tt 2.x environment might look like this:

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

Stop the application's instances with tt 2.x. Then, for each application:

1. Use the real application directory, not the `instances.enabled` symlink, and
   run `tt new` there. This directory becomes the project directory.

   The application name now comes from `[package].name` in the manifest, not the
   directory or symlink name. `tt new` uses the directory name only if it is a
   valid package name: lowercase letters, digits and dashes. For a directory
   named `my_app`, run `tt new -n my-app`. Targets and data directories will use
   `my-app`.

2. Move the instance description and cluster configuration:

   + `instances.yml` to `.templates/default/instances.yaml`;
   + `config.yaml` to `.templates/default/config.yaml`.

   Record names stay the same. Records with no fields, as written by tt 2.x, are
   read unchanged. A Tarantool instance named in a record must be in the cluster
   configuration's topology. Applications that call `box.cfg` themselves need
   neither a topology nor `config.yaml`; see
   [box.cfg applications](#boxcfg-applications-are-script-records).

   Set the restart policy in the record's `restart` field, for example
   `restart: on-failure`, instead of `env.restart_on_failure`.

3. Move the data of **all** instances at once, from `var/lib/<instance>` to
   `.tarantool/var/lib/<app>/<instance>`. Logs, pid files and sockets now go in
   `.tarantool/var/log` and `.tarantool/var/run`.

   > **Warning:** While `var/lib` exists and `.tarantool/var/lib` does not,
   > `tt instance start` refuses to start a project instance whose data would go
   > in `.tarantool/var/lib`. This prevents it from starting with an empty
   > database. Once `.tarantool/var/lib` exists, this check is off: an instance
   > whose data is still in `var/lib` starts with an empty database.

4. Install the project's binaries with `tt binary install`. This replaces
   `env.bin_dir` and `env.inc_dir`:

   + Versions are stored in
     `.tarantool/versions/<product>/<product>_<version>/`.
   + Active versions are linked into `.tarantool/bin`, and their headers into
     `.tarantool/include/tarantool`.

   Without project binaries, the project uses the first `tarantool` on `PATH`.

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

#### Replace the remaining tt.yaml settings

| `tt.yaml` | `tt` 3.0 |
| --- | --- |
| `env.instances_enabled` | None: one project per application, or applications installed into the user or system scope |
| `env.bin_dir`, `env.inc_dir` | `bin/` and `include/` in the scope selected by `tt binary --scope` |
| `env.restart_on_failure` | The instance record's `restart` field |
| `env.tarantoolctl_layout` | None: the tarantoolctl layout is not supported |
| `env.output_history_max` | The argument of `\history` in the `tt connect` console, such as `\history 15`; 10 without an argument |
| `app.run_dir`, `log_dir`, `wal_dir`, `memtx_dir`, `vinyl_dir` | `<root>/var/{run,log,lib}/<app>/<instance>/` under the scope root; the cluster configuration can specify other paths |
| `modules.directory` | `TT_CLI_MODULES_PATH`; see [External modules](#external-modules-are-found-by-tt_cli_modules_path) |
| `repo.rocks` | The rock servers shown by `tt registry`; see below |
| `repo.distfiles` | `--local-repo DIR` and `--local-dir DIR` of `tt binary install` and `tt binary search` |
| `ee.credential_path` | `--credentials-file` of `tt binary` |
| `templates` | Pass the path to your own template to `tt create` |

The behavior of `repo.rocks` has changed:

- In tt 2.x, `repo.rocks` added a rock server that LuaRocks queried before its
  default servers.
- In tt 3.0, `TT_REGISTRIES` or the manifest's `[platform].registries`
  **replace** the default servers for both `tt package` and `tt rocks`.
- `TT_CLI_REPO_ROCKS` still adds a server that `tt rocks` queries first.

See [Rock servers](concepts/rock-servers.md).

### The instance commands are under tt instance

Instance commands are grouped under `tt instance`. The old top-level commands
are removed:

| `tt` 2.x | `tt` 3.0 |
| --- | --- |
| `tt start`, `stop`, `restart`, `kill`, `status`, `logrotate`, `check`, `clean` | `tt instance start`, `stop`, ... |
| `tt log`, `tt log -f` | `tt instance logs`, `tt instance logs --follow` |
| `tt start -i` | `tt instance start --foreground` |
| `tt kill -f`, `tt clean -f` | `tt instance kill --yes`, `tt instance clean --yes` |
| `tt status -f json`, `tt status --pretty` | `tt instance status -o json`, `tt instance status -o pretty-table` |
| `tt connect` | `tt connect` or `tt instance connect`: the same command |

Other changes:

- `-f` no longer means confirmation, follow or output format in these commands.
- Commands prompt for confirmation only in a terminal. If a script piped `y`
  into `tt stop` or ran `tt --no-prompt stop`, pass `--yes` instead. Without it,
  the command exits with code 1.
- `tt instance start --foreground` runs each instance under its own watchdog, as
  in background mode, also writes its output to `tt.log`, and does not restart
  instances. It exits with:
  + `0` if the instances stopped in time;
  + `137` if an instance did not stop within its `stop_timeout` and was killed;
  + otherwise, the exit code of the first instance that ended on its own with
    another code.
- A command targeting several instances continues after an operation fails for
  one instance. It exits with code 3 if the command succeeded for only some
  instances.

### A target names an instance

Instance commands accept these targets:

- `<app>:<instance>`: one instance;
- `<app>:*`: all instances of an application;
- a comma-separated list of these targets;
- no target: all instances of the current project.

A bare `<app>`, which meant all instances of the application in tt 2.x, is now
rejected. This prevents a missing instance name from accidentally stopping them
all:

| `tt` 2.x | `tt` 3.0 |
| --- | --- |
| `tt start app` | `tt instance start app:*`, or `tt instance start` in the project |
| `tt start app:storage-001` | `tt instance start app:storage-001` |
| `tt start`: every application in `instances.enabled` | Run `tt instance start` in each project, or `tt instance start app1:*,app2:*` for applications installed into the system scope |
| `tt connect app` | `tt connect app:<instance>` |

`<app>` is `[package].name` from the project manifest, or the project directory
name if there is no manifest. Outside a project of that name, `<app>` refers to
an application installed into the system scope. Use
`--scope project|user|system` to select the scope explicitly. See
[Targets](concepts/instances.md#targets).

### Removed support

- **Tarantool 1.10 and 2.x.** `tt binary` neither lists nor installs Tarantool,
  Tarantool Enterprise or the Enterprise SDK below version 3.
  + Tarantool Enterprise is now installed under its version, such as `3.8.1`,
    rather than the `gc64-` and `nogc64-` names used in 2.x.
  + A previously installed Enterprise 2.x can still be listed, switched to and
    removed under its old name.
- **The tarantoolctl layout and tarantoolctl applications.**
- **Cartridge:** `tt cartridge`, `tt replicaset --cartridge`, the `cartridge`
  template of `tt create`, and `tt pack --cartridge-compat`.

### Binaries are managed by tt binary

Binary management commands are grouped under `tt binary`:

| `tt` 2.x | `tt` 3.0 |
| --- | --- |
| `tt install tarantool 3.2.0` | `tt binary install tarantool 3.2.0` |
| `tt install tarantool-ee gc64-3.2.0-0-r40` | `tt binary install tarantool gc64-3.2.0-0-r40`, or `3.2.0ee` for the latest build of 3.2.0 |
| `tt install tarantool-dev ~/src/tarantool/build` | `tt binary install tarantool --from-build ~/src/tarantool/build` |
| `tt uninstall tarantool 3.2.0` | `tt binary uninstall tarantool 3.2.0` |
| `tt search tarantool` | `tt binary search tarantool` |
| `tt binaries list` | `tt binary list` |
| `tt binaries switch tarantool 3.2.0` | `tt binary switch tarantool 3.2.0` |
| `tt download <version>` | `tt binary install tarantool-sdk <version>`: installs the SDK rather than just downloading it |
| `tt install --local-repo tarantool` | `tt binary install tarantool --local-repo DIR`: Git sources in `DIR/tarantool` |
| `tt install --local-repo tarantool-ee` | `tt binary install tarantool 3ee --local-dir DIR`: SDK archives in `DIR` |

- `--local-repo` used to be a switch that read `repo.distfiles` from `tt.yaml`.
  It now takes a directory path, as `--local-dir` does, relative to the current
  directory.
- Each `tt binary` command acts on one scope: `--scope project` (the default),
  `user` or `system`. It never falls back to another scope.

See [Manage binaries](how-to/binaries.md).

### tt pack, tt build and tt init are replaced by tt package

An application is built and packed as a [package](concepts/packages.md)
described in `app.manifest.toml`. Every `tt package` command needs a manifest;
create one with `tt new`.

| `tt` 2.x | `tt` 3.0 |
| --- | --- |
| `tt init` | `tt new`. There is no converter for Cartridge or tarantoolctl applications |
| `tt build` | `tt package build` |
| `tt pack tgz`, `deb`, `rpm` | `tt package pack`: creates a `.tt` archive installed with `tt package install` |
| `tt pack --with-integrity-check KEY` | `tt package pack --with-integrity-check KEY` in a distribution built with `integrity = "rsa"`; see [Sign packages](how-to/sign-packages.md) |
| `tt pack --unit-params-file` | `tt service systemd install`; see [systemd units](#systemd-units-come-from-tt-service-systemd) |
| `tt pack --use-docker`, `--cartridge-compat` | None |

The archive includes `.rocks/`, `.templates/`, runtimes and the files listed in
`[package].include`.

> **Note:** An `init.lua` in the project root is included only if
> `[package].include` lists it. If the cluster configuration loads it with
> `app.file`, add it to that list or load a component's code with `app.module`
> instead.

`tt run` runs Tarantool for the package in the current directory, which must
contain `app.manifest.toml`. It previously read `tt.yaml`. Arguments, including
`--`, still pass through unchanged: `tt run script.lua a -- -b` passes `a -- -b`
to the script, even though the tt 2.x help described `--` differently.

### box.cfg applications are script records

In tt 2.x, an application that calls `box.cfg` itself ran from `init.lua`,
`<instance>.init.lua` or a single `<name>.lua` in `instances.enabled`. tt 3.0
does not recognize these layouts. To run a script, set the `script` field in an
`instances.yaml` record:

```yaml
legacy-001:
  script: init.lua          # relative to the application directory
```

- These records are not part of the cluster configuration's topology. An
  application made up only of script records needs no `config.yaml`.
- `TARANTOOL_CFG`, which pointed to `instances.yml`, is no longer set. Use the
  record's `env` field for environment variables the script needs.
- For data directories and the binary port that the script does not set,
  `box.cfg` uses `TT_WAL_DIR`, `TT_MEMTX_DIR`, `TT_VINYL_DIR` and `TT_LISTEN`
  from the layout.

See [Run a box.cfg application](how-to/script-instances.md).

### The system environment is the system scope

In tt 2.x, `tt -S` used the environment in `/etc/tarantool/tt.yaml`.
Applications lived in `/etc/tarantool/instances.enabled`, with data, logs and
pid files in the `sys_env` directories under `/var/lib/tarantool`,
`/var/log/tarantool` and `/var/run/tarantool`.

In tt 3.0, the system scope replaces this environment. Its root is
`/opt/tarantool`, or the path set by `TT_SYSTEM_ROOT`. It holds applications
installed from
[packages](#tt-pack-tt-build-and-tt-init-are-replaced-by-tt-package):

```console
# tt package install --scope system myapp-1.0.0-linux-amd64.tt
# tt instance start myapp:*
```

The layout is:

- `/opt/tarantool/apps/<app>`: a link to the active application version;
- `/opt/tarantool/var/{lib,log,run}/<app>/<instance>/`: instance files;
- `/opt/tarantool/etc/<app>/`: this host's `config.yaml` and `instances.yaml`.

Before the first start, move each instance's data from
`/var/lib/tarantool/sys_env/<app>/<instance>` to
`/opt/tarantool/var/lib/<app>/<instance>`. Outside a project, tt does not check
the old data location.

The tt 3.0 deb and rpm packages no longer install `/etc/tarantool/tt.yaml`, the
`sys_env` directories or `/etc/logrotate.d/tt.conf`. A deb package upgrade
leaves the old files in place until the package is purged.

> **Warning:** The old logrotate rule runs `tt -S logrotate`, which now fails.
> Remove that rule. In your own rule, reopen logs with
> `tt instance logrotate <app>:*`. See [Logs](how-to/systemd.md#logs) for an
> example.

### systemd units come from tt service systemd

The units that tt 2.x `tt pack` added to packages ran
`tt -L <environment> start` as forking services. They do not work with tt 3.0.

For an application installed into the system scope,
`tt service systemd install <app>` creates:

- A template unit, `<app>@.service`, that runs one instance with
  `tt instance start --foreground`.
- A drop-in for each instance, using `restart` and `stop_timeout` from its
  record.

After installation, use `systemctl` to manage these instances.
`tt instance start`, `stop`, `restart` and `kill` refuse to manage them.

In tt 2.x, a multi-instance application also used `<app>@.service`, but in
`/usr/lib/systemd/system`. The tt 3.0 unit goes in `/etc/systemd/system` and
overrides the old one. Migrate in this order:

1. Stop and disable the old units.
2. Remove the package that installed them.
3. Pack the application and install the archive into the system scope.
4. Install the units with `tt service systemd install <app>`.

The command supports only the system scope, not the project or user scope.

Replace `--unit-params-file` parameters as follows:

| `systemd-unit-params.yml` | `tt` 3.0 |
| --- | --- |
| `FdLimit` | The unit sets `LimitNOFILE=65535`. Set a different value in your own drop-in, `<app>@.service.d/override.conf` |
| `instance-env` | The `env` field of the instance record or the `"*"` record |
| `TT`, `ConfigPath`, `ExecArgs` | None: the unit runs the scope's `tt` on the installed application |

See [Run instances under systemd](how-to/systemd.md).

### tt cluster publish asks for a confirmation

- `tt cluster publish` prompts for confirmation before writing configuration,
  but only in a terminal. In publishing scripts, pass `--yes` (`-y`); otherwise,
  the command exits with code 1 and writes nothing.
- `--force` skips validation, not confirmation.
- `tt cluster show --validate` and `tt cluster publish` validate against the
  Tarantool cluster configuration JSON Schema. Validation is stricter than in tt
  2.x: unknown keys at the top level and in configuration scopes are rejected.
  Errors use the format `<path> [code] <message>`.

### Removed commands

| `tt` 2.x | `tt` 3.0 |
| --- | --- |
| `tt env`, `. <(tt env)` | `source .env/activate.sh`. The first `tt binary install` in a project creates this file |
| `tt cfg dump` | None. `tt registry list` shows the rock servers |
| `tt enable` | None: projects are found from the current directory, and `tt package install` installs applications |
| `tt instances` | `tt instance status` for instances, `tt package list --scope system` for installed applications |
| `tt cartridge` | None |

### tt create

- `tt create` needs no `tt.yaml`.
- It creates an application in the current directory or under `--dst`, without a
  symlink in `instances.enabled`.
- Specify your own template by its directory or `.tgz` archive path, for example
  `tt create ./templates/basic`.
- The `rundir` template variable is removed.
- Built-in templates place `instances.yaml` and `config.yaml` in
  `.templates/default/`. `single_instance` uses `.yaml` rather than `.yml`. The
  `cartridge` template is removed.
- Templates that create `app.manifest.toml` reject names that are invalid for a
  package. For example, `vshard_cluster` converts `--name my_app` to `my-app`
  but rejects `My_App`.

### Exit codes

In tt 2.x, all errors except those from `tt backup` and `tt restore` returned
code 1. In tt 3.0, the codes are:

- `1`: the request cannot be carried out as given. Examples include usage
  errors, a missing file or project, or a required confirmation without a
  terminal.
- `2`: a failure of the system where the command ran. Examples include an
  unreachable or unresponsive server, a hostname that cannot be resolved, or a
  filesystem operation denied due to permissions or insufficient space.
  `tt connect` to a port with no listener now exits with 2 rather than 1.
- `3`: a command targeting several resources succeeded for only some of them.

If a script checked for code 1, check for any nonzero code instead. `tt backup`
and `tt restore` keep their own codes from 3 upward. See `tt help errors` for
all codes.

### tt backup-id is tt backup id

The command that prints an identifier for a new backup has moved under
`tt backup`. In backup scripts, replace `id=$(tt backup-id)` with
`id=$(tt backup id)`. The identifier format is unchanged.

### External modules are found by TT_CLI_MODULES_PATH

- External modules are found only in the colon-separated directories in
  `TT_CLI_MODULES_PATH`. Relative paths are resolved from the working directory.
- `modules.directory` from `tt.yaml` and the default `modules` directory next to
  it are no longer searched. A module left there will not be found, and the
  command it replaced will run as a built-in tt command again.
- A module named after a command group, such as `cluster`, replaces the whole
  group: `tt cluster publish ...` runs the module with arguments `publish ...`.
- `tt modules`, `tt help`, `tt completion` and `tt version` cannot be replaced.
- `-I` runs the built-in tt command even if a module with the same name exists.

See [External modules](how-to/external-modules.md).

### tt daemon runs the commands of tt 3.0

A request to `tt daemon` must now name a tt 3.0 command. For example, a request
for `tt start test_app` changes as follows:

- tt 2.x: `{"command_name":"start", "params":["test_app"]}`;
- tt 3.0: `{"command_name":"instance", "params":["start", "test_app:*"]}`.

Global tt 2.x flags such as `-L` are removed. Pass `--yes` for commands that
require confirmation: the daemon runs commands without a terminal. See
[Working with tt daemon](https://github.com/tarantool/tt/blob/v3/doc/examples.md#working-with-tt-daemon-experimental).

### Building tt and importing its packages

`TT_CLI_BUILD_SSL` accepts `default`, `openssl` and `openssl_static`. The values
`no`, `shared` and `static` are rejected:

| `tt` 2.x | `tt` 3.0 |
| --- | --- |
| Unset or `no`: no TLS | Unset or `default`: TLS in pure Go, without cgo |
| `shared` | `openssl` |
| `static` | `openssl_static` |

For Go code that imports tt:

- The module path is `github.com/tarantool/tt/v3`.
- `lib/cluster`, `lib/connect`, `lib/dial` and `lib/integrity` have moved to
  `github.com/tarantool/tt/sdk/cluster`, `sdk/connect`, `sdk/dial` and
  `sdk/integrity`. Each is a separate module with an alpha API.
- `InjectedCmds` from `cli/cmd` is removed. A distribution adds commands as tt
  modules and selects its integrity provider with `core.WithIntegrity`. See
  [Distributions and modules](concepts/distributions.md).

### tt cat and tt play are tt data cat and tt data play

`tt cat` and `tt play` are removed without aliases. Use `tt data cat` and
`tt data play` instead. The new commands read files in Go, so no local Tarantool
is needed. Flag names are retained, but their behavior has changed:

| `tt` 2.x | `tt` 3.0 |
| --- | --- |
| `--format yaml\|json\|lua` | `-o`/`--format`. `json` is JSON Lines: one object per line |
| `--to N` excludes N | `--to N` includes N, as does `--from N` |
| `--from`/`--to` compare one LSN with every replica's | Bound one replica's LSNs: select it with `--replica`, or use the only replica in the files. For several replicas, use `--from-vclock`/`--to-vclock` |
| A bound may split a transaction | A transaction within the bounds is taken whole |
| All `.snap` and `.xlog` files in a directory, snapshots first | The newest snapshot within the bounds, then logs in vclock order, excluding data already in the snapshot |
| `--show-system` filters data changes only | Also hides RAFT, synchronous replication and NOP rows |
| `play`: one request per row, no transactions | One IPROTO stream, with `--batch` requests in flight; supports `--txn-mode`, `--on-error` and `--dry-run` |

Additional changes:

- If `--from` or `--from-vclock` is set, the snapshot is not read. Snapshot row
  numbers are not LSNs, so bounds apply only to write-ahead logs (WAL).
- `cat` output retains each row's `HEADER` and `BODY`. RAFT and synchronous
  replication row bodies are now decoded.
- A snapshot contains no vinyl space data; that data lives in `.run` files.
  `tt data cat` reads them when explicitly named or found with `-r`.
  `tt data play` rejects them.
- `tt data play` exits with code 3 if the target rejected some transactions, or
  2 if the connection failed.
- `tt coredump` has not moved into the group.

## 1.3.1 -> 2.0.0

### New format of tt config file

The tt 2.0.0 configuration file format is incompatible with previous versions:

- The root `tt` section is removed.
- Common environment settings have moved to the `env` section. All relative
  paths in this section are resolved from the configuration file's directory.
- Relative paths in the `app` section are resolved from the application
  directory.

### New runtime artifacts layout

The runtime artifacts layout has changed:

- Relative paths are resolved from the application directory. For an instance
  defined by a single script, a directory is created in `instances.enabled`.
- Since relative paths already contain the application name, only the instance
  name is appended. The default local environment layout in 2.0.0 is:

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

To move artifacts from version 1.*:

- Create the artifact directories in the application directory: var/lib, var/log
  and var/run.
- Copy the instance subdirectories from the 1.* environment into the application
  directory. For example, to copy data:
  `cp -r <env_dir>/var/lib/app/* <instances_enabled>/app/var/lib/`

These changes do not affect absolute paths: the application name is always
appended to them.

### Working directory is changed

The instance process now uses the application directory as its working
directory, rather than tt's current directory. Move or copy files that the
instance accesses by relative paths into the application directory.
