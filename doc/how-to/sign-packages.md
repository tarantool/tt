# Sign packages

A distribution built with `integrity = "rsa"` signs packages with an RSA
private key and runs them checked with the public key. tt itself checks
nothing: it has no integrity provider (see
[Build a distribution of tt](build-distribution.md)).

## Make the keys

The keys are PEM: the private one PKCS#1 or PKCS#8, the public one PKIX or
PKCS#1, as openssl makes them:

```console
$ openssl genrsa -out private.pem 2048
$ openssl rsa -in private.pem -pubout -out public.pem
```

## Sign an archive

`tt package pack --with-integrity-check private.pem` signs the archive it
packs. The package gets two hashes files at its root, each with an
RSASSA-PSS signature next to it (`.sig`): `hashes.json` lists what Tarantool
loads - the Lua sources, the shared libraries, the cluster configs of the
templates and the scripts the instance records run - and `env_hashes.json`
every other file of the package: the manifest, the lock, `VERSION`, the
instances descriptions, the programs in `.rocks/bin/`. Every runtime the
archive bundles in `_runtime/` gets an `env_hashes.json` of its own, listing
its executable, and the headers of tarantool, by the paths
`tt package install` lays them out at. A signed package runs only its own
files: a record of its templates that runs a script or a binary outside of
it, or a program it does not build into `.rocks/bin/`, fails the pack, as
does a Tarantool instance record with `env` and a `var/` at the top of the
package (see the layout on Tarantool Enterprise below). A package is not
signed `--without-deps`, as its install fetches dependencies nobody signed.

## Install a signed archive

`tt --integrity-check public.pem package install` installs only a signed
archive, into the user or the system scope: the version and each bundled
runtime are checked before they are put in place - their signatures, every
file they list, and that they list every file - so that a changed or an added
file is refused. A runtime installed there already, by `tt binary` or an
earlier install, must be the signed one: without hashes files of its own it
gets those of the archive and is checked with them.

## Run checked

`tt --integrity-check public.pem instance start` checks each application at
its own directory - the project, or the installed version - and at the
directories of the runtimes it runs on, whatever the directory tt runs in,
and runs only what they sign: the tarantool of the application, which must
be the signed one it bundles rather than one from `PATH`, the script of a
record, the binary of an external process, which is looked up in
`.rocks/bin/` of the application only, and the tt that runs the watchdog,
which must be the bundled one. The watchdog checks them again every
`--integrity-check-period` seconds and stops the instance for good on a
failure. Tarantool gets `hashes.json` of the application to check what it
loads.

## Limits

The pair of the host in `<root>/etc/<app>/` is not part of a package, and
an application that runs from one is refused with the checks on; it runs
from a template of its package, `--template`. A signed install into the
project scope, whose install root the project shares with its guest
packages, and the signing of a project as it is developed are not supported
yet. The signatures have a random salt, so a signed archive is not reproduced
bit for bit, unlike an unsigned one.

Under `--integrity-check` tt reads the manifests and the executables of the
[external modules](external-modules.md#with-integrity-checks) through the
checks as well, and a file no signed hashes file lists is refused: a module
outside the signed tree is skipped with a warning, and one whose executable
is not signed fails to run.

## The layout on Tarantool Enterprise

tt gives an instance the paths of the layout - its data, its pid file, its
sockets and its log - through the `TT_*_DEFAULT` variables of its
environment. Tarantool Enterprise starts with an empty environment when it
checks integrity, so under `--integrity-check` they do not reach it, and it
falls back to the defaults of Tarantool, relative to its working directory,
the installed version: `var/lib/<instance>` for the data,
`var/run/<instance>/tarantool.pid` and `var/run/<instance>/tarantool.control`,
and `var/log/<instance>/tarantool.log` for a log file.

A signed install makes those defaults the layout. `tt package install
--integrity-check` gives the version `var/lib`, `var/run` and `var/log` as
links to `<root>/var/lib/<app>`, `<root>/var/run/<app>` and
`<root>/var/log/<app>`, so a config that names no path keeps the data, the
pid file and the console socket of an instance in the layout of the root,
where tt finds them, as without the checks. Every version of the application
links the same directories, so the data outlives an upgrade, and removing a
version removes its links, not the data. The links are not part of what is
signed, and a signed package cannot have a `var/` of its own: the pack
refuses it. `tt instance start --integrity-check` makes the links a version
lacks - one installed before tt made them - when it may write into it, and
refuses to start an instance whose version has something else there.

A config may still name the paths itself, in the cluster config of the
package or in its config storage; tt reads the data directories, the console
socket and the first unix socket of `iproto.listen` from the config of an
instance, with `{{ instance_name }}`, `{{ replicaset_name }}` and
`{{ group_name }}` substituted. In the system scope, whose root is
`/opt/tarantool` on Linux, for the package `<app>`:

```yaml
snapshot:
  dir: /opt/tarantool/var/lib/<app>/{{ instance_name }}
wal:
  dir: /opt/tarantool/var/lib/<app>/{{ instance_name }}
vinyl:
  dir: /opt/tarantool/var/lib/<app>/{{ instance_name }}
quiver:
  dir: /opt/tarantool/var/lib/<app>/{{ instance_name }}
process:
  pid_file: /opt/tarantool/var/run/<app>/{{ instance_name }}/tarantool.pid
console:
  socket: /opt/tarantool/var/run/<app>/{{ instance_name }}/tarantool.control
iproto:
  listen:
  - uri: unix/:/opt/tarantool/var/run/<app>/{{ instance_name }}/tarantool.sock
```

`quiver.dir` is an option of Tarantool Enterprise, which Tarantool Community
refuses. `process.work_dir` is no way to move the defaults: Tarantool looks
for the modules of the application, `app.file` among them, in its working
directory, and finds none once it moves there.

What the checks take away with the environment stays away, as it did in tt
2:

- No iproto address by default: an instance listens on what `iproto.listen`
  of its config names, and `tt connect --binary` needs a unix socket there;
  `tt connect` over the console socket works without one.
- The log goes to the standard error of the instance, which tt writes into
  `tt.log` of the layout, where `tt instance logs` reads it; `log.to: file`
  writes `tarantool.log` next to it.
- The `env` of the record of a Tarantool instance does not reach it: the pack
  of a signed package and `tt instance start --integrity-check` refuse a
  record that sets it, and the values belong in the config. An external
  process keeps its `env`: tt runs it.
- A record that runs its script is refused under the checks: tt starts it
  through a launcher that takes the script and the paths of the instance from
  the environment. The instance is configured by the cluster config instead.

