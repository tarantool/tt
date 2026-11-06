# What's new in tt 3

tt 3 is a rework of tt 2.x around three ideas: an application is a
**package** with a manifest and a lock, it is shipped as one **archive** that
installs offline, and it runs on a host as a **pair** of files that says what
runs there. tt 3 supports Tarantool 3 only. Coming from tt 2.x, read
[Migrate from tt 2.x](migration_from_older_versions.md) for what to change.

## No configuration file

tt has no `tt.yaml`. What a command acts on follows from where it runs: tt
looks for a [project](concepts/projects-and-scopes.md) from the current
directory upward, so every command works from any subdirectory of it. Outside
a project, `--scope user` and `--scope system` select the applications and
binaries of the current user or of the whole host, each scope with its own
root directory and one layout for all of them.

## Applications are packages

`app.manifest.toml` declares the package: its name, the Tarantool and tt it
runs on, its dependencies and dev dependencies, the components it is built
from and the products it is packed as. `app.manifest.lock` next to it pins
every dependency, direct and transitive, so a build of the same commit
installs the same versions anywhere.

```console
$ tt new
$ tt package add checks '>=3.0.0'
$ tt package build
$ tt test
```

- `tt new` writes a skeleton manifest; `tt package add`, `remove`, `update`,
  `resolve` and `deps` manage the dependencies, editing the manifest in
  place with its comments kept. The lock moves only when asked to, and
  `--locked` makes a stale lock an error, as CI wants.
- `tt package build` installs the dependencies into `.rocks/` and builds the
  components: Lua modules, native modules with the `c`, `lua-c`, `cargo`,
  `make` or `shell` backend, and programs for external processes with the
  `go` or `cargo` backend. A native module can be
  [written in Rust](how-to/rust-modules.md).
- The version of a package comes from `VERSION` or from the nearest git tag,
  and the code reads it as `require('<name>.version').version`.
- `tt run` runs Tarantool of the project with the arguments as they are;
  `tt test` builds the package and runs luatest on its tests, installing
  luatest when the manifest does not declare it.

See [Packages](concepts/packages.md) and
[Your first package](get-started/first-package.md).

## Self-contained archives

`tt package pack` makes `<name>-<version>-<os>-<arch>.tt`, a reproducible
tar.zst archive: the same commit packs into the same bytes. By default the
archive carries everything the application needs - the dependencies, the
instance templates, and the Tarantool, tt and TCM it runs on - so
`tt package install` needs no network.

```console
$ tt package pack
$ tt package install --scope system kv-api-1.1.0-linux-amd64.tt
```

Installed into the user or the system scope, an archive becomes a version of
the application, and `<root>/apps/<name>` switches to it atomically. Every
application runs on the Tarantool its own lock names, so applications on
different Tarantool versions live side by side. `--upgrade` installs a newer
version, a running instance keeps its version until `tt instance restart`,
and the data and the configuration of the host stay apart from the versions,
so they survive an upgrade and an uninstall. Installed into a project, an
archive is a guest package whose rocks join the project.

A distribution built with an integrity provider
[signs archives](how-to/sign-packages.md) and runs only signed ones.

## Rock servers you choose

The rock servers come from `--registry`, `TT_REGISTRIES`,
`[platform].registries` of the manifest or the built-in list, the first that
is set; `tt registry list` shows which. A server is an HTTP(S) URL or a local
directory. `tt package search` searches them all, and `tt package download`
makes a directory of rocks that is a rock server itself: a mirror for offline
builds. `tt rocks` runs on go-luarocks, a LuaRocks engine in Go.
See [Rock servers](concepts/rock-servers.md).

## Instances: one description, three kinds

An application runs from a pair of files: `instances.yaml`, what runs on
this host, and `config.yaml`, the cluster config of Tarantool 3. The pair
lives in `.templates/<name>/` of the project or of the installed version, or
in `<root>/etc/<app>/` for one host.

```yaml
"*":                       # every instance of the topology of config.yaml
  restart: on-failure
legacy:
  script: init.lua         # a box.cfg application, as in tt 2.x
gateway:
  binary: ./bin/gateway    # a process that is not Tarantool
  args: [--port, "8080"]
```

- Next to the Tarantool instances of the cluster config,
  [box.cfg applications](how-to/script-instances.md) and
  [external processes](how-to/external-processes.md) start, stop, show their
  status and write their logs the same way.
- Every instance runs under a watchdog of tt that restarts it after a crash
  as `restart` asks. `tt instance start --foreground` keeps all the
  instances in the foreground with their output on stdout, for containers.
- The commands are under `tt instance` and take targets as `APP:INSTANCE`,
  `APP:*` or a list. `tt instance status --details` shows which Tarantool an
  instance runs on and where it comes from.
- `stop`, `restart`, `kill` and `clean` ask for a confirmation on a terminal
  only; a script passes `--yes`. `restart` checks that every target can
  start before it stops anything.

See [Instances](concepts/instances.md) and
[A cluster on your laptop](get-started/first-cluster.md).

## systemd units from tt

`tt service systemd install <app>` writes a template unit for an
application installed into the system scope and a drop-in for each of its
instances; `generate` prints them and `uninstall` removes them. The
instance commands know which instances run under a unit and point at
`systemctl` for them. See [Run instances under systemd](how-to/systemd.md).

## Binaries per project, per user, per host

`tt binary install`, `switch`, `list` and `uninstall` manage Tarantool
Community and Enterprise, tt, TCM and the Enterprise SDK in the project,
user or system scope.

- Tarantool Community installs prebuilt on Linux with glibc, and tt from
  its GitHub release, verified against the checksums of the release; other
  platforms build from source.
- `--from-build` links a Tarantool built by hand without copying it.
- The first install into a project writes `.env/activate.sh`, which puts the
  binaries of the project on `PATH`.

See [Manage binaries](how-to/binaries.md).

## Your own tt

tt is a core and a set of Go modules. `tt binary build tt` builds a
[distribution](concepts/distributions.md) from `tt.manifest.toml`: the core
with your modules compiled in, under its own name and edition, pinned by a
lock and buildable offline from a vendored bundle. External modules are
found in `TT_CLI_MODULES_PATH`, and one named like a command of tt replaces
it. See [Build a distribution of tt](how-to/build-distribution.md) and
[External modules](how-to/external-modules.md).

## Journals without Tarantool

`tt data cat` and `tt data play`, which replace `tt cat` and `tt play`, read
`.xlog`, `.snap` and `.vylog` files, and the `.run` and `.index` files of
vinyl, in Go: neither needs a Tarantool on the host.

- A directory is read as Tarantool recovers it: the newest snapshot, then
  the xlogs in vclock order, without the transactions the snapshot holds.
- `--from-vclock` and `--to-vclock` select changes by vclock, `--from` and
  `--to` by the LSNs of one replica, both ends included; a transaction is
  never cut in two.
- `tt data cat -o json` prints one JSON object per row, for scripts.
- `tt data play` applies transactions whole where the target allows it,
  takes `APP:INSTANCE` as well as a URI, and has `--dry-run` and
  `--on-error`.

See [`tt data`](reference/commands/data.md).

## Made for scripts

- Exit codes mean the same for every command: 1 for what the user can fix,
  2 for a failure of the system the command ran on, 3 for a partial
  success. `tt help errors` describes them.
- `--log-format json` writes the log as one JSON object per record; the
  credentials in URIs are masked in every log record.
- The log and the spinner go to stderr, so stdout holds only the result.
  `tt instance status`, `tt registry list` and the `tt package` commands
  that print tables take `-o json` and `-o yaml`.

## Install in one line

```console
$ curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
```

installs the newest tt 3 release on Linux and on macOS with Apple silicon.
See [Install](get-started/install.md).
