# Instances

The instance commands - `tt instance start`, `stop`, `restart`, `kill`,
`status`, `logs`, `logrotate`, `check`, `clean` and `connect` - act on the
instances of an application on this host. `tt connect` is
`tt instance connect`, and `tt data play` takes the same targets. `tt data
play`, `tt connect --binary` and the `tt backup` commands reach an instance
over iproto, at the address its config gives (see
[Over iproto](#over-iproto)).

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

### Instances that are not in the config

An instance can leave the topology while it runs: `tt replicaset expel
--force` removes one that is still running, and so does `tt cluster publish`
of a config without it. With `"*"` such an instance is still found by its
watchdog, which holds `var/run/<app>/<instance>/tt.pid`. `status` shows it
with a note, `stop`, `kill`, `logs` and `logrotate` reach it by name and
through `<app>:*`, and `start` and `restart` do not start it again:

```console
$ tt instance status
 INSTANCE             STATUS   PID      MODE  CONFIG  BOX      UPSTREAM
 ex:replicaset-001-a  RUNNING  2320034  RW    ready   running  --
 ex:replicaset-001-b  RUNNING  2315711  RO    ready   running  stopped
 ex:replicaset-002-a  RUNNING  2320041  RW    ready   running  --

Not in the config: ex:replicaset-001-b.
Removed from the topology while running, they run on: tt instance stop stops them,
tt instance start does not start them again
$ tt instance stop -y ex:replicaset-001-b
```

`-o json` and `-o yaml` mark it with `not_in_config: true`. Once it has
stopped, it is gone from the list.

### Removing an instance from a replicaset

`tt replicaset expel <app>:<instance>` removes an instance of the
centralized config from its replicaset, in the order the Tarantool
documentation gives for
[removing instances](https://www.tarantool.io/en/doc/latest/platform/replication/replication_tutorials/repl_bootstrap/#removing-instances).
Expel does not stop the instance, which may run on another host: stop it
there first. Expel then asks the other instances of the replicaset, not the
instance itself, and refuses while any of them is still connected to it:

```console
$ tt replicaset expel ex:replicaset-002-b
…
   ⨯ instance "replicaset-002-b" is still connected to the replicaset: stop it on its host (tt instance stop ex:replicaset-002-b) or pass --force (replicaset-002-a has upstream follow, downstream follow)
$ tt instance stop -y ex:replicaset-002-b
$ tt replicaset expel ex:replicaset-002-b
…
   • The instance replicaset-002-b is removed from the config and from _cluster. Its data is left in …/var/lib/ex/replicaset-002-b; if instances.yaml lists it by name, remove it from there
   • Done.
```

The entry of the instance is deleted from `config.yaml`, the other
instances reload the config, and the instance is deleted from `_cluster`
on the writable one. The leader of a replicaset with manual failover is
refused: promote another instance first. `tt cluster replicaset expel <URI>
<instance>` does the same with a config in etcd or a Tarantool config
storage; the other instances it asks are those of the project it runs in.

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

## Over iproto

`tt data play`, `tt connect --binary`, `tt backup start` and
`tt backup finalize` reach `<app>:<instance>` over iproto at the address of
its effective config: the `TT_*` variables of its record and of the
environment, the file and the config storage, ranked as Tarantool ranks
them.

1. `iproto.advertise.client`, the URI Tarantool advertises to clients:
   `host:port`, a port, or a unix socket `unix/:<path>`, a relative path
   being relative to the application directory, where the instance runs.
   Tarantool refuses a login, URI parameters, an address of every interface
   and port 0 there, and does not start with such a config; tt refuses them
   too.
2. Otherwise the first TCP address of `iproto.listen`, unix sockets listed
   or not. `0.0.0.0`, `[::]` and a bare port mean every interface of this
   host, and tt dials `127.0.0.1`, `[::1]` and `127.0.0.1` for them. An
   entry of port 0 is skipped: its port is unknown until the instance
   binds it.
3. Otherwise the binary port: the first unix socket of `iproto.listen`, or
   the `tarantool.sock` tt has the instance listen on in its run directory.

The entry of `iproto.listen` at the address gives the transport. With
`transport: ssl` the connection is a TLS one, with the `ssl_*` files of the
entry, relative to the application directory: Tarantool connects a member of
a replica set to a peer with them too, when it takes the URI of the peer
from `iproto.listen`. The `--ssl*` flags take precedence over the files.
`iproto.advertise.client` is a bare URI: it takes the params of the entry at
the same socket, or at the same port of the same host or of every interface.
In the config below `tt connect --binary myapp:storage-001` dials
`db1.example.com:3302` with TLS:

```yaml
iproto:
  listen:
  - uri: 127.0.0.1:3301
  - uri: 0.0.0.0:3302
    params:
      transport: ssl
      ssl_key_file: localhost.key
      ssl_cert_file: localhost.crt
      ssl_ca_file: ca.crt
  advertise:
    client: db1.example.com:3302
```

The credentials are those of `--username` and `--password` only:
`TT_CLI_USERNAME` and `TT_CLI_PASSWORD` are for a URI without credentials,
and a user of the environment meant for one never logs in to an instance of
this host. Without credentials the connection is guest's, and `tt data play`
refused as guest says how to give them.

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
instance when there are several, `<app>:<instance>: `, as `tt instance logs`
prefixes the lines of every instance. SIGINT, SIGTERM and SIGQUIT sent to tt stop
every instance. tt exits once all of them have ended: with 0 when they were
stopped in time, with 137 when one did not stop within its `stop_timeout`
and was killed, and otherwise with the exit code of the first instance that
ended on its own with another code.

A stop sends an instance its `stop_signal`, SIGTERM by default, and kills it
once its `stop_timeout`, 30 seconds by default, is up.

The instances start in the order of the description, and none waits for
another to be ready. An instance that runs already is an error before
anything starts.

Before anything starts, `tt instance start` and `tt instance restart`
validate the cluster config of the instances against the schema of the
Tarantool config, as `tt cluster show --validate` does, and so does
`tt instance check`. A config Tarantool would refuse starts nothing and
stops nothing, and the error names every problem:

```console
$ tt instance start
   ⨯ application "demo": the cluster config /…/demo/.templates/default/config.yaml is invalid:
       unknown option memtx.memroy
     fix it, or pass --no-validate to start all the same
```

`--no-validate` skips the validation, for an option of a Tarantool newer
than the schemas tt knows.

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

The watchdog records how the last process of its instance ended, and when,
in `last_exit.json` in the run directory. `tt instance status --details`
shows it for an instance that does not run, with the last lines of its
`tt.log` unless it was stopped cleanly; without `--details`, the status says
when some instances ended without a stop. `-o json` and `-o yaml` give the
record as `last_exit`:

```console
$ tt instance status --details
Last exit of demo:instance001: it exited with code 1, at 2026-11-17 00:39:27
  The last lines of /…/demo/.tarantool/var/log/demo/instance001/tt.log:
    … E> init.lua:1: boom on start
    Watchdog … (WARN): "exit status 1".
    Watchdog … (INFO): the Instance has shutdown.
  tt instance logs demo:instance001 prints more.
…
 INSTANCE          STATUS       PID  MODE  CONFIG  BOX  UPSTREAM
 demo:instance001  NOT RUNNING
```

The `PID` of `tt instance status` is the process the watchdog runs:
`tarantool`, the program of an external process or TCM, the PID `top`,
`gdb`, `perf`, `box.info.pid` and the log lines of Tarantool name. The
watchdog publishes it in `watchdog.json` in the run directory while it
runs, with when the process started, how many times the `restart` policy
had it start the process again, and the last periodic integrity check.
`tt instance status --details` prints them with the PID of the watchdog,
which holds `tt.pid`, and `-o json` and `-o yaml` give them as
`watchdog_pid`, `started_at`, `restarts` and `last_integrity_check`. Here
the instance was killed and started again:

```console
$ tt instance status --details
Watchdog of demo:instance001: PID 1781207
Process of demo:instance001: PID 1781681, started at 2026-11-23 07:53:21
Restarts of demo:instance001: 1
…
 INSTANCE          STATUS   PID      MODE  CONFIG  BOX      UPSTREAM
 demo:instance001  RUNNING  1781681  RW    --      running  --
```

While no process runs, as in the 5 seconds before a restart, the `PID` is
empty, `null` in JSON and YAML, and `--details` says that none runs now. So
it is for an instance whose watchdog an older tt started, which publishes no
state, until the instance is restarted. Stop an instance with
`tt instance stop`, not with a `kill` of its PID: the watchdog takes a
process a signal ended for one that exited, and starts it again as its
`restart` says. `tt instance stop`, `kill` and `logrotate` name the
watchdog in what they print.

## Where the files go

An instance of the application `<app>` keeps its files under the root of
the scope of the application:

| Directory | What |
| --- | --- |
| `<root>/var/lib/<app>/<instance>/` | data: `*.snap`, `*.xlog`, vinyl; the working directory of an external process |
| `<root>/var/log/<app>/<instance>/` | `tt.log`: the output of the instance and of its watchdog |
| `<root>/var/run/<app>/<instance>/` | `tt.pid` of the watchdog, `watchdog.json`, the state it publishes, `tarantool.pid`, the console socket `tarantool.control`; `process.pid` of an external process |

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
given `--yes`. Under the global `tt --no-prompt` it is not asked on a
terminal either, and the command fails the same way; so do the
confirmations of `tt cluster publish`, `tt replicaset rebootstrap`, and
`tt package install` and `uninstall`.

An instance that is not running is no failure of `stop`, `kill` or
`logrotate`: each says `The instance <app>:<instance> is not running.` and
goes on.

A command over several instances goes on after an instance it fails for. It
exits with 3 when it failed for some of the instances only, with 1 when it
failed for all of them, and with the code of the error when there is one
instance. See [Exit codes](../reference/commands/errors.md).

## Under systemd

`tt service systemd` makes systemd units that run the instances of an
application of the system scope; the instance commands then refuse to start
or stop those instances behind the units. See
[Run instances under systemd](../how-to/systemd.md).
