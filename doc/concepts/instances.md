# Instances

The instance commands - `tt instance start`, `stop`, `restart`, `kill`,
`status`, `logs`, `logrotate`, `check`, `clean` and `connect` - act on the
instances of an application on this host. `tt connect` is
`tt instance connect`, and `tt data play` takes the same targets. `tt data
play` and `tt connect --binary` reach an instance over iproto: at the first
address of `iproto.listen` of its config when that lists TCP addresses only,
as the built-in templates do, or else at its binary port, a unix socket. The
credentials come from `--username` and `--password`, or from
`TT_CLI_USERNAME` and `TT_CLI_PASSWORD`; without them the connection is
guest's.

## The pair

An application runs from a pair of files:

- `instances.yaml`, the description: what runs on this host;
- `config.yaml`, the cluster config Tarantool 3 is started with
  (`tarantool -c`).

The pair is taken whole from one directory:

- a **template** of the application, `.templates/<name>/`: `default` unless
  `--template` names another. A project always runs from a template, and so
  does an installed application with no pair of the host.
- the **pair of the host**, `<root>/etc/<app>/`, for an application
  installed into the user or the system scope. It wins over the templates
  when it is there, and lives apart from the installed version, so it
  survives an upgrade. `--template` is refused when the host has a pair.

A broken pair is an error, never a reason to fall back to another one.

## The description

`instances.yaml` maps instance names to records:

```yaml
storage-001:
storage-002:
  restart: on-failure
gateway:
  binary: ./bin/gateway
legacy:
  script: init.lua
```

A record is one of four kinds:

- **A Tarantool instance configured by the cluster config**: a record with
  neither `binary` nor `script`, such as `storage-001`. It must be in the
  topology of `config.yaml`; the topology may hold more instances, which
  run on other hosts.
- **A Tarantool instance with a script of its own**, a record with
  `script`, configured by `box.cfg` in the script, as applications of
  tt 2.x were. See [Run a box.cfg application](../how-to/script-instances.md).
- **An external process**, a record with `binary`: a program that is not
  Tarantool, started, stopped and shown next to the Tarantool instances. See
  [Run an external process](../how-to/external-processes.md).
- **TCM**, a record with `kind: tcm`: the Tarantool Cluster Manager of the
  application, at most one, whose binary and config tt provides. See
  [Run TCM](../how-to/tcm.md).

The record `"*"`, quoted, stands for every instance of the topology of
`config.yaml` that no other record names, each a Tarantool instance with the
fields of `"*"`; a record of its own replaces `"*"` for its instance whole.
The instances `"*"` stands for start in the order of their names.

A template of `config.yaml` alone runs its whole topology, as `"*": {}`
would. The pair of a host needs its description: it says what runs on this
host, while the rest of the cluster may run on others.

A description of script instances and external processes alone needs no
`config.yaml`. A `config.yaml` that holds only `config.etcd` or
`config.storage` brings the rest of the cluster config from that storage.

When that storage is out of reach, `start`, `restart` and `check` fail,
naming the storage. `status`, `stop`, `kill`, `logs` and `logrotate` go on
with what is known on this host: `"*"` stands for the instances the
description names, those the topology of `config.yaml` itself declares and
those with state under `var/run/<app>/` or `var/lib/<app>/`. tt warns that
the list may be incomplete, naming the storage:

```console
$ tt instance status
   ⚠ application "app": the config storage etcd http://127.0.0.1:2379, prefix "/app" cannot be read: …; "*" stands for the instances known on this host, … and the list may be incomplete
 INSTANCE       STATUS       PID  MODE  CONFIG  BOX  UPSTREAM
 app:legacy     NOT RUNNING
 app:storage-3  NOT RUNNING
```

Every field is in the [reference](../reference/instances-yaml.md).

## Targets

An instance command takes targets:

- `<app>:<instance>`, `<app>:*`, or a comma-separated list of them;
- no target: every instance of the current project.

`<app>` is the current project when it is named so, and the application
installed in the system scope otherwise; `--scope project|user|system`
picks the scope outright, and the user scope is reached only so. A bare
`<app>` is refused: forgetting the instance name must not stop every
instance.

```console
$ tt instance start                               # every instance of the project
$ tt instance start myapp:storage-001 --scope user
$ tt instance stop myapp:* --yes
$ tt instance logs myapp:storage-001 --follow
```

## Running

`tt instance start` runs every instance in the background under a watchdog
of tt. The watchdog starts the instance again after it exits on its own when
its `restart` field says so: `no` (the default), `on-failure` or `always`.

`tt instance start` resolves what an instance runs before it starts the
watchdog: the binary or the `tarantool`, the script or the cluster config,
the arguments, the environment, the restart policy and the paths of the
instance. The watchdog runs that for its whole life, restarts after a crash
included, on the version the instance started from. An edit of
`instances.yaml` made after the start therefore takes effect only at the next
`tt instance start` or `tt instance restart`. `tarantool` itself reads
`config.yaml` at every start, as `config:reload()` does, so a restarted
instance sees the cluster config as it is then. While the watchdog runs, what it
runs is in `<root>/var/run/<app>/<instance>/launch.json`, and
`tt instance status --details` names the `tarantool` the instance runs on.

With `--foreground` tt stays in the foreground instead, for systemd, a
container or a terminal. Every instance still runs under a watchdog of its
own, which does not restart it, so `tt instance stop`, `kill`, `logrotate`
and `status` work on it from another terminal. The output of the instances
goes to their logs and to the standard output of tt, prefixed with the
instance when there are several. SIGINT, SIGTERM and SIGQUIT sent to tt stop
every instance. tt exits once all of them have ended: with 0 when they were
stopped in time, with 137 when one did not stop within its `stop_timeout`
and was killed, and otherwise with the exit code of the first instance that
ended on its own with another code.

A stop sends an instance its `stop_signal`, SIGTERM by default, and kills it
once its `stop_timeout`, 30 seconds by default, is up.

The instances start in the order of the description, and none waits for
another to be ready. An instance that runs already is an error before
anything starts.

In the background, `tt instance start` and `tt instance restart` then wait
for all the instances they started at once, for `--timeout` at most (60
seconds by default):

- a Tarantool instance is up once it answers on its console socket that its
  box runs and its config is applied, what `tt instance status` shows as
  `BOX running` and `CONFIG ready`; an instance that runs its script needs
  only the box;
- an external process is up once it has run for a second;
- TCM is up once its HTTP address answers.

An instance that exits before that has failed, also when its `restart`
policy has the watchdog start it again, and so has one that is not up in
time, which is left running under its watchdog. tt prints what happened and
the last lines of the `tt.log` of the instance, and exits with 1, or with 3
when only some of the instances failed:

```console
$ tt instance start
   • Starting an instance [demo:instance001]...
   ⨯ instance demo:instance001 did not start: it exited with code 1
     The last lines of /…/demo/.tarantool/var/log/demo/instance001/tt.log:
         … E> init.lua:1: boom on start
         Watchdog … (WARN): "exit status 1".
```

`--no-wait` starts the watchdogs and returns at once. An application file
that never returns keeps the config of its instance from being applied:
start its endless work in a fiber of its own.

## Where the files go

An instance of the application `<app>` keeps its files under the root of
the scope of the application:

| Directory | What |
| --- | --- |
| `<root>/var/lib/<app>/<instance>/` | data: `*.snap`, `*.xlog`, vinyl; the working directory of an external process |
| `<root>/var/log/<app>/<instance>/` | `tt.log`: the output of the instance and of its watchdog |
| `<root>/var/run/<app>/<instance>/` | `tt.pid` of the watchdog, `tarantool.pid`, the console socket `tarantool.control`; `process.pid` of an external process |

In a project `<root>` is `.tarantool/`. A Tarantool instance runs in the
application directory: the project, or the installed version. tt gives it
these paths through the `TT_*_DEFAULT` environment variables, so the cluster
config may name others.

`tt instance clean` removes the data and the log of the instances that are
not running.

## Confirmations and exit codes

`stop`, `restart`, `kill` and `clean` ask for a confirmation, which
`--yes` (`-y`) skips. The confirmation is asked on a terminal only: in a
script or a pipe, a command that needs one fails with code 1 unless it is
given `--yes`.

A command over several instances goes on after an instance it fails for. It
exits with 3 when it failed for some of the instances only, with 1 when it
failed for all of them, and with the code of the error when there is one
instance. See [Exit codes](../reference/commands/errors.md).

## Under systemd

`tt service systemd` makes systemd units that run the instances of an
application of the system scope; the instance commands then refuse to start
or stop those instances behind the units. See
[Run instances under systemd](../how-to/systemd.md).
