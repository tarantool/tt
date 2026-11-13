# What's new in tt 3

tt 3 is a rework of tt 2.x. The main changes are:

- An application is a **package**: a manifest declares its dependencies, and a
  lockfile pins their exact versions.
- An application ships as a single **archive** that installs without network
  access.
- On a host, an application runs from a **pair of files** that describe which
  instances run there and how they are configured.

tt 3 supports Tarantool 3 only. If you are upgrading from tt 2.x, see
[Migrate from tt 2.x](migration_from_older_versions.md).

## No configuration file

tt 3 has no `tt.yaml`. The directory where you run a command determines what it
acts on:

- Inside a [project](concepts/projects-and-scopes.md), tt searches upward from
  the current directory for the project root. Commands therefore work from any
  project subdirectory.
- Outside a project, select the installation scope with a flag:
  + `--scope user`: applications and binaries for the current user;
  + `--scope system`: applications and binaries for the whole host.

  Each scope has its own root directory, but all scopes use the same directory
  layout.

## Applications are packages

Two files in the project root describe a package:

- `app.manifest.toml` is the manifest. It declares the package name, compatible
  Tarantool and tt versions, dependencies and dev dependencies, build
  components, and the products to package.
- `app.manifest.lock` is the lockfile. It pins the exact versions of all direct
  and transitive dependencies, so builds of the same commit install the same
  versions everywhere.

For example:

```sh
tt new
tt package add checks '>=3.0.0'
tt package build
tt test
```

- `tt new` creates a skeleton manifest.
- `tt package add`, `remove`, `update`, `resolve` and `deps` manage
  dependencies. They edit the manifest in place and preserve its comments.
- The lockfile changes only on an explicit request. With `--locked`, a stale
  lockfile is an error, which is useful in CI.
- `tt package build` installs dependencies into `.rocks/` and builds components:
  + Lua modules;
  + native modules with the `c`, `lua-c`, `cargo`, `cmake`, `make` or `shell`
    backend. A native module can be [written in Rust](how-to/rust-modules.md);
  + programs for external processes with the `go`, `cargo` or `cmake` backend.
- The package version comes from `VERSION` or the nearest git tag. Read it in
  code with `require('<name>.version').version`.
- `tt run` runs the project's Tarantool and passes arguments through unchanged.
- `tt test` builds the package and runs its tests with luatest. If the manifest
  does not declare luatest, tt installs it automatically.

See [Packages](concepts/packages.md) and
[Your first package](get-started/first-package.md).

## Self-contained archives

`tt package pack` creates a `<name>-<version>-<os>-<arch>.tt` archive in tar.zst
format. The archive is reproducible: the same commit produces a byte-for-byte
identical archive.

By default, the archive includes everything the application needs: dependencies,
instance templates, and the required versions of Tarantool, tt and TCM. This
lets `tt package install` work without network access.

```console
$ tt package pack
.../kv-api/_build/pack/kv-api-1.1.0-linux-amd64.tt
$ tt package install --scope system _build/pack/kv-api-1.1.0-linux-amd64.tt
```

When you install an archive into the user or system scope:

- The archive becomes a new application version, and `<root>/apps/<name>`
  switches to it atomically.
- Each application runs on the Tarantool version pinned in its own lockfile.
  Applications using different Tarantool versions can run side by side on one
  host.
- `--upgrade` installs a newer version. Running instances keep their current
  version until `tt instance restart`.
- Data and host configuration are stored separately from application versions,
  so they survive upgrades and uninstalls.

When installed into a project, the archive becomes a guest package whose rocks
join the project.

A tt distribution built with an integrity provider
[signs archives](how-to/sign-packages.md) and runs only signed ones.

## Rock servers you choose

tt takes its list of rock servers from the first configured source in this
order:

1. The `--registry` flag.
2. The `TT_REGISTRIES` environment variable.
3. The manifest's `[platform].registries` setting.
4. The built-in list.

`tt registry list` shows which list is in use.

- A rock server can be an HTTP(S) URL or a local directory.
- `tt package search` searches all configured servers.
- `tt package download` downloads rocks into a directory that can itself serve
  as a rock server. Use it to prepare a mirror for offline builds.
- `tt rocks` uses go-luarocks, a LuaRocks engine written in Go.

See [Rock servers](concepts/rock-servers.md).

## Instances: one description, three kinds

An application runs from a pair of files:

- `instances.yaml` describes which instances run on this host;
- `config.yaml` contains the Tarantool 3 cluster configuration.

These files live in `.templates/<name>/` in the project or installed application
version. Host-specific copies go in `<root>/etc/<app>/`.

For example, `instances.yaml` can contain:

```yaml
"*":                       # every instance in the config.yaml topology
  restart: on-failure
legacy:
  script: init.lua         # a box.cfg application, as in tt 2.x
gateway:
  binary: ./bin/gateway    # an external process, not Tarantool
  args: [--port, "8080"]
```

- tt starts, stops, reports status and collects logs the same way for three
  kinds of instances:
  + Tarantool instances from the cluster configuration;
  + [box.cfg applications](how-to/script-instances.md);
  + [external processes](how-to/external-processes.md).
- Each instance runs under a tt watchdog. After a crash, tt restarts it
  according to the `restart` policy.
- `tt instance start --foreground` runs all instances in the foreground and
  writes their output to stdout. This mode is intended for containers.
- Instance commands are grouped under `tt instance`. Targets can be
  `APP:INSTANCE`, `APP:*` or a list.
- `tt instance status --details` shows which Tarantool version an instance uses
  and where it comes from.
- `stop`, `restart`, `kill` and `clean` prompt for confirmation only in a
  terminal. Pass `--yes` in scripts.
- `restart` checks that all targets can start before stopping any instances.

See [Instances](concepts/instances.md) and
[A cluster on your laptop](get-started/first-cluster.md).

## systemd units from tt

For an application installed into the system scope, tt manages systemd units:

- `tt service systemd install <app>` creates an application template unit and a
  drop-in for each instance.
- `tt service systemd generate` prints the units without installing them.
- `tt service systemd uninstall` removes the units.

The `tt instance` commands detect instances managed by systemd and direct you to
`systemctl`.

See [Run instances under systemd](how-to/systemd.md).

## Binaries per project, per user, per host

`tt binary install`, `switch`, `list` and `uninstall` manage versions of
Tarantool Community Edition, Tarantool Enterprise Edition, tt, TCM and the
Enterprise SDK. Binaries can be installed into the project, user or system
scope.

- Tarantool Community Edition installs as a prebuilt binary on Linux with glibc.
  tt installs from its GitHub release, with release checksum verification. Other
  platforms build from source.
- `--from-build` links a manually built Tarantool without copying its files.
- The first install into a project creates `.env/activate.sh`. This script adds
  the project's binaries to `PATH`.

See [Manage binaries](how-to/binaries.md).

## Your own tt

tt consists of a core and a set of Go modules. `tt binary build tt` builds a
custom [distribution](concepts/distributions.md) from `tt.manifest.toml`:

- The distribution includes the core and your modules, compiled in at build
  time.
- It can have its own name and edition.
- A lockfile pins its contents, and a vendored source bundle allows offline
  builds.

External modules are loaded from the directories in `TT_CLI_MODULES_PATH`. A
module with the same name as a tt command replaces that command.

See [Build a distribution of tt](how-to/build-distribution.md) and
[External modules](how-to/external-modules.md).

## Journals without Tarantool

`tt data cat` and `tt data play` replace `tt cat` and `tt play`. They read
`.xlog`, `.snap` and `.vylog` files, as well as vinyl `.run` and `.index` files.
The readers are written in Go, so no Tarantool installation is needed on the
host.

- A data directory is read in Tarantool recovery order: the newest snapshot
  first, then `.xlog` files in vclock order. Transactions already included in
  the snapshot are skipped.
- Changes can be filtered:
  + by vclock with `--from-vclock` and `--to-vclock`;
  + by the LSNs of one replica with `--from` and `--to`.

  Both bounds are inclusive. A transaction is never split.
- `tt data cat -o json` prints one JSON object per row, one object per line, for
  use in scripts.
- `tt data play` applies whole transactions where the target supports it. The
  target can be `APP:INSTANCE` or a URI. The command supports `--dry-run` and
  `--on-error`.

See [`tt data`](reference/commands/data.md).

## Made for scripts

- Commands use consistent exit codes; see `tt help errors` for details:
  + `1`: an error the user can fix;
  + `2`: a failure of the system where the command ran;
  + `3`: partial success.
- `--log-format json` writes one JSON object per log record. Credentials in URIs
  are masked in log records.
- Logs and the progress indicator go to stderr, leaving stdout for command
  results.
- `tt instance status`, `tt registry list` and the `tt package` commands that
  print tables support `-o json` and `-o yaml`.

## Install in one line

To install the latest tt 3 release on Linux or macOS with Apple silicon, run:

```sh
curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
```

See [Install](get-started/install.md).
