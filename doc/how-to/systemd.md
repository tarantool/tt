# Run instances under systemd

`tt service systemd` makes the systemd units that run the instances of an
application installed in the system scope, `<root>/apps/<app>`, from its pair:
the pair of the host in `<root>/etc/<app>/`, or else the default template of
the installed version. Units are made for the system scope only; the user
scope and projects are refused as not supported. `<app>` names the
application as the instance commands name it: inside a project of the same
name it is the project, and `--scope system` picks the installed one.

## Steps

Run as root. The host needs a tt 3 and the archive of the application.

```console
# tt package install --scope system kv-api-1.0.0-linux-amd64.tt
# tt binary switch tt 3.0.0 --scope system     # the tt the archive bundles
# mkdir -p /opt/tarantool/etc/kv-api           # the pair of this host
# $EDITOR /opt/tarantool/etc/kv-api/config.yaml
# $EDITOR /opt/tarantool/etc/kv-api/instances.yaml
# install -d -o tarantool -g tarantool /opt/tarantool/var
# tt service systemd install kv-api
# systemctl daemon-reload
# systemctl enable --now kv-api@kv-a.service kv-api@kv-b.service
```

1. Install the application into the system scope, `/opt/tarantool` (see
   [Packages](../concepts/packages.md#installing)). Its runtime goes into
   `/opt/tarantool/versions/`.
2. Give the scope a tt of its own, `/opt/tarantool/bin/tt`, which the units
   run: switch to the one the archive bundles, or
   `tt binary install tt --scope system`.
3. Optionally, write the pair of the host; without it the units run the
   default template of the installed version.
4. Make `var/` of the scope writable by the user the units run as,
   `tarantool` by default.
5. Install the units and start them with `systemctl`.

`tt instance status` shows the unit of every instance:

```console
# tt instance status kv-api:*
 INSTANCE     STATUS   UNIT                 PID  MODE  CONFIG  BOX      UPSTREAM
 kv-api:kv-a  RUNNING  kv-api@kv-a.service  200  RW    ready   running  --
 kv-api:kv-b  RUNNING  kv-api@kv-b.service  207  RO    ready   running  --
# tt instance stop kv-api:kv-a --yes
   ⨯ kv-api:kv-a is managed by systemd through the unit kv-api@kv-a.service: use systemctl stop kv-api@kv-a.service
```

<!-- Verified on 2026-10-27 (1482dea6) in a container with systemd; re-check
before release. -->

## The commands

```text
tt service systemd generate myapp                    # print the units
tt service systemd generate myapp --unit-dir ./units # write them into ./units
tt service systemd install myapp                     # into /etc/systemd/system
tt service systemd install myapp --run-as tt:tarantool
tt service systemd uninstall myapp
```

## The units

The template unit `myapp@.service` runs one instance of `myapp`:

```ini
[Unit]
Description=Tarantool application myapp, instance %i
After=network.target

[Service]
Type=simple
User=tarantool
Group=tarantool
Environment=TT_SYSTEM_ROOT=/opt/tarantool
Environment=TT_SYSTEMD_UNIT=%n
ExecStart=/opt/tarantool/bin/tt instance start --foreground --scope system myapp:%i
KillMode=mixed
KillSignal=SIGTERM
SendSIGKILL=yes
Restart=no
RestartSec=5s
TimeoutStopSec=35s
LimitNOFILE=65535
LimitCORE=infinity

[Install]
WantedBy=multi-user.target
```

and every instance gets a drop-in, `myapp@<instance>.service.d/tt.conf`, with
what differs between the instances:

```ini
[Service]
Restart=on-failure
TimeoutStopSec=65s
```

- `tt instance start --foreground` does not restart the instance: it exits
  with the code of the instance, and systemd restarts it as `Restart=` says,
  which is the `restart` field of the record: `no`, `on-failure` and `always`
  are the values of systemd.
- `tt` stops the instance itself: it sends the instance its stop signal and
  kills it once its `stop_timeout` is up. So the stop signal of systemd goes
  to `tt` alone (`KillMode=mixed`), and `TimeoutStopSec=` is the
  `stop_timeout` of the record, 30 seconds by default, plus 5 seconds for `tt`
  to kill the instance and exit: systemd kills nothing before `tt` does. A
  stop in time ends the unit with 0, a kill with 137.
- Tarantool instances, instances with `script` and external processes with
  `binary` run under the same template.
- `TT_SYSTEM_ROOT` is the root of the system scope the units are made in, so
  that `tt` under systemd finds the application there. `ExecStart=` runs
  `<root>/bin/tt`, the `tt` of the scope, which
  `tt binary install tt --scope system` installs; without it the units run the
  `tt` that makes them, with a warning.
- The instances run as the user and the group `--run-as <user>[:<group>]`
  names, `tarantool:tarantool` by default, the user the deb and rpm packages
  of `tt` create; without a group the group is the user's name, and a numeric
  id is taken as well. `<root>/var` must be writable by them. `install`
  records the value with the units, and an `install` over them without the
  flag keeps it; it warns when the host has no such user or group. Settings of
  your own go into a drop-in of your own, `myapp@.service.d/override.conf`;
  `Restart=` and `TimeoutStopSec=` come from `instances.yaml`.
- An application or instance name that a unit name would have to escape, a
  user or a group that `User=` and `Group=` would have to escape, and a root or
  a path of `tt` that a unit would have to quote, are refused.

## Install and update the units

`install` writes the units into `/etc/systemd/system`, or the directory
`--unit-dir` names, and records in `<root>/etc/<app>/systemd.yaml` which
instances the units manage. It does not run `systemctl`, it prints what to
run:

```text
systemctl daemon-reload
systemctl enable --now myapp@router-1.service myapp@storage-1.service
```

Run it again after the instances of the application change: it rewrites the
units and removes the drop-ins of the instances that are gone. It refuses a
unit file of the same name that it did not write, and an instance that runs
without a unit: stop it with `tt instance stop` first.

## The instance commands

`tt instance start`, `stop`, `restart` and `kill`, and
`tt replicaset rebootstrap`, refuse the instances the units manage and print
the `systemctl` command to use instead: a watchdog of `tt` next to the unit
would supervise the instance twice, and a stop behind the back of the unit
would leave it out of step. The start the unit itself runs is told by
`TT_SYSTEMD_UNIT`, the name of the unit. `tt instance status` reads the same
pid files and shows the unit, in the `UNIT` column and the `unit` field of
`-o json` and `-o yaml`; `logs`, `logrotate`, `check`, `clean` and `connect`
work as with any instance.

## Upgrade

Install the new version, then restart the units one by one, replicas first:

```console
# tt package install --scope system --upgrade kv-api-1.1.0-linux-amd64.tt
# systemctl restart kv-api@kv-b.service
# systemctl restart kv-api@kv-a.service
```

When the instances, `restart` or `stop_timeout` change in `instances.yaml`,
run `tt service systemd install kv-api` and `systemctl daemon-reload` again.

## Logs

The output of an instance goes to its `tt.log` and, through
`tt instance start --foreground`, to the journal:

```console
# journalctl -u kv-api@kv-a.service -f
# tt instance logs kv-api:kv-a -n 100
```

tt installs no logrotate configuration. A rule of your own reopens `tt.log`
with `tt instance logrotate`:

```text
/opt/tarantool/var/log/kv-api/*/tt.log {
    daily
    rotate 14
    missingok
    notifempty
    compress
    delaycompress
    sharedscripts
    postrotate
        TT_SYSTEM_ROOT=/opt/tarantool /opt/tarantool/bin/tt instance logrotate kv-api:* >/dev/null
    endscript
}
```

<!-- TODO(writers): the logrotate rule was not run through logrotate itself. -->

## Uninstall

`uninstall` removes the units and the record, from the directory they were
installed into, and needs only the record: it works for an application that
is gone. Without a record there is nothing to remove: it says the units are
not installed and exits with 0, so that it can be run again; a record that
cannot be read is an error. It refuses while an instance runs under its unit:
stop the units with `systemctl disable --now` first. A file it did not write
is left in place. `generate` writes the units only and records nothing: the instance
commands do not refuse the instances of units installed by hand.

