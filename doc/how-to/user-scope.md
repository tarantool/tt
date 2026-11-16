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

`tt instance status --scope user` with no target shows every instance of
every application installed in the scope; an application whose description
cannot be read is warned of and left out.

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
installed kv-api 1.1.0 into user scope
   ⚠ 2 instances of kv-api run 1.0.0: kv-1, kv-2
   ⚠ to move them to 1.1.0: tt instance restart kv-api:kv-1,kv-api:kv-2 --scope user
$ tt package list --scope user
NAME    ACTIVE  VERSIONS      DESCRIPTION
kv-api  1.1.0   1.0.0, 1.1.0
$ tt instance restart kv-api:* --scope user --yes
```

Every install makes the version it installs active at once, newer or older
than the active one: a plain `tt package install` of a newer archive is an
upgrade already. `--upgrade` only adds a check: it installs a version newer
than every installed one, and does nothing otherwise.

A running instance keeps running the version it started from, also when its
watchdog restarts it after a crash; `tt instance restart` moves it to the
active one. `install` names the instances that do not run the version it
installed, and `tt instance status --details` shows the version every
running instance runs:

```console
$ tt instance status kv-api:* --scope user --details
Version of kv-api:kv-1: 1.0.0, restart pending: 1.1.0 is active
...
```

In `-o json` the same is the `version` and `restart_pending` of an instance.

## Roll back

The earlier versions stay installed next to the active one.
`tt package switch` makes one of them active again, with no archive and with
the instances running:

```console
$ tt package switch kv-api 1.0.0 --scope user
switched kv-api from 1.1.0 to 1.0.0 in user scope
   ⚠ 2 instances of kv-api run 1.1.0: kv-1, kv-2
   ⚠ to move them to 1.0.0: tt instance restart kv-api:kv-1,kv-api:kv-2 --scope user
$ tt instance restart kv-api:* --scope user --yes
```

As after an upgrade, the running instances stay on the version they started
from until `tt instance restart`. `apps/kv-api` is replaced in one rename, so
an instance that starts during the switch gets one version or the other.

Without a version, `tt package switch kv-api --scope user` offers the
installed versions to choose from on a terminal; off a terminal it lists
them, the active one marked with `*`, and fails. Switching to the active
version does nothing. A version that is not installed is refused, and so is
one whose bundled Tarantool has been removed from the scope with
`tt binary uninstall`: the error names the `tt binary install` command that
brings it back. The project scope holds one version of every package, so
there `tt package switch` is refused: reinstall the version wanted with
`tt package install --force`.

Forward again is a switch as well, to the newer version:
`tt package switch kv-api 1.1.0 --scope user`.

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
