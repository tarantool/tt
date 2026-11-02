# A cluster on your laptop

This tutorial creates a project from the built-in `cluster` template, runs
its four instances on localhost, connects to one of them and cleans up. It
takes a few minutes and needs no root.

## Create the project

```console
$ tt create cluster --name myapp
$ cd myapp
```

`tt create` asks for the number of replica sets, of replicas in each and for
the user and the password of the cluster; press Enter to take the defaults:
two replica sets of two instances, user `client` with password `secret`. Add
`-s` to take them without asking.

The project is the directory with the code of the application and the
instance template it runs from:

```text
myapp/
├── .templates
│   └── default
│       ├── config.yaml      # the cluster config: users, replica sets, ports
│       └── instances.yaml   # what runs on this host
└── app.lua
```

`instances.yaml` holds one record, `"*": {}`, which stands for every
instance of the topology of `config.yaml`. See
[Instances](../concepts/instances.md).

## Install Tarantool into the project

```console
$ tt binary install tarantool
```

This installs the latest Tarantool Community release into `.tarantool/` of
the project. The instance commands of the project run it, whatever
`tarantool` is on `PATH`. To run it yourself, `source .env/activate.sh`
puts it first on `PATH`.

## Start the instances

```console
$ tt instance start
   • Starting an instance [myapp:replicaset-001-a]...
   • Starting an instance [myapp:replicaset-001-b]...
   • Starting an instance [myapp:replicaset-002-a]...
   • Starting an instance [myapp:replicaset-002-b]...
$ tt instance status
 INSTANCE                STATUS   PID      MODE  CONFIG  BOX      UPSTREAM
 myapp:replicaset-001-a  RUNNING  1303991  RW    ready   running  --
 myapp:replicaset-001-b  RUNNING  1303992  RO    ready   running  --
 myapp:replicaset-002-a  RUNNING  1303993  RW    ready   running  --
 myapp:replicaset-002-b  RUNNING  1303994  RO    ready   running  --
```

Each instance runs in the background under a watchdog of tt. Inside the
project the commands act on its instances; an instance is named
`<application>:<instance>`, and the application is named after the project
directory.

The instances keep their data, logs and sockets under `.tarantool/var/`:

```text
.tarantool/var/
├── lib/myapp/replicaset-001-a/   # *.snap, *.xlog
├── log/myapp/replicaset-001-a/   # tt.log
└── run/myapp/replicaset-001-a/   # tt.pid, tarantool.pid, tarantool.control
```

## Connect

`tt connect myapp:replicaset-001-a` opens an interactive console on an
instance through its console socket. With `-f-` it runs the Lua it reads
from the standard input instead:

```console
$ echo "box.info.ro" | tt connect myapp:replicaset-001-a -f-
---
- false
...
```

An instance is reached over the network too, with the user of the cluster
config:

```console
$ echo "return box.space._space:count()" | tt connect client:secret@127.0.0.1:3301 -f-
```

<!-- TODO(writers): a screenshot or a transcript of the interactive console. -->


## Read the logs

```console
$ tt instance logs myapp:replicaset-001-a -n 3
$ tt instance logs myapp:replicaset-001-a --follow
```

## Stop and clean up

```console
$ tt instance stop --yes
$ tt instance clean --yes
```

`stop` and `clean` ask for a confirmation, which `--yes` skips. `clean`
removes the data and the logs of the instances that are not running.

## Next

- [Your first package](first-package.md): write an application of your own,
  with dependencies and tests.
- [Instances](../concepts/instances.md): how tt finds what to run.
- [`tt instance`](../reference/commands/instance.md): every flag.
