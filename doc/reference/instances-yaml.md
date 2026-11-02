# instances.yaml

The description of what runs on a host: half of the pair an application runs
from, next to the cluster config `config.yaml`. It is in
`.templates/<name>/` of a project or a package, or in `<root>/etc/<app>/`
for the pair of a host. See [Instances](../concepts/instances.md).

```yaml
"*":
  restart: on-failure
router-001:
  restart: always
  stop_timeout: 60s
gateway:
  binary: gateway
  args: [--port, "8080"]
  env: {GATEWAY_MODE: production}
  logrotate_signal: SIGHUP
legacy:
  script: init.lua
```

The document maps instance names to records. A name with no record,
`storage-001:`, is a Tarantool instance with every field at its default.
The instances start in the order they are written; those `"*"` stands for
take its place, in the order of their names.

An instance name cannot be empty, `.` or `..`, or hold a slash or a control
character: it names the directories of the instance. YAML aliases and merge
keys are not supported.

## The kind of a record

| Record | Kind |
| --- | --- |
| neither `binary` nor `script` | a Tarantool instance configured by the cluster config; it must be in the topology of `config.yaml` |
| `script` | a Tarantool instance configured by its script; see [Run a box.cfg application](../how-to/script-instances.md) |
| `binary` | an external process; see [Run an external process](../how-to/external-processes.md) |

`binary` and `script` are mutually exclusive. Neither the name of a script
instance nor of an external process can be an instance of the topology.

The record `"*"` - quoted, as `*` alone is a YAML alias - stands for every
instance of the topology no other record names. It cannot have `binary` or
`script`. A record of an instance of the topology replaces `"*"` for it
whole, taking none of its fields.

## Fields

| Field | Kinds | Default | |
| --- | --- | --- | --- |
| `restart` | all | `no` | `no`, `on-failure` or `always`: when the watchdog starts the instance again after it exits on its own |
| `stop_signal` | all | `SIGTERM` | the signal a stop sends: `SIGINT` or `INT` |
| `stop_timeout` | all | `30s` | how long a stop waits before it kills the instance; a positive duration such as `10s` or `1m30s` |
| `env` | all | | environment variables on top of the environment of tt |
| `script` | script | | the Lua script, relative to the application directory |
| `binary` | external | | the program: a path, relative to the application directory, when it has a slash; otherwise a name, looked up in `.rocks/bin/` of the application and then on `PATH` |
| `args` | external | | the arguments of the program, a list of strings |
| `workdir` | external | the data directory | the working directory, relative to the application directory |
| `logrotate_signal` | external | none | the signal `tt instance logrotate` sends the process |

Under systemd, `restart` becomes `Restart=` of the unit and `stop_timeout`
its `TimeoutStopSec=`; see [Run instances under systemd](../how-to/systemd.md).

Under `--integrity-check` the `env` of a Tarantool instance is refused: the
values belong in the cluster config. See
[Sign packages](../how-to/sign-packages.md#the-layout-on-tarantool-enterprise).
