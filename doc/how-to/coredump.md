# Inspect a crash of Tarantool

When Tarantool crashes, or hangs and is made to quit with a dump, the kernel
writes a core dump: the memory of the process. `tt coredump pack`, run on
the host where Tarantool ran, turns the core into one archive that holds
everything gdb needs to read it: the core, the tarantool that dumped it, its
shared libraries and the gdb extensions. `tt coredump inspect`, run on a
support host, opens the archive in gdb with the extensions of LuaJIT and
Tarantool loaded, which show the C and the Lua frames of the crash, the
fibers and the Lua heap. The support host needs no Tarantool of that version
and no access to the host of the customer.

This page is in two parts: what the customer runs to get the archive, and
what support runs to read it, with a Docker image that has all of it. The
flags of the commands are in [`tt coredump`](../reference/commands/coredump.md).

## On the customer's host

You need:

- tt 3 for Linux, amd64 or arm64;
- the tarantool that dumped the core, the very file: pack compares its
  build ID with that of the core and runs `tarantool --version`;
- bash, GNU tar and gzip, `getopt` of util-linux and the coreutils
  `basename`, `cat`, `date`, `dirname`, `ln`, `mkdir`, `mktemp` and `rm`.
  `hostname` is optional: without it the archive is named `...-hostname`.
  BusyBox does not do: its tar has no `--format=posix` and `--sparse`, and
  pack fails;
- read access to the core and to the shared libraries the process had
  loaded;
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
  file. It keeps cores up to the sizes `ProcessSizeMax=` and
  `ExternalSizeMax=` of `/etc/systemd/coredump.conf` say, 32G by default in
  systemd 257 of Debian 13: an instance of more memory needs them raised.
- `|/usr/share/apport/apport ...`, on Ubuntu: apport keeps the core inside
  its report in `/var/crash/`, and `apport-unpack <report>.crash <dir>`
  writes it out as `<dir>/CoreDump`.
- A pipe to tt, as below.

<!-- TODO(writers): the coredumpctl and apport-unpack commands were not run:
the host of the walkthrough had core_pattern "core". They are those of
test/integration/coredump/coredump_helpers.py. -->

With a core file, go to [Pack a core](#pack-a-core).

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
`/var/core/tarantool-core-<PID>-<time>-<host>.tar.gz`. The core of any other
program of the host that crashes is stored and not packed, so `/var/core`
needs room for those too. The stored core stays next to the archive, which
holds it: remove it once the archive is sent. `core_pattern` is one for the
whole host; this replaces systemd-coredump or apport there.

<!-- TODO(writers): the pipe was not set up on the host of the walkthrough,
which other users share. The command line of the pipe was run as the kernel
runs it, from / with an empty environment and a core on the standard input
while /proc/PID was a tarantool of the same build, as
test_coredump_pack_pipe does: it stored the core and made the archive as
described. -->

### Pack a core

A crash leaves the backtrace of Tarantool and the signal it ended with in
the log of the instance, and the core in the directory of the project:

```console
$ tt instance logs kv:instance001 -n 3
kv:instance001: #12 0xeaa17e in coro_init+62
kv:instance001: Watchdog 2026/11/23 22:27:26 (WARN): tarantool, PID 33, has ended: it was killed by signal 6 (aborted).
kv:instance001: Watchdog 2026/11/23 22:27:26 (INFO): the Instance has shutdown.
$ ls core.*
core.33
```

Tarantool reports a crash, a SIGSEGV here, and then aborts, so the core is
of signal 6. Pack it in the directory of the project:

```console
$ tt coredump pack -p 33 core.33
The resulting archive is located here: /home/user/kv/tarantool-core-33-202611232227-db1.tar.gz
The archive contents are listed below:
tarantool-core-33-202611232227-db1/version
tarantool-core-33-202611232227-db1/etc/os-release
tarantool-core-33-202611232227-db1/tarantool
tarantool-core-33-202611232227-db1/coredump
tarantool-core-33-202611232227-db1/lib/x86_64-linux-gnu/libdl.so.2
tarantool-core-33-202611232227-db1/lib/x86_64-linux-gnu/librt.so.1
tarantool-core-33-202611232227-db1/lib/x86_64-linux-gnu/libpthread.so.0
tarantool-core-33-202611232227-db1/lib/x86_64-linux-gnu/libm.so.6
tarantool-core-33-202611232227-db1/lib/x86_64-linux-gnu/libgcc_s.so.1
tarantool-core-33-202611232227-db1/lib/x86_64-linux-gnu/libc.so.6
tarantool-core-33-202611232227-db1/lib64/ld-linux-x86-64.so.2
tarantool-core-33-202611232227-db1/lib/x86_64-linux-gnu/libthread_db.so.1
tarantool-core-33-202611232227-db1/gdb.sh
tarantool-core-33-202611232227-db1/ext/tarantool-gdb.py
tarantool-core-33-202611232227-db1/ext/luajit_dbg.py

If you want to upload it, choose the available resource
and run the following command:
$ curl -T /home/user/kv/tarantool-core-33-202611232227-db1.tar.gz <resource-uri>
   • Core was successfully packed. module=coredump
```

The archive is `tarantool-core-<PID>-<time>-<host>.tar.gz`; `-p` gives the
PID, `N` without it, and the time is that of the pack, unless `-t` gives
the time of the dump in seconds since the Epoch. The list of the contents
and the hint of `curl` are printed on a terminal only. `-d` makes the
archive in another directory.

Pack takes the tarantool `-e` names. Without `-e` it takes the one the
instance commands run in the project of the working directory, else the
`tarantool` of `PATH`; the pipe takes the executable of the process. A
tarantool of another build is refused, as when the Tarantool of the project
was upgraded after the crash:

```console
$ tt coredump pack -p 33 core.33
   ⨯ core dump file is produced by the different Tarantool executable: looks like 'core.33' is not generated by `/home/user/kv/.tarantool/versions/tarantool/tarantool_3.8.2/tarantool': the executable of the core has build ID 781dc56a9fb4e0f2e457995160fea91d4cd3fa72, the binary 77b6c017b00ea47bd50386524b503e4457420aa8; check that the given COREDUMP is produced by the specified BINARY
$ tt coredump pack -e .tarantool/versions/tarantool/tarantool_3.9.0/tarantool -p 33 core.33
   • Core was successfully packed. module=coredump
```

The first line gdb prints of the core, `Core was generated by ...`, names
the path the process ran.

### A tarantool without debugging information

The archive holds the tarantool as the customer has it. One stripped of its
debugging information, DWARF, gives plain backtraces only: the `lj-*`
commands and the pretty-printers of Tarantool look its types up. Pack warns
of it, and finds the debug file a debuginfo or dbgsym package installs under
`/usr/lib/debug/.build-id/` and packs it, when there is one. For a release
of Tarantool Community 3.x, whose static builds are the same in the RPM and
the deb packages, the hint names the tarantool of the static deb, which
keeps its DWARF and which `tt binary install` installs:

```console
$ tt coredump pack -e /usr/bin/tarantool -p 33 core.33
   ⚠ the tarantool /usr/bin/tarantool has no debugging information, and no debug file of its build ID 781dc56a9fb4e0f2e457995160fea91d4cd3fa72 is under /usr/lib/debug: the archive will have none, and the lj-* commands of LuaJIT and the pretty-printers of Tarantool will not work on the core, plain backtraces will module=coredump
   ⚠ hint: the tarantool of the static deb of Tarantool 3.9.0, which `tt binary install tarantool 3.9.0` installs, has the debugging information: if its build ID is that of the core, 781dc56a9fb4e0f2e457995160fea91d4cd3fa72, pack the core with it, `tt coredump pack -e <its path>` module=coredump
   • Core was successfully packed. module=coredump
$ mkdir ~/deb
$ cd ~/deb
$ tt binary install tarantool 3.9.0
   • Not inside a project: the project scope is /home/user/deb/.tarantool, in the current directory (see tt binary --help)
...
   • Made default by symlink "/home/user/deb/.tarantool/bin/tarantool"
   • Done.
...
$ cd ~/kv
$ tt coredump pack -e ~/deb/.tarantool/bin/tarantool -p 33 core.33
   • Core was successfully packed. module=coredump
```

The build ID check of pack tells whether it is the same build. `tt binary
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
`--yes` does not.

```console
$ tt instance kill --dump kv:instance001 --yes
   • The instance kv:instance001 (PID = 33) has been terminated with dump.
$ ls core.*
core.33
$ tt coredump pack -p 33 core.33
   • Core was successfully packed. module=coredump
```

An instance a unit of systemd runs is refused, and tt names the
`systemctl kill --signal=SIGQUIT` command of its unit instead.

`gcore` of gdb, run as root, writes a core of a running process and leaves
it running, but gcore of gdb before 18.1, on glibc 2.42 or later with Linux
6.13 or later, writes the stacks of all threads but the main one as zeros.
Pack warns of such a core:

```console
# gcore -o core 80747
...
warning: Memory read failed for corefile section, 1048576 bytes at 0x787ab8fff000.
...
Saved corefile core.80747
[Inferior 1 (process 80747) detached]
# tt coredump pack -p 80747 core.80747
   ⚠ all 4 threads of the core besides the main one have nothing but zeros in the 1024 bytes above the stack pointer: the backtraces of those threads will be empty, and libthread_db may fail on the core module=coredump
   ⚠ hint: gcore of gdb older than 18.1 writes such stacks on glibc 2.42 or later with Linux 6.13 or later; take the core the kernel writes, or write it with gcore-pages of tarantool-gdb-tools module=coredump
   • Core was successfully packed. module=coredump
```

Prefer `tt instance kill --dump`. Where the process must keep running,
`gcore-pages` of [tarantool-gdb-tools](https://github.com/buyemune-sys/tarantool-gdb-tools)
is a gcore that keeps the stacks. Take it at the commit `tt coredump inspect
--help` names:

```console
# curl -fsSLO https://raw.githubusercontent.com/buyemune-sys/tarantool-gdb-tools/dba4b9be8be9c9a35c0d318fd2f9a244f40fff92/gcore_pages_gdb.py
# gdb -p 80747 -batch -ex 'source gcore_pages_gdb.py' -ex 'gcore-pages core.80747'
...
Saved corefile core.80747
gcore-pages: read again 33570816 bytes of 4 mappings, 5 pages unreadable
[Inferior 1 (process 80747) detached]
# tt coredump pack -p 80747 core.80747
   • Core was successfully packed. module=coredump
```

### What the archive holds

- `coredump`, the core: the memory of the process;
- `tarantool`, the executable that dumped it, and `version`, what its
  `--version` printed;
- the shared libraries the process had loaded, under their paths, with
  `ld-linux` and `libthread_db`, which gdb loads from the archive in place
  of those of the support host;
- the debug file of the tarantool, under `usr/lib/debug/.build-id/`, when
  the host has one;
- `etc/os-release` of the host, `gdb.sh` and the gdb extensions of the
  release, `ext/`.

The Lua code of the application is not in it. `lj-bt` names the Lua frames
by their file and line, `init.lua:20`: get the code of the application of
the version that crashed from the customer to read them.

The archive of the crash above is 44 MB and unpacks into files of 1.3 GB,
which take 133 MB of the disk. A core is as large as the memory the
process had mapped, 1.2 GB here, but the pages the process never touched
are holes of the file, which take no room: pack keeps them in the archive,
and unpack and inspect keep them on the disk, so the core takes 19 MB
there. Most of the rest is the tarantool with its debugging information,
110 MB. `ls -l` shows the size of the core, `du` the room it takes. A copy
that fills the holes, onto a file system without them or by a tool that
writes them out, takes the whole 1.2 GB.

Handle the archive as the data of the customer: the core holds what the
process had in memory, the Lua values among it, as the strings and the
tuples of the requests in flight. The tuples of memtx spaces are left out of
it by default: `process.strip_core` of the cluster config, `true` by
default, leaves the memtx arena out of the core, and gdb reads those tuples
as zeros. With `strip_core: false` they are in the core, and so is the data
of the spaces.

### An archive of tt 2

tt 3 opens the archives of tt 2: their layout is the same, one directory
`tarantool-core-<PID>-<time>-<host>/` with `tarantool`, `coredump`,
`version`, `etc/os-release` and the libraries under their paths. tt 3 runs
its own `gdb.sh` and picks the extensions itself, by `version`, and leaves
the `gdb.sh` and `ext/` of the archive alone. On the customer's host, the
pack of tt 2 needs gdb and `file`, packs no debug file of `/usr/lib/debug`,
and cannot be the pipe of `kernel.core_pattern`, since it reads the core
from a file only. Have the customer pack with tt 3 where they can.

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

gdb runs as the user `ubuntu` of the image, not root, and asks for no
network: the extensions come from tt. The gdb of Ubuntu reads the cores of
its own architecture alone: build the image on a host of the architecture
of the core.

The image keeps the pagination of gdb. tt turns it off while gdb loads the
core and the extensions, whose lines run past a screen, and back on at the
prompt: a long output, as that of `thread apply all bt`, stops at `--Type
<RET> for more` as in any gdb. With `set pagination off` in a gdbinit,
`/etc/gdb/gdbinit` of the image or the `~/.gdbinit` of a host, there is no
pager at all: tt leaves it off.

Run it in the directory of the archive, which it sees as `/work`:

```console
$ docker run --rm -it -v "$PWD:/work" tt-coredump tarantool-core-33-202611232227-db1.tar.gz
```

The archive is unpacked into `/tmp` of the container and removed when gdb
exits. Exit gdb with `quit`.

### Open the archive

On a host with gdb and tt, `tt coredump inspect --experimental <archive>`
is the same as the image. What it prints before the prompt of gdb:

```text
   • gdb extensions of tarantool-gdb-tools dba4b9be, which tt embeds: luajit_gdb.py, tarantool_gdb.py, luajit_backtrace_gdb.py, gcore_pages_gdb.py module=coredump
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
Reading symbols from /tmp/tt-coredump-4045708535/tarantool-core-33-202611232227-db1/tarantool...
warning: Can't open file /home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool during file-backed mapping note processing
...
[New LWP 33]
[New LWP 36]
...
[Thread debugging using libthread_db enabled]
Using host libthread_db library "/tmp/tt-coredump-4045708535/tarantool-core-33-202611232227-db1/lib/x86_64-linux-gnu/libthread_db.so.1".
Core was generated by `/home/user/kv/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool -n instance001 -c /home/user/kv/.templates/default/config.yaml'.
Program terminated with signal SIGABRT, Aborted.
...
LuaJIT debug extension is successfully loaded
lj-bt command initialized
(gdb)
```

- The first line names the extensions `--experimental` loads.
- The block of `git clone` is how to get the sources of the Tarantool of
  the core, at its commit, into `tarantool-<commit>` of the working
  directory, once for the cores of that commit, and to give them with
  `-s`, as below. gdb shows the backtraces without them; with them it shows
  the lines of the C code. With `-s` the block is not printed.
- The warnings `Can't open file ... during file-backed mapping note
  processing` are about files of the customer's host the archive does not
  need: they do no harm.
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
$ docker run --rm -v "$PWD:/work" --user "$(id -u):$(id -g)" --entrypoint tt tt-coredump coredump unpack tarantool-core-33-202611232227-db1.tar.gz
   • Archive was successfully unpacked. module=coredump
$ docker run --rm -it -v "$PWD:/work" tt-coredump tarantool-core-33-202611232227-db1
```

`-s` gives gdb the sources of the Tarantool of the core, a checkout of its
commit with its submodules, so that it shows the lines of the C code. The
image has no git: run the commands of the block on the host, in the
directory of the archive, which the image sees as its working directory:

```console
$ git clone https://github.com/tarantool/tarantool.git tarantool-8b9a0f59a95b
$ git -C tarantool-8b9a0f59a95b checkout 8b9a0f59a95b
$ git -C tarantool-8b9a0f59a95b submodule update --init --recursive
$ docker run --rm -it -v "$PWD:/work" tt-coredump -s tarantool-8b9a0f59a95b tarantool-core-33-202611232227-db1.tar.gz
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
They have no `lj-bt`.

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
   ⚠ hint: the tarantool of the static deb of Tarantool 3.9.0, which `tt binary install tarantool 3.9.0` installs, has the debugging information: if its build ID is that of the core, 781dc56a9fb4e0f2e457995160fea91d4cd3fa72, unpack the archive with `tt coredump unpack tarantool-core-33-202611232228-db1.tar.gz`, copy it over tarantool-core-33-202611232228-db1/tarantool, or to the debug file named above under tarantool-core-33-202611232228-db1, and run `tt coredump inspect tarantool-core-33-202611232228-db1` module=coredump
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
lj-bt: L=0x739716b4bbe8 (G->cur_L), G=0x7397188fc3d8, vmstate=CFUNC, fiber 133 "console/unix/:" (running)
#0    0x739718c6169c in ?? ()
#1    0x739718c0cc42 in raise ()
#2    0x739718bf54ac in abort ()
#3    crash_signal_cb () at core/crash.c:204
#4    <signal handler called>
#5    lj_cconv_ct_ct () at src/lj_cconv.c:203
#6    lj_cconv_ct_tv () at src/lj_cconv.c:640
#7    lj_cdata_set () at src/lj_cdata.c:304
#8    lj_cf_ffi_meta___newindex () at src/lib_ffi.c:178  <- builtin ffi.meta.__newindex  <metamethod, continuation lj_cont_nop>
#9    lj_BC_FUNCC ()  [VM entry L=0x739716b4bbe8 cframe=0x739717480ed0, fiber 133 "console/unix/:"]
#9.1  [lua] init.lua:20 in function <init.lua:17>
#9.2  [lua] init.lua:28 in function <init.lua:25>  <protected by pcall/xpcall>
#9.3  [lua] (tail call from local 'fun' = [string "return put('key-3', 'a value too long')..."]:0)  [heuristic]
#9.4  [lua] builtin pcall
#9.5  [lua] eval:43 in main chunk
...
#9.13 [lua] builtin/socket.lua:1130 in function <builtin/socket.lua:1128>  <called from C: lua_pcall/cpcall>
#10   lua_pcall () at src/lj_api.c:1142
...
#15   coro_init () at coro/coro.c:108
```

Here a write through a null pointer of the FFI, at line 20 of `init.lua` of
the application, called from line 28, in a request of the console. Frames
`#0` to `#4` are the crash handler of Tarantool and the signal. A core of a
hung instance shows where it spins, in a trace of the JIT:

```text
(gdb) lj-bt
lj-bt: L=0x768f7fba0af0 (G->cur_L), G=0x768f81cd43d8, vmstate=TRACE 232, fiber 135 "console/unix/:" (running)
-- L=0x768f7fba0af0: running a trace: Lua frames from g->jit_base 0x768f7fba34c8
#0    0x2a74e340 in [trace 232] started at init.lua:32  snapshot #1
#1    lj_BC_JLOOP ()  [VM entry L=0x768f7fba0af0 cframe=0x768f80880ed0, fiber 135 "console/unix/:"]
#1.1  [lua] init.lua:32 in function <init.lua:31>  (trace 232 base frame, snapshot #1)  <protected by pcall/xpcall>
...
```

`info tt-fibers` lists the fibers of the thread, the running one marked
`*`, and `tt-fiber <id>` switches `bt` and `lj-bt` to the stack of another:

```text
(gdb) info tt-fibers
  Id     Target   Name                               Stack              Function
* 133    TtFiber  "console/unix/:"                 * 0x739717480d98     0x619ce0 <lua_fiber_run_f>
  130    TtFiber  "ssl_certificate.config"           0x7396ed280e18     0x619ce0 <lua_fiber_run_f>
  129    TtFiber  "expire"                           0x7396ed180e18     0x619ce0 <lua_fiber_run_f>
...
  1      TtFiber  "sched"                            0x7ffc6b928ce8     0x0
(gdb) tt-fiber 129
Switching stack unwinder to fiber 129 (0x739718402700 (expire))
#1  0x000000000064571f in fiber_yield () at /tarantool/src/lib/core/fiber.c:895
(gdb) bt
#1  0x000000000064571f in fiber_yield () at /tarantool/src/lib/core/fiber.c:895
#2  fiber_yield_timeout (delay=<optimized out>)
    at /tarantool/src/lib/core/fiber.c:939
#3  0x0000000000645ac5 in fiber_sleep (delay=<optimized out>)
    at /tarantool/src/lib/core/fiber.c:967
#4  0x000000000061a765 in lbox_fiber_sleep (L=0x739716b76b10)
    at /tarantool/src/lua/fiber.c:719
    <- C function lbox_fiber_sleep ()
#5  0x00000000006860e1 in lj_BC_FUNCC () at buildvm_x86.dasc:852
    [VM entry L=0x739716b76b10 cframe=0x7396ed180ed0, fiber 129 "expire"] ()
    [lua] init.lua:13 in function <init.lua:10>  <called from C: lua_pcall/cpcall> ()
...
```

`lj-bt` makes a plain `bt` show the Lua frames too, as above. `lj-bt
--all-fibers` is `lj-bt` of every fiber.

`lj-stack <L>` shows the Lua stack of the Lua state `lj-bt` names, the
values of every frame, the arguments and the locals:

```text
(gdb) lj-stack 0x739716b4bbe8
...
0x739717bd42e0                [  T ] VALUE: string "a value too long" @ 0x7397182f4b78
...
0x739717bd4280                [    ] VALUE: cdata @ 0x739716b51510 [1186] <struct tuple &> 0x739718438400
0x739717bd4278                [    ] VALUE: cdata @ 0x739716b514f0 [1186] <struct tuple &> 0x73970000793c
0x739717bd4268:0x739717bd4270 [    ] FRAME: [L] delta=9, Lua function @ 0x739716b76d40, 1 upvalues, "@init.lua":17
...
```

A tuple is printed from its address, cast to `struct tuple *`: a
`box_tuple_t *` of a C frame needs the cast as well, since the printer does
not take that type
([tarantool-gdb-tools#14](https://github.com/buyemune-sys/tarantool-gdb-tools/issues/14)).
`data` is the fields; of a compact tuple `bsize_bulky` and `field_map` are
wrong. `tt-mp` decodes MsgPack at an address, as the fields of a tuple
`data_offset` bytes after it:

```text
(gdb) print *(struct tuple *)0x739718438400
$1 = {local_refs = 2 '\002', flags = 0x00, format_id = 0,
...
  data_offset = 6, data = {"key-3", "a value too long"}}
(gdb) print *(struct tuple *)0x73970000793c
$2 = {local_refs = 0 '\000', flags = 0x00, format_id = 0,
...
  data_offset = 0, data = 0U}
(gdb) tt-mp (char *)0x739718438400 + 6
$3 = {"key-3", "a value too long"}
```

The first is the tuple the request made, `box.tuple.new()`; the second, all
zeros, is a tuple of a memtx space, which a core of the default
`strip_core` does not hold (see
[What the archive holds](#what-the-archive-holds)).

`lj-gc` is the state of the garbage collector of LuaJIT, and `lj-gctop` the
largest objects of the Lua heap, for a crash out of memory:

```text
(gdb) lj-gc
GC stats: SWEEPSTRING
	total: 12340279
	threshold: 12340687
...
(gdb) lj-gctop
81085 GC objects, 12000911 bytes; the 10 largest:
32840 bytes: table @ 0x739718851568 (asize: 1025, hmask: 0x3ff)
24433 bytes: proto @ 0x739717ff6488
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
- On a support host whose gdb finds the debugging information of the glibc
  of the core, as one of the same glibc build with `libc6-dbg`, `tt-fiber`
  of `--experimental` does not switch `bt` and `lj-bt` to another fiber on
  the core of a crash
  ([tarantool-gdb-tools#36](https://github.com/buyemune-sys/tarantool-gdb-tools/issues/36)).
  The image has no such information; `tt coredump inspect` without
  `--experimental` switches there.

<!-- Verified on 2026-11-24 with tt 3 and Tarantool 3.9.0 of the static
deb: the customer side in debian:stable-slim, its gcore part as root in the
image above, the support side in the image above, with gdb 17.1, and, for
gdb 15.1, in one of ubuntu:24.04. The sizes of the archive unpacked, the
start of gdb without the pager in the image on a terminal of 24 rows, the
block of git clone and the -s of its checkout were checked again on a core
of the same crash. The tt-fiber limit was seen on an Ubuntu 26.04 host with
libc6-dbg. -->
