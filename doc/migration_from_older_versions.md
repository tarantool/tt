# Migration from older versions

This file contains information on how to migrate existing projects from
one `tt` version to a newer one. Additionally to migration hints, it
contains breaking changes descriptions.

## Contents

- [2.x -> 3.0.0](#2x---300)
- [1.3.1 -> 2.0.0](#131---200)

## 2.x -> 3.0.0

### tt.yaml is not read

`tt` 3.0 reads no `tt.yaml`, neither the one of an environment nor the system
`/etc/tarantool/tt.yaml`. The `-S`, `-L` and `-c` flags, the `TT_CLI_CFG`
variable and the `tt cfg` and `tt env` commands are removed, and `tt` no
longer runs the `tt` of `bin_dir` in its place. A directory that holds
`tt.yaml` or `instances.yml` and is not a project is reported as a `tt` 2.x
environment.

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
   the project directory, and run `tt new` there.
2. Move the instances description, `instances.yml`, to
   `.templates/default/instances.yaml` and the cluster config, `config.yaml`,
   to `.templates/default/config.yaml`. Every instance must be in the topology
   of the config; the restart policy is the `restart` field of an instance
   record now, not `env.restart_on_failure`.
3. Move the data of each instance from `var/lib/<instance>` to
   `.tarantool/var/lib/<app>/<instance>`. `tt` refuses to start an instance
   while the data is only in the old place, rather than start it on an empty
   database. Logs, pid files and sockets go to `.tarantool/var/log` and
   `.tarantool/var/run` the same way.
4. Install the binaries the project runs on with `tt binary install`, which
   puts them into `.tarantool/bin` and the headers into
   `.tarantool/include/tarantool`, in place of `env.bin_dir` and
   `env.inc_dir`.
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

The other settings of `tt.yaml` are replaced as follows: `repo.rocks` by the
rock servers of `tt registry` (`TT_REGISTRIES` or `[platform].registries` of
the manifest), `templates` by the path `tt create` takes a template of your own
by, `ee.credential_path` by the `--credentials-file` flag of `tt binary`, and
`modules.directory` by `TT_CLI_MODULES_PATH`. The `app` directories have no
replacement: the runtime artifacts go under the root of the scope.

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
output into their `tt.log` as well, and does not restart an instance: it
exits with 0 when the instances were stopped, and with the exit code of an
instance that failed. A command over several instances goes on after an
instance it fails for and exits with 3 when some of them failed.

### systemd units come from tt service systemd

The units `tt pack` of `tt` 2.x put into packages ran `tt -L <environment>
start` as forking services, and do not work with `tt` 3.0. For an application
installed in the system scope, `tt service systemd install <app>` writes a
template unit `<app>@.service`, which runs one instance with
`tt instance start --foreground`, and a drop-in for every instance with the
`restart` and `stop_timeout` of its record; `systemctl` starts and stops the
instances then, and `tt instance start`, `stop`, `restart` and `kill` refuse
them. See [Running instances under systemd](../README.md#running-instances-under-systemd).

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
