# Install an application for a user

The user scope runs an application without root: a test stand, a
developer's machine, a host where tt is not installed system-wide. Its root
is `~/.local/share/tarantool` on Linux, or `TT_USER_ROOT`. The same steps
work for the system scope with `--scope system`, run as root.

You need the archive of the application, made by `tt package pack` (see
[Your first package](../get-started/first-package.md)).

## Install

```console
$ tt package install --scope user kv-api-1.0.0-linux-amd64.tt
installed kv-api 1.0.0 into user scope
$ tt package list --scope user
NAME    ACTIVE  VERSIONS  DESCRIPTION
kv-api  1.0.0   1.0.0
```

The archive becomes the version `versions/kv-api/kv-api_1.0.0/` of the
scope, and `apps/kv-api` points to it. The Tarantool and the tt the archive
bundles go into `versions/` of the scope; no network is needed.

## Run

No project is needed: run the commands from any directory.

```console
$ tt instance start kv-api:* --scope user
$ tt instance status kv-api:* --scope user
$ tt instance logs kv-api:kv-1 --scope user --follow
$ tt connect kv-api:kv-1 --scope user
```

The instances run from the default template of the version, and keep their
data in `var/lib/kv-api/` of the scope.

## Describe this host

The templates of a version are part of the package; an upgrade replaces
them. To set what runs on this host - which instances, which ports, which
passwords - write the pair of the host into `etc/<app>/` of the scope. It
wins over the templates:

```console
$ mkdir -p ~/.local/share/tarantool/etc/kv-api
$ cd ~/.local/share/tarantool/etc/kv-api
$ $EDITOR config.yaml       # the cluster config of this host
$ $EDITOR instances.yaml    # what runs here, such as: kv-1: {restart: on-failure}
$ tt instance start kv-api:* --scope user
```

The pair of the host needs `instances.yaml`: it says which instances of the
cluster run on this host. See [Instances](../concepts/instances.md#the-pair).

## Upgrade

```console
$ tt package install --scope user --upgrade kv-api-1.1.0-linux-amd64.tt
$ tt package list --scope user
NAME    ACTIVE  VERSIONS      DESCRIPTION
kv-api  1.1.0   1.0.0, 1.1.0
$ tt instance restart kv-api:* --scope user --yes
```

`--upgrade` installs only a version newer than every installed one. The new
version becomes active at once, but a running instance keeps running the
version it started from, also when its watchdog restarts it after a crash;
`tt instance restart` moves it to the new one.

## Roll back

There is no rollback command. Reinstalling a version that is installed
needs `--force`, which is refused while an instance of the application
runs, so a rollback stops the instances:

```console
$ tt instance stop kv-api:* --scope user --yes
$ tt package install --scope user --force kv-api-1.0.0-linux-amd64.tt
$ tt instance start kv-api:* --scope user
```

## Uninstall

```console
$ tt instance stop kv-api:* --scope user --yes
$ tt package uninstall kv-api --scope user --yes
removed kv-api from user scope: versions 1.0.0, 1.1.0
```

`uninstall` refuses while an instance runs. It removes every version and the
`apps/` link, and keeps the data in `var/`, the pair of the host in
`etc/kv-api/`, and the runtimes in `versions/`, which other applications may
use.
