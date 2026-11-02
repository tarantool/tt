# Run a box.cfg application

An application that configures Tarantool with `box.cfg` in its own script,
as the `init.lua` and `<instance>.init.lua` applications of tt 2.x did, runs
as records with `script`: Tarantool instances configured by their script
rather than by the cluster config. Such an application needs no
`config.yaml`.

## Describe the instances

`.templates/default/instances.yaml`:

```yaml
legacy-001:
  script: init.lua          # relative to the application directory
  env: {TT_MEMTX_MEMORY: "268435456"}
```

`init.lua`:

```lua
box.cfg{}
box.schema.space.create('kv', {if_not_exists = true})
```

```console
$ tt instance start
$ tt instance status
 INSTANCE           STATUS   PID      MODE  CONFIG         BOX      UPSTREAM
 legacy:legacy-001  RUNNING  1330686  RW    uninitialized  running  --
$ echo "return box.cfg.memtx_memory" | tt connect legacy:legacy-001 -f-
---
- 268435456
...
```

## Where the instance keeps its files

Tarantool runs the script in the application directory. `box.cfg` takes the
options the script does not pass from the environment, and tt sets the ones
of the layout there:

- `TT_WAL_DIR`, `TT_MEMTX_DIR` and `TT_VINYL_DIR`: the data directory,
  `<root>/var/lib/<app>/<instance>/`;
- `TT_LISTEN`: the unix socket `tarantool.sock` in the run directory, unless
  `TT_LISTEN` is set in the environment of tt.

The console socket is `tarantool.control` in the run directory, and the
output goes to `tt.log`. `env` of the record goes on top and may move the
data with the same variables; in the user and system scopes a relative path
there is refused.

A relative path the script passes to `box.cfg` itself, tt cannot check. In
the user and system scopes it is resolved against the installed version, so
the instance writes into the version, which an upgrade replaces: let the
script take the paths from the environment.

## What works on it

- `tt connect`, `tt play`, `tt instance status` and `tt instance logrotate`
  work as with any Tarantool instance.
- `tt instance check` checks the syntax of the script.
- `tt instance clean` removes its data and log.
- `tt cluster show` and `tt cluster publish` refuse it: no cluster config
  configures it.
- The application targets of `tt replicaset` leave it out when the
  application has instances of the cluster config too. An application of
  script instances alone keeps them, and `tt replicaset` acts on it with the
  custom orchestrator without `--custom`; `--config` and `--custom` still
  force an orchestrator.

See also [Migrate from tt 2.x](../migration_from_older_versions.md).
