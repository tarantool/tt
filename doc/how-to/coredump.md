# Inspect a crash of Tarantool

When Tarantool crashes, or hangs and is made to quit with a dump, the kernel
writes a core dump: the memory of the process. `tt coredump pack`, run on
the host where Tarantool ran, turns the core into one archive that holds
everything gdb needs to read it: the core, the tarantool that dumped it, its
shared libraries and the gdb extensions, and, for an instance of tt, the
tails of its logs. For an instance it finds the core by itself, and
`tt coredump list` tells where the core of each crash is. `tt coredump
inspect`, run on a support host, opens the archive in gdb with the
extensions of LuaJIT and Tarantool loaded, which show the C and the Lua
frames of the crash, the fibers and the Lua heap. The support host needs no
Tarantool of that version and no access to the host of the customer.

This page is in two parts: what the customer runs to get the archive, and
what support runs to read it, with a Docker image that has all of it. The
flags of the commands are in [`tt coredump`](../reference/commands/coredump.md).

## On the customer's host

You need:

- tt 3 for Linux, amd64 or arm64;
- the tarantool that dumped the core, that file or another of its build, as
  the same release installed again: pack finds it, compares its build ID
  with that of the core and reads its version from the file. It runs no
  tarantool;
- bash, GNU tar and gzip, `getopt` of util-linux and the coreutils
  `basename`, `cat`, `date`, `dirname`, `ln`, `mkdir`, `mktemp` and `rm`.
  `hostname` is optional: without it the archive is named `...-hostname`.
  BusyBox does not do: its tar has no `--format=posix` and `--sparse`, and
  pack fails;
- read access to the core and to the shared libraries the process had
  loaded, and, for an instance, to the crashes its watchdog records, which
  are its user's alone: pack as the user the instance runs as, or as root;
- room for the archive in the directory it is made in, the working
  directory or `-d`.

gdb, `file`, Python, the sources and the network are not needed. Pack the
core soon after the crash: a shared library that a package upgrade has
replaced since the process loaded it is not packed, and a replaced
tarantool fails the build ID check.

### Where the kernel puts the core

`kernel.core_pattern` says what the kernel does with a core, for every
process of the host:

```console
$ cat /proc/sys/kernel/core_pattern
core
```

- A file name, as `core` here: the kernel writes the core into the working
  directory of the process, as `core.<PID>` when
  `/proc/sys/kernel/core_uses_pid` is 1. The working directory of an
  instance that `tt instance start` runs is the directory of its project.
  The `ulimit -c` of the shell that starts the instances must allow the
  core, 0 writes none: run `ulimit -c unlimited` before `tt instance start`.
  Instances under systemd, below, have their limit from the unit.
- `|/usr/lib/systemd/systemd-coredump ...`: systemd-coredump keeps the
  core, and `coredumpctl dump <PID> --output=core.<PID>` writes it out to a
  file, its holes as zeros. It keeps cores up to the sizes
  `ProcessSizeMax=` and `ExternalSizeMax=` of `/etc/systemd/coredump.conf`
  say, 32G by default in systemd 257 of Debian 13: an instance of more
  memory needs them raised.
- `|/usr/share/apport/apport ...`, on Ubuntu: apport keeps the core of a
  program of no package, as a tarantool of `tt binary install`, and of
  SIGQUIT, in `/var/lib/apport/coredump/`, as
  `core.<executable>.<UID>.<boot ID>.<PID>.<start time>`. Of another, it
  keeps the core inside its report in `/var/crash/`, and
  `apport-unpack <report>.crash <dir>` writes it out as `<dir>/CoreDump`.
- A pipe to tt, as below.

<!-- TODO(writers): the coredumpctl and apport-unpack commands were not run,
nor were tt coredump list and pack of an instance with systemd-coredump or
apport: the host of the walkthrough had core_pattern "core". The commands
are those of test/integration/coredump/coredump_helpers.py, and the places
those tt coredump list looks in. -->

For an instance of tt, `tt coredump list` and `tt coredump pack
<APP>:<INSTANCE>` find the core where the pattern puts it: go to
[The crashes of an instance](#the-crashes-of-an-instance). With a core
file, go to [Pack a core](#pack-a-core).

### Instances under systemd

The units of [`tt service systemd`](systemd.md) set `LimitCORE=infinity`:
their instances dump cores whatever the `ulimit -c` of a shell is, and the
core goes where `core_pattern` says, as for any process. A drop-in of your
own turns the cores off: for every instance of `myapp` in
`/etc/systemd/system/myapp@.service.d/override.conf`, which
`systemctl edit myapp@.service` writes, or for one instance in
`myapp@<instance>.service.d/override.conf`:

```ini
[Service]
LimitCORE=0
```

After a file written by hand, run `systemctl daemon-reload`; the limit
takes effect at the next start of a unit. `tt service systemd install`
keeps the file: it removes only the files it wrote. The drop-ins of the
template and of an instance apply in the order of their names, and
`tt.conf`, the one tt writes for every instance, comes after
`override.conf`: its `Restart=` and `TimeoutStopSec=`, which come from
`instances.yaml`, win over those of your drop-in, and your `LimitCORE=`,
which `tt.conf` does not set, stays.

<!-- Checked on 2026-11-24 with user units of systemd 259 of the same
layout, a template with LimitCORE=infinity, an override.conf of the template
or of one instance and a tt.conf of the instance: systemctl show gave
LimitCORE=0 and the Restart= of tt.conf; systemctl edit wrote
<template>.d/override.conf. Not run with the units of tt as root. -->

### Pack every crash as it happens

With `kernel.core_pattern` a pipe to tt, a crash of Tarantool is packed by
the time the support engineer asks for it. As root, in
`/etc/sysctl.d/50-coredump.conf`, the name of the file of systemd-coredump
in `/usr/lib/sysctl.d/`, which this one replaces, with the path of tt on the
host:

```ini
kernel.core_pattern = |/usr/bin/tt coredump pack -d /var/core -p %p -t %t
```

then `mkdir /var/core` and `sysctl --system`. The kernel runs tt as root
with the core on its standard input. tt stores it as
`/var/core/<comm>-core.<PID>.<time>` and, when the process is a tarantool,
packs it with the executable of the process into
`/var/core/tarantool-core-<PID>-<time>-<host>.tar.gz`; both are root's
alone, 0600. The core of any other program of the host that crashes is
stored and not packed, so `/var/core` needs room for those too. The stored
core stays next to the archive, which holds it: remove it once the archive
is sent. The archive of the pipe has no logs of the instance. `core_pattern`
is one for the whole host; this replaces systemd-coredump or apport there.

<!-- TODO(writers): the pipe was not set up on the host of the walkthrough,
which other users share. The command line of the pipe was run as root,
from / with a core on the standard input while /proc/PID was a tarantool of
the same build, as test_coredump_pack_pipe does. With PATH set it stored
the core and made the archive, both 0600 under a umask of 022. With an
empty environment, which the kernel gives a pipe of core_pattern, it stored
the core and did not pack it: exec: "bash": executable file not found in
$PATH. Check the page again once tt runs the pack script without PATH. -->

### The crashes of an instance

The watchdog of an instance, the tt that `tt instance start` leaves
running, records every exit of its process by a signal, a crash, in
`crashes.json` of the run directory of the instance: when it was, the PID,
the signal and whether the kernel dumped a core. It keeps the last 10,
through the restarts of the instance, a stop and the next start, for its
own user alone. A crash is in the log of the instance as well, after the
backtrace Tarantool writes as it dies:

```console
$ tt instance logs kv:instance001 -n 3
kv:instance001: #12 0xeaa17e in coro_init+62
kv:instance001: Watchdog 2026/11/24 04:01:04 (WARN): tarantool, PID 3030, has ended: it was killed by signal 6 (aborted), core dumped.
kv:instance001: Watchdog 2026/11/24 04:01:04 (INFO): the Instance has shutdown.
```

Tarantool reports a crash, a SIGSEGV here, and then aborts, so the core is
of signal 6. `core dumped` says that the kernel wrote a core; without it the
`ulimit -c` of the process was 0, or the kernel could not write the core
where `core_pattern` says. `tt instance status --details` tells the last
crash of every instance, running or not:

```console
$ tt instance status --details
Last exit of kv:instance001: it was killed by signal 6 (aborted), core dumped, at 2026-11-24 04:01:04
  The last lines of /home/user/kv/.tarantool/var/log/kv/instance001/tt.log:
...
Last crash of kv:instance001: PID 3030, it was killed by signal 6 (aborted), core dumped, at 2026-11-24 04:01:04
  tt coredump list kv:instance001 tells where its core is.
...
```

`tt coredump list` lists the crashes, the newest first, with where the core
of each is now and its size, or why it is not found. It takes the instances
as `tt instance status` does: those of the project it runs in, the targets
given, or with `--scope user` or `system` those installed there. `-o json`
and `-o yaml` give the same.

```console
$ tt coredump list
INSTANCE        CRASHED AT           PID   SIGNAL   CORE
kv:instance001  2026-11-24 04:01:04  3030  SIGABRT  /home/user/kv/core.3030, 1.1 GiB
   • The cores are looked for where kernel.core_pattern "core" puts them now. module=coredump
   • tt coredump pack <APP>:<INSTANCE> packs the core of the last crash of an instance. module=coredump
```

The size is that of the file, as `ls -l` shows it: most of a core is holes,
which take no room (see [What the archive holds](#what-the-archive-holds)).
The crashes of an instance of another user cannot be read: list warns of
them, with why. The core is looked for where `core_pattern` puts it now,
by the PID and the time of the crash:

- a file name: the file the pattern names, with `%p` the PID, `%s` the
  signal, `%h` the host and any name for the other specifiers, `.PID` of
  `core_uses_pid` appended to a pattern without `%p`, relative to the
  working directory of the process; the one written last about the time of
  the crash;
- systemd-coredump: the entry of the PID that `coredumpctl` lists nearest
  the time of the crash;
- the pipe to tt: the core it stored in the directory of its `-d`, and the
  archive it packed of it;
- apport: the core in `/var/lib/apport/coredump/`, else `core.<PID>` or
  `core` in the working directory of the process, where an older apport
  wrote it; else list names the report of the time in `/var/crash/`, to
  `apport-unpack`;
- a pipe to another program is named, and its cores are not looked for.

A core file of another PID than the crash, by the PID the core records, is
not taken: the instances of an application share a working directory, and
with a pattern without `%p` the file `core` there is the core of the one
that crashed last.

`tt coredump pack <APP>:<INSTANCE>` packs the core of the last crash of the
instance that dumped one, found so, and says which crash it packs and where
the core came from:

```console
$ tt coredump pack kv:instance001
   • Packing the core of the crash of kv:instance001, PID 3030, SIGABRT, at 2026-11-24 04:01:04, from the file of kernel.core_pattern, /home/user/kv/core.3030. module=coredump
   • packing with /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool, the executable the core names module=coredump
   • Packing the log /home/user/kv/.tarantool/var/log/kv/instance001/tt.log as logs/tt.log of the archive. module=coredump
The resulting archive is located here: /home/user/kv/tarantool-core-3030-202611240401-db1.tar.gz
The archive contents are listed below:
tarantool-core-3030-202611240401-db1/etc/os-release
tarantool-core-3030-202611240401-db1/tarantool
tarantool-core-3030-202611240401-db1/coredump
tarantool-core-3030-202611240401-db1/version
tarantool-core-3030-202611240401-db1/lib/x86_64-linux-gnu/libdl.so.2
tarantool-core-3030-202611240401-db1/lib/x86_64-linux-gnu/librt.so.1
tarantool-core-3030-202611240401-db1/lib/x86_64-linux-gnu/libpthread.so.0
tarantool-core-3030-202611240401-db1/lib/x86_64-linux-gnu/libm.so.6
tarantool-core-3030-202611240401-db1/lib/x86_64-linux-gnu/libgcc_s.so.1
tarantool-core-3030-202611240401-db1/lib/x86_64-linux-gnu/libc.so.6
tarantool-core-3030-202611240401-db1/lib64/ld-linux-x86-64.so.2
tarantool-core-3030-202611240401-db1/lib/x86_64-linux-gnu/libthread_db.so.1
tarantool-core-3030-202611240401-db1/logs/tt.log
tarantool-core-3030-202611240401-db1/gdb.sh
tarantool-core-3030-202611240401-db1/ext/tarantool-gdb.py
tarantool-core-3030-202611240401-db1/ext/luajit_dbg.py

If you want to upload it, choose the available resource
and run the following command:
$ curl -T /home/user/kv/tarantool-core-3030-202611240401-db1.tar.gz <resource-uri>
   • Core was successfully packed: /home/user/kv/tarantool-core-3030-202611240401-db1.tar.gz module=coredump
```

The last line says where the archive is, on a terminal or not; the list of
the contents and the hint of `curl` are printed on a terminal only. The
archive is `tarantool-core-<PID>-<time>-<host>.tar.gz`, of the PID and the
time of the crash, to the minute; `-t` gives another time, in seconds since
the Epoch, and `-d` makes the archive in another directory than the working
one. A core of systemd-coredump is dumped by `coredumpctl`, the one of the
entry of the crash, into a hidden file of that directory, with its holes,
and removed once packed.

The archive has the logs of the instance in `logs/`: the last MiB of its
`tt.log`, which has what Tarantool writes to its standard error, the
backtrace of the crash among it, and `tarantool.log`, the file Tarantool
logs into when its config has `log.to: file`. The config of the instance
is not packed: it may hold passwords. The logs may hold the data of the
customer, as the core does: what the application and Tarantool logged.
`--no-logs` packs none:

```console
$ tt coredump pack kv:instance001 --no-logs
   • Packing the core of the crash of kv:instance001, PID 3030, SIGABRT, at 2026-11-24 04:01:04, from the file of kernel.core_pattern, /home/user/kv/core.3030. module=coredump
   • The logs of kv:instance001 are not packed: --no-logs. module=coredump
...
   • Core was successfully packed: /home/user/kv/tarantool-core-3030-202611240401-db1-2.tar.gz, since tarantool-core-3030-202611240401-db1.tar.gz is there already module=coredump
```

An archive of the name there already is kept, as the one of the crash
above: the same core packed again goes into `...-2.tar.gz`, then
`...-3.tar.gz`, and the last line says so.

A crash that dumped no core, or a core that is not found, is an error that
says why and names `tt coredump list` (see
[A hung instance](#a-hung-instance)). Ctrl-C, or SIGTERM as the stop of a
service sends it, stops pack, which removes what it was making, the archive
begun among it, and tt exits 130 or 143.

### Pack a core

A core file is packed by its path: the core of a tarantool that runs as no
instance of tt, or that of an earlier crash of an instance. Here in the
directory of the project:

```console
$ tt coredump pack core.3030
   • packing with /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool, the executable the core names module=coredump
...
   • Core was successfully packed: /home/user/kv/tarantool-core-3030-202611240402-db1.tar.gz module=coredump
```

The archive has no `logs/`. Its PID is that of the process of the core,
which the core records, unless `-p` gives it, and `N` for a core that
records none, as one of a 32-bit process. Its time is that of the pack,
unless `-t` gives the time of the dump.

Pack finds the tarantool by itself and says which it took. It is the
executable the core names, the file the process ran, when that file is
still there and has the build ID of the core. Otherwise it is the first with
that build ID of the `tarantool` of `PATH`, the one the instance commands
run in the project of the working directory, and those `tt binary install`
put into the project, user and system scopes: the active one of each scope,
then each of its versions. `-e` names the tarantool instead, and pack tries
no other; the pipe takes the executable of the process.

So, run in any directory of the host and whatever `PATH` has, pack takes
the tarantool the instance ran, as here, after the project went back to
Tarantool 3.8.2, which its instances run now:

```console
$ tt binary install tarantool 3.8.2
...
   • Made default by symlink "/home/user/kv/.tarantool/bin/tarantool"
...
$ tt coredump pack core.3030
   • packing with /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool, the executable the core names module=coredump
...
   • Core was successfully packed: /home/user/kv/tarantool-core-3030-202611240403-db1.tar.gz module=coredump
```

A tarantool of another build is refused. When none has the build, as once
the project has uninstalled 3.9.0, pack fails: it lists each tarantool it
tried and why it is not the one, and names the path the core records:

```console
$ tt binary uninstall tarantool 3.9.0
...
$ tt coredump pack core.3030
   ⨯ core dump file is produced by the different Tarantool executable: none of the tarantools tried dumped 'core.3030':
       /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool, the executable the core names: missing
       /home/user/kv/.tarantool/versions/tarantool/tarantool_3.8.2/tarantool, the tarantool tt runs here: the executable of the core has build ID 781dc56a9fb4e0f2e457995160fea91d4cd3fa72, the binary 77b6c017b00ea47bd50386524b503e4457420aa8
     specify the tarantool that dumped the core with -e <path>; the core records it as /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool
```

`tt binary install` of the same release installs the same build again, and
pack finds it in a scope by its build ID:

```console
$ tt binary install tarantool 3.9.0 --scope user
...
$ tt coredump pack core.3030
   • not packing with /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool, the executable the core names: missing module=coredump
   • packing with /home/user/.local/share/tarantool/bin/tarantool, the active tarantool of the user scope: it has the build ID of the core, 781dc56a9fb4e0f2e457995160fea91d4cd3fa72 module=coredump
...
   • Core was successfully packed: /home/user/kv/tarantool-core-3030-202611240404-db1.tar.gz module=coredump
```

A tarantool of the build anywhere else, as a copy, is given with `-e`, as
the error says; with `-e` pack prints no `packing with` line.

### A tarantool without debugging information

The archive holds the tarantool as the customer has it. One stripped of its
debugging information, DWARF, gives plain backtraces only: the `lj-*`
commands and the pretty-printers of Tarantool look its types up. Pack warns
of it, and finds the debug file a debuginfo or dbgsym package installs under
`/usr/lib/debug/.build-id/` and packs it, when there is one. For a release
of Tarantool Community 3.x, whose static builds are the same in the RPM and
the deb packages, the hint names the tarantool of the static deb, which
keeps its DWARF and which `tt binary install` installs. Here the instance
ran the `/usr/bin/tarantool` of a package, without DWARF:

```console
$ tt coredump pack core.3860
   • packing with /usr/bin/tarantool, the executable the core names module=coredump
   ⚠ the tarantool /usr/bin/tarantool has no debugging information, and no debug file of its build ID 781dc56a9fb4e0f2e457995160fea91d4cd3fa72 is under /usr/lib/debug: the archive will have none, and the lj-* commands of LuaJIT and the pretty-printers of Tarantool will not work on the core, plain backtraces will module=coredump
   ⚠ hint: the tarantool of the static deb of Tarantool 3.9.0, which `tt binary install tarantool 3.9.0` installs, has the debugging information: if its build ID is that of the core, 781dc56a9fb4e0f2e457995160fea91d4cd3fa72, pack the core with it, `tt coredump pack -e <its path>` module=coredump
...
   • Core was successfully packed: /home/user/kv/tarantool-core-3860-202611240404-db1.tar.gz module=coredump
$ mkdir ~/deb
$ cd ~/deb
$ tt binary install tarantool 3.9.0
   • Not inside a project: the project scope is /home/user/deb/.tarantool, in the current directory (see tt binary --help)
...
   • Made default by symlink "/home/user/deb/.tarantool/bin/tarantool"
   • Done.
...
$ cd ~/kv
$ tt coredump pack -e ~/deb/.tarantool/bin/tarantool core.3860
...
   • Core was successfully packed: /home/user/kv/tarantool-core-3860-202611240405-db1.tar.gz module=coredump
```

`-e` is needed here: pack takes the tarantool the core names when it has the
build ID, whether it has DWARF or not, and looks no further. The build ID
check of pack tells whether the one of `-e` is the same build. `tt binary
install` needs the network; support can do the same with an archive
already packed, as its hint says (see
[Warnings](#warnings-before-gdb-starts)). An Enterprise build, a
pre-release, a build of a commit past its tag and a build of a distribution
or of the user, which is a PIE, get no such hint: their build IDs are
others.

### A hung instance

An instance that does not answer is dumped with `tt instance kill --dump`:
tt sends it SIGQUIT, on which the kernel writes the core, and waits for it
to end. The instance is not started again. On a terminal it asks first;
`--yes` does not. The dump is a crash of the instance too, of SIGQUIT,
listed with those before it, and `tt coredump pack <APP>:<INSTANCE>` packs
its core:

```console
$ tt instance kill --dump kv:instance001 --yes
   • The instance kv:instance001 (PID = 4241) has been terminated with dump.
$ tt coredump list
INSTANCE        CRASHED AT           PID   SIGNAL   CORE
kv:instance001  2026-11-24 04:06:14  4241  SIGQUIT  /home/user/kv/core.4241, 97.1 MiB
kv:instance001  2026-11-24 04:04:49  3860  SIGABRT  /home/user/kv/core.3860, 1.1 GiB
kv:instance001  2026-11-24 04:01:04  3030  SIGABRT  /home/user/kv/core.3030, 1.1 GiB
...
$ tt coredump pack kv:instance001
   • Packing the core of the crash of kv:instance001, PID 4241, SIGQUIT, at 2026-11-24 04:06:14, from the file of kernel.core_pattern, /home/user/kv/core.4241. module=coredump
   • packing with /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool, the executable the core names module=coredump
...
   • Core was successfully packed: /home/user/kv/tarantool-core-4241-202611240406-db1.tar.gz module=coredump
```

Pack of an instance takes the core of its last crash that dumped one, and
no other: with that core gone, it fails, and the core of an earlier crash
is packed by its file, the path `tt coredump list` gives (see
[Pack a core](#pack-a-core)):

```console
$ tt coredump pack kv:instance001
   ⨯ the core of the crash of kv:instance001, PID 4241, SIGQUIT, at 2026-11-24 04:06:14: the core is not found: no file matches /home/user/kv/core.4241: it was removed or moved, or the kernel could not write it there; tt coredump list kv:instance001 lists the crashes of the instance and where their cores are
```

An instance a unit of systemd runs is refused, and tt names the
`systemctl kill --signal=SIGQUIT` command of its unit instead.

`gcore` of gdb, run as root, writes a core of a running process and leaves
it running, but gcore of gdb before 18.1, on glibc 2.42 or later with Linux
6.13 or later, writes the stacks of all threads but the main one as zeros.
Pack warns of such a core:

```console
# gcore -o core 52
...
warning: Memory read failed for corefile section, 1048576 bytes at 0x7c470a3ff000.
...
Saved corefile core.52
[Inferior 1 (process 52) detached]
# tt coredump pack core.52
   • packing with /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool, the executable the core names module=coredump
   ⚠ all 4 threads of the core besides the main one have nothing but zeros in the 1024 bytes above the stack pointer: the backtraces of those threads will be empty, and libthread_db may fail on the core module=coredump
   ⚠ hint: gcore of gdb older than 18.1 writes such stacks on glibc 2.42 or later with Linux 6.13 or later; take the core the kernel writes, or write it with gcore-pages of tarantool-gdb-tools module=coredump
...
   • Core was successfully packed: /root/tarantool-core-52-202611240420-db1.tar.gz module=coredump
```

Prefer `tt instance kill --dump`. Where the process must keep running,
`gcore-pages` of [tarantool-gdb-tools](https://github.com/buyemune-sys/tarantool-gdb-tools)
is a gcore that keeps the stacks. Take it at the commit `tt coredump inspect
--help` names:

```console
# curl -fsSLO https://raw.githubusercontent.com/buyemune-sys/tarantool-gdb-tools/d6510d757fbb104e5bbc7e36f5820438e225127a/gcore_pages_gdb.py
# gdb -p 52 -batch -ex 'source gcore_pages_gdb.py' -ex 'gcore-pages core.52'
...
Saved corefile core.52
gcore-pages: read again 41963520 bytes of 5 mappings, 6 pages unreadable
[Inferior 1 (process 52) detached]
# tt coredump pack core.52
   • packing with /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool, the executable the core names module=coredump
...
   • Core was successfully packed: /root/tarantool-core-52-202611240421-db1.tar.gz module=coredump
```

### What the archive holds

- `coredump`, the core: the memory of the process;
- `tarantool`, the executable that dumped it, and `version`, the version
  as the first line of its `--version` prints it, `Tarantool
  3.9.0-0-g8b9a0f59a95b` here, which pack reads from the file. When it
  cannot, pack warns and packs the core all the same, with a `version` of
  an unknown one: inspect then loads the gdb extensions tt embeds, which
  may not match the core, and names no commit of the sources;
- the shared libraries the process had loaded, under their paths, with
  `ld-linux` and `libthread_db`, which gdb loads from the archive in place
  of those of the support host;
- the debug file of the tarantool, under `usr/lib/debug/.build-id/`, when
  the host has one;
- of an instance, `logs/`, the tails of its `tt.log` and `tarantool.log`;
- `etc/os-release` of the host, `gdb.sh` and the gdb extensions of the
  release, `ext/`.

The Lua code of the application is not in it. `lj-bt` names the Lua frames
by their file and line, `/home/user/kv/kv/init.lua:20`: get the code of the
application of the version that crashed from the customer to read them.
Nor are the files the process had mapped that hold no code, as the locale
data of glibc: gdb warns that it cannot open them, and needs none of them
(see [Open the archive](#open-the-archive)).

The archive of the crash above is 44 MB and unpacks into files of 1.3 GB,
which take 133 MB of the disk. A core is as large as the memory the
process had mapped, 1.2 GB here, the 1.1 GiB of `tt coredump list`, but the
pages the process never touched are holes of the file, which take no room:
pack keeps them in the archive, and unpack and inspect keep them on the
disk, so the core takes 19 MB there. Most of the rest is the tarantool with
its debugging information, 110 MB. `ls -l` shows the size of the core, `du`
the room it takes. A copy that fills the holes, onto a file system without
them or by a tool that writes them out, takes the whole 1.2 GB.

Handle the archive as the data of the customer: the core holds what the
process had in memory, the Lua values among it, as the strings and the
tuples of the requests in flight, and the logs what was logged. The tuples
of memtx spaces are left out of it by default: `process.strip_core` of the
cluster config, `true` by default, leaves the memtx arena out of the core,
and gdb reads those tuples as zeros. With `strip_core: false` they are in
the core, and so is the data of the spaces.

The archive is its user's alone, 0600, as the core the kernel writes is.
`tt coredump unpack` and `inspect` keep the bits of the owner alone of the
files they extract, 0600 of the core and 0700 of the tarantool, and make
the directories 0700, whatever modes the entries of the archive have: give
them to others by hand where they need them.

### An archive of tt 2

tt 3 opens the archives of tt 2: their layout is the same, one directory
`tarantool-core-<PID>-<time>-<host>/` with `tarantool`, `coredump`,
`version`, `etc/os-release` and the libraries under their paths. tt 3 runs
its own `gdb.sh` and picks the extensions itself, by `version`, and leaves
the `gdb.sh` and `ext/` of the archive alone; it links the files the core
maps to those of the archive as it does for its own archives (see
[Open the archive](#open-the-archive)). On the customer's host, the
pack of tt 2 needs gdb and `file`, runs the tarantool for its version,
packs no debug file of `/usr/lib/debug` and no logs, and cannot be the pipe
of `kernel.core_pattern`, since it reads the core from a file only. It does
not look for the tarantool of the core: it takes the one of `-e`, else that
of the `bin_dir` of its `tt.yaml` or of `PATH`. Without `-p` it names the
archive `tarantool-core-N-...`, and it says where the archive is on a
terminal only. Have the customer pack with tt 3 where they can.

## On the support host

You need:

- Linux of the architecture of the core, amd64 or arm64, with gdb built
  with Python, 17.1 or later: the main Lua state of Tarantool 3.7 and
  later, `tarantool_L`, is thread-local, and gdb before 17.1 reads it
  through libthread_db only, which may not load (see
  [Warnings](#warnings-before-gdb-starts)). Ubuntu 26.04 has gdb 17.1;
  Ubuntu 24.04 has 15.1;
- tt 3 for Linux;
- room for the archive unpacked in `TMPDIR`, `/tmp` by default: 133 MB for
  the archive of 44 MB above (see
  [What the archive holds](#what-the-archive-holds)).

Or Docker, with the image below, which has all of it.

### The support image

Put a tt 3 for Linux next to the `Dockerfile`. A tt built from source needs
a base image whose glibc is not older than the one of the host it was built
on.

```console
$ mkdir coredump-image && cd coredump-image
$ cp "$(command -v tt)" .
```

`Dockerfile`:

```dockerfile
FROM ubuntu:26.04
RUN apt-get update \
 && apt-get install -y --no-install-recommends gdb \
 && rm -rf /var/lib/apt/lists/*
COPY tt /usr/local/bin/tt
USER ubuntu
WORKDIR /work
ENTRYPOINT ["tt", "coredump", "inspect", "--experimental"]
```

```console
$ docker build -t tt-coredump .
```

gdb runs as the user `ubuntu` of the image, not root, unless `--user`
names another, as below, and asks for no network: the extensions come from
tt. The gdb of Ubuntu reads the cores of its own architecture alone: build
the image on a host of the architecture of the core.

The image keeps the pagination of gdb. tt turns it off while gdb loads the
core and the extensions, whose lines run past a screen, and back on at the
prompt: a long output, as that of `thread apply all bt`, stops at `--Type
<RET> for more` as in any gdb. With `set pagination off` in a gdbinit,
`/etc/gdb/gdbinit` of the image or the `~/.gdbinit` of a host, there is no
pager at all: tt leaves it off.

Run it in the directory of the archive, which it sees as `/work`, as
yourself:

```console
$ docker run --rm -it -v "$PWD:/work" --user "$(id -u):$(id -g)" tt-coredump tarantool-core-3030-202611240401-db1.tar.gz
```

The archive is yours alone, 0600, as are the files `tt coredump unpack`
writes (see [What the archive holds](#what-the-archive-holds)): without
`--user` gdb runs as the user `ubuntu` of the image, of the uid 1000, which
reads them only when yours is 1000 too, and else inspect fails with
`failed to unpack: cannot open the archive: open
tarantool-core-3030-202611240401-db1.tar.gz: permission denied`. A uid the
image has no user of gets `/` as its home, which gdb and tt do without.

The archive is unpacked into `/tmp` of the container and removed when gdb
exits. Exit gdb with `quit`.

### Open the archive

On a host with gdb and tt, `tt coredump inspect --experimental <archive>`
is the same as the image. What it prints before the prompt of gdb:

```text
   • gdb extensions of tarantool-gdb-tools d6510d75, which tt embeds: luajit_gdb.py, tarantool_gdb.py, luajit_backtrace_gdb.py, gcore_pages_gdb.py module=coredump
   • The archive has the logs of the instance of the core in logs/, tt.log: tt coredump unpack tarantool-core-3030-202611240401-db1.tar.gz puts them into tarantool-core-3030-202611240401-db1/logs. module=coredump
   • gdb will warn of 2 files the core maps that are not in the archive, /usr/lib/x86_64-linux-gnu/gconv/gconv-modules.cache and /usr/lib/locale/C.utf8/LC_CTYPE: they hold no code gdb needs module=coredump
================================================================================
gdb shows no lines of the C code of Tarantool without its sources. Clone them
once, at the commit of the core:
  git clone https://github.com/tarantool/tarantool.git tarantool-8b9a0f59a95b
  git -C tarantool-8b9a0f59a95b checkout 8b9a0f59a95b
  git -C tarantool-8b9a0f59a95b submodule update --init --recursive
and give them with -s tarantool-8b9a0f59a95b.
================================================================================
GNU gdb (Ubuntu 17.1-2ubuntu1) 17.1
...
Reading symbols from /tmp/tt-coredump-1046434021/tarantool-core-3030-202611240401-db1/tarantool...
warning: Can't open file /usr/lib/x86_64-linux-gnu/gconv/gconv-modules.cache during file-backed mapping note processing
warning: Can't open file /usr/lib/locale/C.utf8/LC_CTYPE during file-backed mapping note processing
[New LWP 3030]
[New LWP 3033]
...
[Thread debugging using libthread_db enabled]
Using host libthread_db library "/tmp/tt-coredump-1046434021/tarantool-core-3030-202611240401-db1/lib/x86_64-linux-gnu/libthread_db.so.1".
Core was generated by `/home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool -n instance001 -c /home/user/kv/.templates/default/config.yaml'.
Program terminated with signal SIGABRT, Aborted.
...
LuaJIT debug extension is successfully loaded
lj-bt command initialized
(gdb)
```

- The first line names the extensions `--experimental` loads.
- The second says that the archive has the logs of the instance, and where
  `tt coredump unpack` puts them: inspect unpacks the archive into a
  directory it removes.
- gdb opens every file the core maps, for the pages of it the core does not
  hold, by the path it had on the customer's host, under the directory of
  the archive. The archive holds the tarantool and the shared libraries by
  other names, `tarantool` and the names the dynamic loader opened them by:
  tt links the paths the core records, as
  `home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool` and
  `usr/lib/x86_64-linux-gnu/libc.so.6`, to them, by relative symbolic links
  in that directory, before gdb starts. The files the process had mapped that
  hold no code, the locale data of glibc here, are not in the archive: the
  third line says how many, and gdb warns of each, `Can't open file ...
  during file-backed mapping note processing`. They do no harm.
- The block of `git clone` is how to get the sources of the Tarantool of
  the core, at its commit, into `tarantool-<commit>` of the working
  directory, once for the cores of that commit, and to give them with
  `-s`, as below. gdb shows the backtraces without them; with them it shows
  the lines of the C code. With `-s` the block is not printed.
- `Thread debugging using libthread_db enabled`, with the libthread_db of
  the archive, means gdb reads the thread-local variables of the core.
- `Core was generated by` is the command line of the process, and `Program
  terminated with signal` the signal it ended with: SIGABRT for a crash
  Tarantool reported, SIGQUIT for `tt instance kill --dump`.

`tt coredump inspect` takes the directory an archive unpacks into as well,
which is quicker to open more than once. `tt coredump unpack` unpacks it
into the working directory; in the image, as the user of the host, so that
the files are theirs:

```console
$ docker run --rm -v "$PWD:/work" --user "$(id -u):$(id -g)" --entrypoint tt tt-coredump coredump unpack tarantool-core-3030-202611240401-db1.tar.gz
   • Archive was successfully unpacked. module=coredump
$ docker run --rm -it -v "$PWD:/work" --user "$(id -u):$(id -g)" tt-coredump tarantool-core-3030-202611240401-db1
```

inspect of the directory says where the logs are,
`tarantool-core-3030-202611240401-db1/logs`. It makes the links of the
paths the core records in that directory, `home/` and `usr/` here, where it
has nothing at them; the next inspect finds them there. They point into the
directory, so it may be moved. A directory inspect cannot write to, as one
mounted read-only, is opened as it is: tt says that it cannot link the
files, and gdb warns that it cannot open them. The directory unpack makes
is 0700, and no other user reads it, the user `ubuntu` of the image among
them, for whom inspect says that `/work/<dir>/version file is missing`: run
the image with `--user` on it too.

`-s` gives gdb the sources of the Tarantool of the core, a checkout of its
commit with its submodules, so that it shows the lines of the C code. The
image has no git: run the commands of the block on the host, in the
directory of the archive, which the image sees as its working directory:

```console
$ git clone https://github.com/tarantool/tarantool.git tarantool-8b9a0f59a95b
$ git -C tarantool-8b9a0f59a95b checkout 8b9a0f59a95b
$ git -C tarantool-8b9a0f59a95b submodule update --init --recursive
$ docker run --rm -it -v "$PWD:/work" --user "$(id -u):$(id -g)" tt-coredump -s tarantool-8b9a0f59a95b tarantool-core-3030-202611240401-db1.tar.gz
...
(gdb) frame function crash_signal_cb
#3  0x000000000063da59 in crash_signal_cb (signo=11, siginfo=<optimized out>,
    context=<optimized out>) at /tarantool/src/lib/core/crash.c:204
204		abort();
```

A checkout outside the directory of the archive is mounted into the image
as well: `-v ~/src/tarantool:/src:ro` and `-s /src`.

Without `--experimental`, `tt coredump inspect` loads the extensions of the
release of the core instead: those of the sources of `-s`, else those it
finds on GitHub by the version of the core, else the copies tt embeds.
They have no `lj-bt`. The image has no CA certificates, so the lookup on
GitHub fails there, and tt warns and takes its copies: add
`ca-certificates` to the `apt-get install` of the `Dockerfile` for it.

### Warnings before gdb starts

Before gdb starts, tt checks the gdb, the archive and the core, and prints
what will not work, each warning followed by a `hint:` line of what to do:

| The warning begins with | What it means | What to do |
| --- | --- | --- |
| `gdb ... has no Python: no extension will load in it` | No `lj-*` and `tt-*` command, plain gdb only. | Use a gdb built with Python: the image. |
| `gdb ... is older than the gdb the extensions of tarantool-gdb-tools are checked on, 12.1, 15.1, 17.1 and 18.1` | The extensions may fail in that gdb. | Use gdb 12.1 or later, best 17.1: the image. |
| `the tarantool of the core has no debugging information` | No DWARF in the archive: plain backtraces only, the `lj-*` commands and the pretty-printers fail. | As the hint says: of a Community 3.x release, unpack the archive, copy the tarantool of the static deb, `tt binary install tarantool <version>`, over `<dir>/tarantool`, and inspect `<dir>`; else have the customer install the debug package of their tarantool and pack again. |
| `... threads of the core besides the main one have nothing but zeros` | A core of gcore, with empty stacks of those threads. | Ask for the core the kernel writes, `tt instance kill --dump`, or one of `gcore-pages`. |
| `gdb 15.1 will not read tarantool_L of this core` | `lj-bt` shows `L=0x0 (none)` and the Lua frames of the VM entries alone, and the `lj-*` commands find no Lua state by themselves. | Use gdb 17.1 or later: the image. The hint says why the libthread_db does not do. |
| `luajit_backtrace_gdb.py is checked on Tarantool 3.3 to 3.9, the core is of ...` | A core of a version, or an architecture, the extension was not checked on. | It is loaded anyway: it may work, or fail. |

A gdb before 17.1, as that of Ubuntu 24.04, of glibc 2.39, on a core of
Tarantool 3.9 from a host of glibc 2.43:

```text
   ⚠ gdb 15.1 will not read tarantool_L of this core of Tarantool 3.9.0, the Lua state of the main thread, thread-local since 3.7: lj-bt will show L=0x0 (none) and the Lua frames of the VM entries alone, and the lj-* commands given no lua_State will not find it module=coredump
   ⚠ hint: gdb before 17.1 needs libthread_db for it, and the libthread_db of the archive does not load here, it needs GLIBC_ABI_DT_X86_64_PLT, which /lib/x86_64-linux-gnu/libc.so.6 does not define, and that of this host is of glibc 2.39, the core of 2.43; use gdb 17.1 or later, or inspect the core on a host of glibc 2.43 or later module=coredump
```

An archive without debugging information, where the hint names what to
copy where:

```text
   ⚠ the tarantool of the core has no debugging information: no .debug_info in the tarantool, and no usr/lib/debug/.build-id/78/1dc56a9fb4e0f2e457995160fea91d4cd3fa72.debug of its build ID in the archive: the lj-* commands of LuaJIT and the pretty-printers of Tarantool will not work, plain backtraces will module=coredump
   ⚠ hint: the tarantool of the static deb of Tarantool 3.9.0, which `tt binary install tarantool 3.9.0` installs, has the debugging information: if its build ID is that of the core, 781dc56a9fb4e0f2e457995160fea91d4cd3fa72, unpack the archive with `tt coredump unpack tarantool-core-3860-202611240404-db1.tar.gz`, copy it over tarantool-core-3860-202611240404-db1/tarantool, or to the debug file named above under tarantool-core-3860-202611240404-db1, and run `tt coredump inspect tarantool-core-3860-202611240404-db1` module=coredump
...
(gdb) lj-bt
lj-bt: the LuaJIT extension is not initialized yet (no LuaJIT symbols loaded?)
```

A host without gdb fails at once, before anything is unpacked, with `gdb
is not found in PATH: install gdb, or put the directory of the gdb to use
first in PATH`.

### In gdb

`lj-bt` is where it crashed: one backtrace of the C and the Lua frames in
the order of the calls, with the fiber they ran in. The Lua frames that run
inside the VM frame `#9` are `#9.1`, `#9.2` and so on, the innermost first:

```text
(gdb) lj-bt
lj-bt: L=0x773ac7a542d0 (G->cur_L), G=0x773ac833a3d8, vmstate=CFUNC, fiber 139 "console/unix/:" (running)
#0    0x773ac869f69c in ?? ()
#1    0x773ac864ac42 in raise ()
#2    0x773ac86334ac in abort ()
#3    crash_signal_cb () at core/crash.c:204
#4    <signal handler called>
#5    lj_cconv_ct_ct () at src/lj_cconv.c:200
#6    lj_cconv_ct_tv () at src/lj_cconv.c:640
#7    lj_cdata_set () at src/lj_cdata.c:304
#8    lj_cf_ffi_meta___newindex () at src/lib_ffi.c:178  <- builtin ffi.meta.__newindex  <metamethod, continuation lj_cont_nop>
#9    lj_BC_FUNCC ()  [VM entry L=0x773ac7a542d0 cframe=0x773ac4e80ed0, fiber 139 "console/unix/:"]
#9.1  [lua] /home/user/kv/kv/init.lua:20 in function </home/user/kv/kv/init.lua:17>
#9.2  [lua] /home/user/kv/kv/init.lua:28 in function </home/user/kv/kv/init.lua:25>  <protected by pcall/xpcall>
#9.3  [lua] (tail call from local 'fun' = [string "return put('key-3', 'a value too long')..."]:0)  [heuristic]
#9.4  [lua] builtin pcall
#9.5  [lua] eval:43 in main chunk
...
#9.13 [lua] builtin/socket.lua:1130 in function <builtin/socket.lua:1128>  <called from C: lua_pcall/cpcall>
#10   lua_pcall () at src/lj_api.c:1142
...
#15   coro_init () at coro/coro.c:108
```

Here a write through a null pointer of the FFI, at line 20 of
`kv/init.lua` of the application, called from line 28, in a request of the
console. Frames `#0` to `#4` are the crash handler of Tarantool and the
signal. A core of a hung instance shows where it spins, in a trace of the
JIT:

```text
(gdb) lj-bt
lj-bt: L=0x7b973bb613f8 (G->cur_L), G=0x7b973c7cf3d8, vmstate=TRACE 223, fiber 136 "console/unix/:" (running)
-- L=0x7b973bb613f8: running a trace: Lua frames from g->jit_base 0x7b973bb5de70
#0    0x2a755458 in [trace 223] started at /home/user/kv/kv/init.lua:35  snapshot #1
#1    lj_BC_JLOOP ()  [VM entry L=0x7b973bb613f8 cframe=0x7b971a480ed0, fiber 136 "console/unix/:"]
#1.1  [lua] /home/user/kv/kv/init.lua:36 in function </home/user/kv/kv/init.lua:33>  (trace 223 base frame, snapshot #1)  <protected by pcall/xpcall>
#1.2  [lua] (tail call from local 'fun' = [string "return spin()..."]:0)  [heuristic]
...
```

`info tt-fibers` lists the fibers of the thread, the running one marked
`*`, and `tt-fiber <id>` switches `bt` and `lj-bt` to the stack of another:

```text
(gdb) info tt-fibers
  Id     Target   Name                               Stack              Function
* 139    TtFiber  "console/unix/:"                 * 0x773ac4e80d98     0x619ce0 <lua_fiber_run_f>
  133    TtFiber  "ssl_certificate.config"           0x773a9da80e18     0x619ce0 <lua_fiber_run_f>
  132    TtFiber  "expire"                           0x773a9d980e18     0x619ce0 <lua_fiber_run_f>
...
  1      TtFiber  "sched"                            0x7fff986064e8     0x0
(gdb) tt-fiber 132
Switching stack unwinder to fiber 132 (0x773ac7c02700 (expire))
#1  0x000000000064571f in fiber_yield () at /tarantool/src/lib/core/fiber.c:895
(gdb) bt
#1  0x000000000064571f in fiber_yield () at /tarantool/src/lib/core/fiber.c:895
#2  fiber_yield_timeout (delay=<optimized out>)
    at /tarantool/src/lib/core/fiber.c:939
#3  0x0000000000645ac5 in fiber_sleep (delay=<optimized out>)
    at /tarantool/src/lib/core/fiber.c:967
#4  0x000000000061a765 in lbox_fiber_sleep (L=0x773ac804e1f8)
    at /tarantool/src/lua/fiber.c:719
    <- C function lbox_fiber_sleep ()
#5  0x00000000006860e1 in lj_BC_FUNCC () at buildvm_x86.dasc:852
    [VM entry L=0x773ac804e1f8 cframe=0x773a9d980ed0, fiber 132 "expire"] ()
    [lua] /home/user/kv/kv/init.lua:13 in function </home/user/kv/kv/init.lua:10>  <called from C: lua_pcall/cpcall> ()
...
```

With the extensions of `--experimental` a plain `bt` shows the Lua frames
too, as above. `lj-bt --all-fibers` is `lj-bt` of every fiber.

`lj-stack <L>` shows the Lua stack of the Lua state `lj-bt` names, the
values of every frame, the arguments and the locals:

```text
(gdb) lj-stack 0x773ac7a542d0
...
0x773ac7543190                [ B  ] VALUE: cdata @ 0x773ac801ddf0 [1562] <struct kv_limit *> 0x0
0x773ac7543180:0x773ac7543188 [    ] FRAME: [M] delta=11, fast function ffi.meta.__newindex (#164)
...
0x773ac7543148                [    ] VALUE: string "a value too long" @ 0x773ac80362d0
0x773ac7543140                [    ] VALUE: cdata @ 0x773ac7581e28 [1186] <struct tuple &> 0x773ac7c382c0
0x773ac7543138                [    ] VALUE: cdata @ 0x773ac77453c0 [1186] <struct tuple &> 0x773ab0007904
0x773ac7543128:0x773ac7543130 [    ] FRAME: [L] delta=6, Lua function @ 0x773ac7998330, 1 upvalues, "@/home/user/kv/kv/init.lua":17
...
```

The frame of the metamethod holds the null pointer written through, a
`struct kv_limit *` of `0x0`; the frame of line 17 the two tuples and the
value it was given.

A tuple is printed from its address, cast to `struct tuple *`: a
`box_tuple_t *` of a C frame needs the cast as well, since the printer does
not take that type
([tarantool-gdb-tools#14](https://github.com/buyemune-sys/tarantool-gdb-tools/issues/14)).
`data` is the fields; of a compact tuple `bsize_bulky` and `field_map` are
wrong. `tt-mp` decodes MsgPack at an address, as the fields of a tuple
`data_offset` bytes after it:

```text
(gdb) print *(struct tuple *)0x773ac7c382c0
$1 = {local_refs = 2 '\002', flags = 0x00, format_id = 0,
...
  data_offset = 6, data = {"key-3", "a value too long"}}
(gdb) print *(struct tuple *)0x773ab0007904
$2 = {local_refs = 0 '\000', flags = 0x00, format_id = 0,
...
  data_offset = 0, data = 0U}
(gdb) tt-mp (char *)0x773ac7c382c0 + 6
$3 = {"key-3", "a value too long"}
```

The first is the tuple the request made, `box.tuple.new()`; the second, all
zeros, is the tuple of the key in the memtx space, `box.space.kv:get()`,
which a core of the default `strip_core` does not hold (see
[What the archive holds](#what-the-archive-holds)).

`lj-gc` is the state of the garbage collector of LuaJIT, and `lj-gctop` the
largest objects of the Lua heap, for a crash out of memory:

```text
(gdb) lj-gc
GC stats: PAUSE
	total: 6255922
	threshold: 10677800
...
(gdb) lj-gctop
45418 GC objects, 5916554 bytes; the 10 largest:
32840 bytes: table @ 0x773ac8263ba0 (asize: 1025, hmask: 0x3ff)
12352 bytes: table @ 0x773ac818a670 (asize: 0, hmask: 0x1ff)
...
```

`thread apply all bt` is the stacks of all threads of the process: the main
one, where the fibers run, and the other threads of Tarantool. `apropos
lj-` lists the other `lj-*` commands, `lj-tv`, `lj-str`, `lj-tab`,
`lj-func` and the rest, and `apropos tt-` those of Tarantool, `tt-list` and
`tt-list-walk` among them.

### Limits

- `tt coredump inspect` runs gdb, and there is no lldb: a Mac needs a
  Linux machine or a Docker of the architecture of the core.
- The hint of a tarantool without debugging information covers the
  releases of Tarantool Community only.
- On the cores of Tarantool Enterprise `lj-bt` may show a builtin as a Lua
  frame of its own instead of folding it into its C frame
  ([tarantool-gdb-tools#35](https://github.com/buyemune-sys/tarantool-gdb-tools/issues/35)).

<!-- Verified on 2026-11-24 with tt 3 and Tarantool 3.9.0 of the static
deb, every output of the page again. The customer side ran in
debian:stable-slim, Debian 13.7 of glibc 2.41, with --init, --hostname db1,
HOME=/home/user, core_pattern "core" and core_uses_pid 1, on a project kv
of tt new, tt binary install tarantool 3.9.0 and tt package build, whose
component kv/init.lua has put() at line 25, which writes through a null
struct kv_limit * of the FFI at line 20 once a value grows, spin() at line
33 and a fiber expire. The instance, started by tt instance start, crashed
by put('key-3', 'a value too long') of tt connect after three puts; then
tt instance logs, tt instance status --details, tt coredump list, tt
coredump pack kv:instance001 and the same with --no-logs; pack of the core
file; the lookup with Tarantool 3.8.2 installed into the project, 3.9.0
uninstalled from it and installed into the user scope, each pack in a
minute of its own; the section without debugging information on the core
of the instance started with TT_USE_SYSTEM_TARANTOOL=1 on a
/usr/bin/tarantool stripped by objcopy --strip-debug, of the build ID of
the static one; the hang by spin() of tt connect, tt instance kill --dump,
the list of the three crashes and the pack of the instance; the error of
pack with the core of the hang moved away. Every output of a terminal was
taken on a pty, as printed. Pack run in / with no tarantool in PATH and no
terminal took the tarantool the core names and printed the path of the
archive; SIGINT to a pack left nothing behind, and tt exited 130. Its gcore
part ran as root in the image above, with --cap-add SYS_PTRACE and the
instance of kv running in it; the gcore_pages_gdb.py of the URL above has
the sha256 that tt has of it. The support side ran the image above on a pty
of 80 columns, with gdb 17.1, as printed, of the uid 1000: the start of gdb,
which reached the prompt without the pager on 24 rows and paged thread apply
all bt; every command from lj-bt on of the archive of the crash, lj-bt of
the archive of the hang and the warnings of the archive without debugging
information; unpack and inspect of the directory, which made the links,
said where the logs are and warned of the two files of locale data alone;
the sizes and the modes of the archive unpacked; -s of a checkout of the
commands of the block of git clone, in the directory of the archive and
mounted on /src. A user of the uid 1234, in a container with the docker
CLI, on a copy of the archive of its own: the commands of the image without
--user failed on the archive, permission denied, and on the directory
unpack made; with --user they opened both, and inspect without
--experimental warned that GitHub cannot be reached for want of CA
certificates. The warnings of gdb 15.1 are of an image of ubuntu:24.04 on
the archive of the gcore-pages core of the image, of glibc 2.43. tt-fiber
then bt showed the fiber on the core of a crash in ubuntu:26.04 with the
libc6-dbg of its glibc, where abort() stops under two inline frames, and
the crash stack with the extensions tt had before; on the crash above, in
images with the libc6-dbg of its Debian build, both showed the fiber. The
command line of the pipe ran as its comment says, and the logs of an
instance with log.to: file went into logs/tarantool.log. Not run again: the
archive of tt 2.9.1, a gdbinit with pagination off, the units of systemd,
coredumpctl and apport. -->
