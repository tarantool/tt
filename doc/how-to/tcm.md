# Run TCM

TCM, the [Tarantool Cluster Manager](https://www.tarantool.io/en/doc/latest/tooling/tcm/),
is the web interface of the clusters of Tarantool Enterprise. In tt every
application has a TCM of its own: a record with `kind: tcm` in its
`instances.yaml`, which runs as any instance does. `tt instance start`, `stop`,
`restart`, `status` and `logs` manage it, and `tt service systemd` makes a unit
for it. tt finds the binary of TCM, writes its config and gives its `admin`
user a password.

TCM is a product of Tarantool Enterprise: the project runs on Tarantool
Enterprise, and TCM is downloaded with the credentials of the customer zone of
tarantool.io.

## Declare TCM

The project says it runs on Tarantool Enterprise, with the flavor `[ee]` in
`[platform].tarantool` of `app.manifest.toml`:

```toml
[platform]
tarantool = ">=3.0.0[ee]"
tt = ">=3.0.0"
```

`tt tcm init` adds TCM to the `instances.yaml` of a template, `default` unless
`--template` names another, and the version of TCM the package bundles to
`[platform].tcm`:

```console
$ tt tcm init --version 1.3
   • Declared TCM as "tcm" in .templates/default/instances.yaml.
   • Set [platform].tcm = ">=1.3" in app.manifest.toml.
   • Next:
         tt binary install tcm <version>   # Tarantool Enterprise: --credentials-file with the credentials of the customer zone
         tt instance start myapp:tcm
         tt instance status myapp:tcm --details   # the URL, the user and the password file
$ cat .templates/default/instances.yaml
# TCM, the Tarantool Cluster Manager of the application, added by tt tcm init.
tcm:
  kind: tcm
  http: 127.0.0.1:8080
  # config: tcm.yaml   # settings of TCM of your own, relative to this file
```

- `--version` takes a version, which becomes the lowest one allowed (`1.3`
  makes `>=1.3`), or a constraint (`">=1.3,<2"`). Without it a
  `[platform].tcm` that is set is kept, and one that is not is set from the
  newest TCM installed with `tt binary install`; with none installed, the
  command asks for `--version`, as only the customer zone of tarantool.io
  lists the versions of TCM.
- `--http` is the address TCM listens on, `127.0.0.1:8080` by default.
- A template that has TCM already is left as it is, and the command says
  so. A template of a `config.yaml` alone gets `"*": {}` in front of TCM, so
  that it keeps running every instance of its topology.
- A project of Tarantool Community Edition is refused:

  ```console
  $ tt tcm init --version 1.3
     ⨯ TCM is Tarantool Enterprise only, and [platform].tarantool of app.manifest.toml is ">=3.0.0", with no flavor, which is [ce]: Tarantool Community Edition; switch the project to Tarantool Enterprise, tarantool = ">=3.0.0[ee]", to run TCM
  ```

  `tt instance start` and `tt instance check` refuse a `kind: tcm` record of
  such a project the same way, and so does `tt package pack`. A project
  without `app.manifest.toml` says no edition: TCM runs there, with a
  warning.

`"*"` stands for the instances of the topology of `config.yaml`, and TCM is
not one of them: it is named. A bare `tt instance start` starts every
instance of the pair, TCM among them.

## Install TCM

`tt instance start` runs the TCM the package bundles, as
[`tt package install`](user-scope.md) put its `_runtime/tcm` into
`<root>/versions/tcm/`, and otherwise the active `<root>/bin/tcm` of the
scope, which `tt binary install` sets:

```console
$ tt binary install tcm 1.3.1-0-g074b5ffa --credentials-file ./credentials
```

Without one, the start says where it looked:

```console
$ tt instance start
   ⨯ instance "tcm" of "myapp": no TCM to run: there is no /home/user/myapp/.tarantool/bin/tcm; install it with tt binary install tcm <version> (a version [platform].tcm allows, ">=1.3"); TCM is a product of Tarantool Enterprise, downloaded with the credentials of the customer zone of tarantool.io (--credentials-file)
```

See [Install binaries](binaries.md) for the sources of TCM.

## Start TCM and log in

```console
$ tt instance start
   • Starting an instance [myapp:tcm]...
   • The instance myapp:tcm is up.
   • TCM: http://127.0.0.1:8080 (user admin, password in /home/user/myapp/.tarantool/var/lib/myapp/tcm/admin.secret)
$ cat .tarantool/var/lib/myapp/tcm/admin.secret
```

TCM is up once its HTTP address answers. Open the URL and log in as
`admin` with the password in the file. tt generates the password on the
first start, 32 letters and digits, into `admin.secret` with the mode 0600,
and gives it to TCM at every start as `TCM_SECURITY_BOOTSTRAP_PASSWORD`, the
password of the first login of `admin`; no other file holds it, the config
of TCM and the logs included. A restart keeps it. TCM's documentation
describes the setting as being for the first login only: change the password
in TCM after you log in. The file keeps the first password, which TCM no
longer takes once it is changed.

`tt instance status --details` tells the same, and what TCM keeps its data
in; `-o json` gives it as `tcm`:

```console
$ tt instance status --details
Watchdog of myapp:tcm: PID 118136
Process of myapp:tcm: PID 118148, started at 2026-11-17 10:02:11
Restarts of myapp:tcm: 0
TCM myapp:tcm: http://127.0.0.1:8080 (user admin, password in /home/user/myapp/.tarantool/var/lib/myapp/tcm/admin.secret)
  • storage: the embedded etcd of TCM, http://127.0.0.1:45925, with its data in /home/user/myapp/.tarantool/var/lib/myapp/tcm/etcd
  • the application myapp has no cluster config in a config storage, which is the only place TCM reads one from: TCM shows the cluster myapp from the embedded etcd of TCM http://127.0.0.1:45925, prefix "/myapp", empty until a cluster config is published there; keep the cluster config in etcd (config.etcd in config.yaml) and publish it with tt cluster publish myapp for TCM to see and manage the cluster

 INSTANCE   STATUS   PID     MODE  CONFIG  BOX  UPSTREAM
 myapp:tcm  RUNNING  118148        --      --   --
```

TCM writes its log to its standard output, which goes to `tt.log` with
everything else of the instance:

```console
$ tt instance logs myapp:tcm
$ tt instance stop myapp:tcm
```

## The password of your own

A password you set wins over the one tt generates, and tt then writes no
`admin.secret`. In the order TCM takes them:

- `TCM_SECURITY_BOOTSTRAP_PASSWORD` in the `env` of the record, or in the
  environment of tt;
- `security.bootstrap-password` in the config of your own (`config`, below);
- `password_file: <file>` in the record: a file with the password, relative
  to the directory of `instances.yaml`. tt reads it at every start of TCM.

`tt instance status --details` names where the password is, never the
password. `tt package pack` refuses a `password_file` inside the template,
which would go into the package with it; keep the file next to the pair of
the host or outside of the project.

## What TCM works with

tt writes the config of TCM into `<root>/var/run/<app>/<tcm>/tcm.yaml` at
every start, with the mode 0600, and runs `tcm -c=<that file>`:

- `http.host` and `http.port` from `http` of the record;
- the storage of TCM: the config storage of the application, when its
  `config.yaml` points to one (`config.etcd`, or `config.storage` for a
  Tarantool config storage), with its endpoints and credentials and the
  prefix `/tcm/<app>`, apart from the cluster config and from the TCMs of
  other applications that share the storage. An application without one gets
  the embedded etcd of TCM, with its data in `<root>/var/lib/<app>/<tcm>/etcd`
  and on two free ports of `127.0.0.1` tt chooses on the first start, so that
  it takes neither the port of an etcd of the host nor that of the TCM of
  another application;
- `initial-settings.clusters`: the cluster of the application, named after
  it, with the config storage and the prefix of `config.yaml`, in place of the
  default cluster of TCM;
- the log to the standard output, without colors.

The storage of the application, for `config.yaml` as below:

```yaml
config:
  etcd:
    endpoints: [http://127.0.0.1:2379]
    prefix: /myapp
```

```console
$ tt instance status myapp:tcm --details
TCM myapp:tcm: http://127.0.0.1:8080 (user admin, password in /home/user/myapp/.tarantool/var/lib/myapp/tcm/admin.secret)
  • storage: etcd http://127.0.0.1:2379, prefix "/tcm/myapp", the config storage of the application
```

TCM reads the config of a cluster from a config storage only. An application
whose cluster config is the file `config.yaml` has the cluster in TCM, but
empty: it points to the embedded etcd of TCM under `/<app>`, where nothing is
published, and `tt instance status --details` says so. To see and manage the
cluster in TCM, keep its config in etcd: point `config.yaml` to it with
`config.etcd`, publish the config with `tt cluster publish <app>`, and restart
TCM. The storage of TCM then moves to that etcd as well.

TCM applies `initial-settings` once, on its first start with an empty
storage, as its documentation says: a TCM that has started already keeps
what it has, the cluster and the `admin` user among them, whatever the pair
says next. `tt instance clean <app>:<tcm>` removes the data directory, the
embedded etcd and `admin.secret` with it, and the next start is a first one.

## Settings of your own

`config` in the record names a YAML file of settings of TCM, relative to the
directory of `instances.yaml`, which tt merges over the config it writes:

```yaml
tcm:
  kind: tcm
  http: 0.0.0.0:8080
  config: tcm.yaml
```

```yaml
# .templates/default/tcm.yaml
mode: development
http.tls:
  enabled: true
  cert-file: /etc/tcm/cert.pem
  key-file: /etc/tcm/key.pem
initial-settings:
  clusters:
    - name: myapp
      tarantool-connection:
        username: admin
        password: secret
```

- A group is merged key by key, anything else replaces what tt wrote. A dotted
  key such as `http.tls` is a group.
- An item of `initial-settings.clusters` named after the application
  completes the cluster tt declares, here with the credentials TCM connects
  to the instances with; an item of another name is one more cluster.
- `tt instance status` and the start take the URL from the result, so the
  URL above is `https://127.0.0.1:8080`.

The `env` of the record reaches TCM too, and TCM ranks its `TCM_*` variables
above its config file. `tcm generate-config` of TCM prints every setting with
its default; the
[configuration reference](https://www.tarantool.io/en/doc/latest/tooling/tcm/tcm_configuration_reference/)
describes them.

## Under systemd

TCM is an instance of the application: `tt service systemd install <app>`
makes a unit for it as for the others, `<app>@tcm.service`, with `Restart=`
and `TimeoutStopSec=` from `restart` and `stop_timeout` of its record. See
[Run instances under systemd](systemd.md).

## From `tt tcm start`

`tt tcm start`, `stop`, `status` and `log` are gone. They ran a TCM of the
working directory, with a `tcm.yaml` of your own:

| Was | Now |
| --- | --- |
| `tt tcm start [--watchdog]` | `tt instance start <app>:tcm`, always under a watchdog, which restarts TCM as `restart` of its record says |
| `tt tcm stop` | `tt instance stop <app>:tcm` |
| `tt tcm status` | `tt instance status <app>:tcm [--details]` |
| `tt tcm log [-f]` | `tt instance logs <app>:tcm [--follow]` |
| `tt tcm start --path <tcm>` | the TCM of the package or of the scope; `tt binary install tcm --local-dir <dir>` installs one from a `tcm-*.tar.gz` archive in `<dir>` |
| a `tcm.yaml` of your own | `config: tcm.yaml` in the record, merged over the config tt writes |
