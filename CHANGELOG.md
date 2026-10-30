# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `tt package pack --with-integrity-check KEY`, in a distribution with the
  RSA integrity provider: sign the archive. The package gets `hashes.json`,
  listing what Tarantool loads, and `env_hashes.json`, listing every other
  file, each with its signature, and each bundled runtime an
  `env_hashes.json` of its own; a template record that runs a file outside
  the package fails the pack. A package packed `--without-deps` is not
  signed.
- `tt package install` with `--integrity-check`: install only a signed
  archive, into the user or the system scope, the version and each bundled
  runtime checked before they are put in place; a changed or an added file
  is refused, and so is a runtime installed already that is not the signed
  one.
- `tt binary build tt`: build a distribution of tt - the tt core and the tt
  modules compiled into one binary - from `tt.manifest.toml`. Every field but
  `manifest_version` and `manifest_kind = "tt-distribution"` is optional: the
  core defaults to the one of the running tt and takes any Go version query
  or a local checkout; modules are local directories, module paths or
  `module@version`, and the modules of the core's repository follow its
  commit. `[distribution]` sets the title, the edition, the integrity
  provider and whether tt's own commands and external modules are in;
  `[build]` sets the TLS implementation and extra tags and linker flags.
  `tt.manifest.lock` and `tt.manifest.sum` pin the build; `--update`
  re-resolves it and `--locked` reproduces it. An application manifest and a
  distribution manifest refuse each other's command by name.
- `tt binary build tt --vendor DIR`: make an offline bundle of a distribution
  instead of the binary - the main module with every module vendored, the
  local modules copied in, and `build.env` and `build.sh` with the tags and
  linker flags of each TLS implementation - which builds with the Go
  toolchain alone, with no network, no module cache and no tt.
- `tt binary build tt --core VALUE` and `TT_DIST_CORE`: build a distribution
  on a local checkout of the core or on a version query instead of `[core]`,
  without editing the manifest. The lock is neither read for the core nor
  written, and `--locked` refuses the override.
- `tt.manifest.lock` records `content_hash` of every local module and local
  core - the hash of the files a bundle would copy - and `tt binary build tt
  --locked` fails when they changed since the lock was written.
- `[replace]` in `tt.manifest.toml`: replace a Go module of the build with a
  local directory or with another module version, as a `replace` directive of
  a `go.mod` does. Go applies those of the main module only, so a tt module
  that depends on an unpublished library had no way to be built into a
  distribution. The version may be a query; the lock pins it, records a
  directory by `content_hash`, and `tt binary build tt --locked` fails when
  the directory changed. `--vendor` copies the directories into the bundle,
  which builds offline. The core, the modules of its repository and the
  `[dependencies]` modules are refused: they have `[core].path`, `--core`,
  `path` and `version`.
- `tt version` of a distribution with a version of its own also shows the
  version of the tt core it is built on, and `tt version --modules` lists the
  modules the binary is built from with the versions Go recorded for their
  Go modules.

- `tt cluster topology -c <CLUSTER_CONFIG|URI>`: show a Tarantool 3 cluster's
  configured membership and current instance state. Read the configuration
  from a file, etcd or Tarantool Config Storage and query instances in parallel.
  Print a table by default or JSON with `--format json`, including replicaset
  and instance UUIDs, instance names, hostnames, read/write modes and
  reachability. Keep unavailable instances, unavailable replicasets and
  configured empty replicasets in the result. Support storage credentials and
  the connector TLS, encrypted key password and connection timeout flags.
  Share configuration loading and topology discovery with backup and restore
  planning.
- `mage lint:python` and `mage lint:full`: accept `--fix` to let Ruff apply
  automatic Python lint fixes.
- `tt backup`: add full and incremental point-in-time backup workflows for
  Tarantool 3.8 and newer. `start` opens `box.backup`, freezes the replicaset
  membership and recovery points, and writes a compressed archive and manifest
  fragment. `--snapshot` takes a fresh checkpoint before a full backup;
  `--from-vclock` selects an incremental backup. `finalize` closes the backup
  and removes its local artifacts, with `--force` to close it when local metadata
  is unavailable. Both commands accept `--dir` and the connector TLS, encrypted
  key password and connection timeout flags. A failed start closes the backup
  it opened; an already open backup is reported with its identifying details.
- `tt backup plan`, `upload` and `last`: plan against the configured and live
  cluster topology, validate and upload per-replicaset archives, aggregate a
  versioned cluster manifest, and inspect the latest backup. Plans carry the
  cluster and environment scope and record why a full backup is required.
  Upload checks the plan, fragments, checksums, archive contents and current
  storage before publication, reports missing or partial shards as warnings,
  and records archive paths relative to the storage root. `tt backup-id`
  generates a sortable UTC identifier for a new backup.
- `tt backup verify` and `gc`: check manifest chains and archive checksums,
  distinguish an unfinished upload from corruption, and apply full-backup or
  age-based retention with optional `--dry-run`. Garbage collection preserves
  the chains retained backups need, restricts deletion to the backup layout,
  rechecks orphaned archives, and reports actual deletions. All storage commands
  accept `--cluster-name` and `--environment`, and support filesystem or
  S3-compatible storage selected by URI or a configuration file. Backup and
  restore verdicts use command-specific exit codes from 3 upward, keeping
  them distinct from v3 request errors (1) and system failures (2).
- `tt restore plan`: resolve a named recovery point or timestamp within its
  own complete backup chain, optionally select `--replicasets`, and produce
  per-instance restore targets from the cluster configuration and frozen backup
  membership. Plans distinguish out-of-range requests, missing points and broken
  chains, and refuse replicasets absent from the backup.
- `tt restore apply`: verify checksums and archive-chain metadata before
  clearing restore-owned files. Restore the snapshot, WAL and vinyl trees
  to separate directories derived from `--config` and
  `--instance`, or selected with `--snapshot-dir`, `--wal-dir`, `--vinyl-dir`
  and `--work-dir`. Truncate WAL by its recorded log position while preserving
  its starting vclock, check restored journal continuity, target membership,
  snapshot identity and startup compatibility, and write a per-instance
  restore-state marker after success. Preserve vinyl space/index directories
  and route archive names by file type even when data directories overlap.
- `sdk/backup`: add filesystem and S3 object storage and streaming tar+zstd
  archive APIs. Filesystem writes use atomic replacement, validate the declared
  size, and sweep stale temporary files only on the first write; a missing
  storage is distinct from an empty prefix. S3 reads distinguish a missing key
  from a missing bucket. Archive readers reject unsafe names, links and
  special-file entries, preserving the snapshot, WAL and vinyl archive layout.
- `tt cluster`: add the worker publish, show and delete subcommands.

- `tt connect` and connector-based `tt replicaset` commands: add
  `--sslpassword` and `--sslpasswordfile` to decrypt private SSL keys, and
  `--connect-timeout` to bound binary connection establishment (default: 1s).
- `tt instance status`: add `--instance-timeout` to limit status collection
  for each instance; the default is 5 seconds, and `0` disables the timeout.
- `tt binary install tt`: install Community release binaries from GitHub Releases
  on Linux amd64/arm64 and macOS amd64 when available. Other platforms and
  releases without matching archives build from source automatically, sharing
  the Tarantool installation flow. Use `--source-build` to always compile from
  source. Archives are downloaded directly from release URLs. A missing archive
  (HTTP 404) falls back to a source build; other download errors and damaged
  archives stop installation.
- `tt binary install`: select `--scope project` (the default), `user`, or
  `system` when installing `tt`, `tarantool`, `tarantool-ee`, `tcm`, or
  `tarantool-sdk`.
  Project installations use `.tarantool/` in the project directory: the
  current directory or its nearest parent with `app.manifest.toml`,
  `.templates/<name>/instances.yaml` or `.templates/<name>/config.yaml`, so
  running from a subdirectory of a project installs into the project. Outside a project, the current
  directory is used. A directory inside the `versions/` or `apps/` tree of
  the user or system root is an installed version, not a project: the
  project scope refuses to work there and points at `--scope user` or
  `--scope system`.
  On Linux, user installations use `$XDG_DATA_HOME/tarantool` when
  `XDG_DATA_HOME` is absolute, falling back to `~/.local/share/tarantool`.
  System installations use `/opt/tarantool`. On macOS, the corresponding
  roots are `~/Library/Application Support/tarantool` and
  `/Library/Application Support/tarantool`. `TT_USER_ROOT` and
  `TT_SYSTEM_ROOT` override the user and system roots on any OS;
  `TT_USER_ROOT` takes precedence over `XDG_DATA_HOME`. Unlike a relative
  `XDG_DATA_HOME`, which is ignored, a relative value of either variable is
  an error.
- `tt binary search` and `tt binary install`: add `--credentials-file <path>`
  for remote EE/TCM/SDK searches and downloads, allowing customer-zone
  credentials to be supplied from a file without `tt.yaml`.
- `tt binary install tarantool-ee`, `tt binary install tarantool-sdk`, and
  `tt binary install tcm`: accept `X`,
  `X.Y`, and `X.Y.Z` to select the latest matching stable build, including from
  `--local-dir`. Omitting the version selects the latest stable build.
  EE and SDK use `gc64` by default. Complete version names select exact builds,
  including `nogc64` and RC.
- `tt binary switch` and `tt binary uninstall`: accept the complete numeric version `X.Y.Z`
  for EE/TCM/SDK when exactly one installed version matches in the selected scope.
  Ambiguous requests report matching full names without changing active links
  or removing installations. Full names select specific builds.
- `tt binary search tarantool-sdk` (aliases: `sdk`, `SDK`): search SDK archives
  and mark complete SDK installations in the selected scope. An SDK is active
  when all its components and headers are selected.
  `tt binary search tarantool-ee` separately marks standalone Enterprise
  installations.
- `tt binary list`, `tt binary switch sdk`, and `tt binary uninstall sdk`:
  list, select, or remove SDK versions in the selected scope. Switching an SDK
  selects its components and headers together. Individual components
  can also be selected using `sdk-<bundle-version>` identities, independently
  of standalone product versions.
- `tt binary install tarantool --from-build <dir>`: link an existing
  Tarantool build into the selected scope without copying or rebuilding it.
  `--include-dir` selects the source headers; no version argument is accepted.
  The command can also link an Enterprise build.
- `tt package build`: build a manifest package (`app.manifest.toml`) into
  `.rocks/` — resolve and lock dependencies, materialize the pinned closure,
  run each component's build backend, lay component files out under their
  install namespace and generate `version.lua`. `--product` selects the product
  and `--locked` fails on a stale lock instead of re-resolving.
- Manifest: executable components. A component with `kind = "executable"`
  builds the external processes of an application from their sources into
  `.rocks/bin/`, its `TT_OUTPUT_DIR`, instead of laying files out under the
  package's namespace; `include`, `exclude` and `namespace` are refused on it
  and `build` is required. It takes the new `go` backend (`go build -trimpath`
  for the build platform; fields `package`, `name`, `tags`, `ldflags`, `cgo`,
  `static`; cgo off unless `cgo = true`), the new `cargo` backend
  (`cargo build --profile --bin`, the binary's path taken from cargo's JSON
  messages; fields `bin`, `package`, `features`, `profile`, `static`), or
  `make` and `shell`. `static = true` makes a cgo Go program static
  (`-linkmode external -extldflags -static`, tags `netgo,osusergo`) and builds
  a Rust one for `<arch>-unknown-linux-musl`; it is refused for darwin. A
  field of another backend on a `go` or `cargo` build, or a field of `go` or
  `cargo` on another backend's build, is a validation error, and two
  components may not build one program name. A missing `go` or `cargo` fails
  the build before anything is built. Every program the build puts into
  `.rocks/bin/` must be an executable ELF or Mach-O program of the build
  platform, and a static one under `static = true`, or the build fails
  (exit code 2). The build records which files of `.rocks/bin/` are the
  package's own in `_build/executables.json`, and `tt package pack` carries
  them, and not the console scripts of rocks, in a `--without-deps` archive
  too, which they pin to the host platform; the programs of components outside
  the packed product travel in neither mode.
- `tt package fetch`: materialize `.rocks/` strictly from the lock, without
  re-resolving or running component build backends.
- `tt package pack`: run the full build and assemble the result into a
  reproducible `.tt` archive (`tar.zst`) at
  `_build/pack/<package>-<version>-<token>.tt`, printing the path to stdout. By
  default the archive is self-contained: it carries `_runtime/` (Tarantool, tt,
  and TCM when `[platform].tcm` is set) and the whole dependency closure, so
  installing it needs no network. `--without-deps` drops both and leaves the
  `bundled_*_version` lock fields empty. The `<token>` is the host platform
  (`linux-amd64`, `darwin-arm64`, ...), and `any` only for a `--without-deps`
  archive with no native file: any ELF or Mach-O file in the archive, by
  content rather than name — a compiled module, an executable from
  `[package].include`, a hook's output — pins it to the host platform, and one
  built for another OS or architecture fails the pack, naming the file.
  Runtime components are taken from
  `<user cache>/tt/runtimes/<component>/<flavor>/<version>/`, falling back to
  the active tt environment when it satisfies `[platform]`, with a warning. An
  Enterprise build satisfies a `[ce]` requirement (the default) as well as an
  `[ee]` one; a Community build satisfies only `[ce]`. The bundled Tarantool's
  license file travels with it when the build ships one, wherever it sits
  relative to the binary; the Enterprise SDK ships none, and the pack then says
  so and continues. A Community Tarantool built from a release tag
  (`3.8.1-0-ge643f88a5e0c`, zero commits after the tag) is recorded in
  `bundled_tarantool_version` as the release, `3.8.1` — the name
  `tt binary install tarantool 3.8.1` installs it under; a build off a tag
  keeps its full version, and Enterprise versions are recorded as reported.
  Archives are byte-reproducible for a given commit; set `SOURCE_DATE_EPOCH` to
  pin the build timestamp explicitly.
- `tt package install`: unpack a `.tt` archive into a scope and bring the tree
  to a runnable state. Every scope accepts both archive modes: a with-deps
  archive is a plain offline extraction, while a `--without-deps` archive
  additionally refetches its dependency closure from the registry using the
  lock's pins. The runtime a with-deps archive bundles in `_runtime/` is
  installed the way `tt binary install` installs it — the executable at
  `versions/<product>/<product>_<version>/<exec>`, Tarantool headers under its
  `include/` — into `versions/` of the scope root, unless that installation
  exists already, so `tt binary list` shows it and applications bundling the
  same runtime share it. The installation is named by the version the lock
  records (`bundled_tarantool_version` and so on), so a release Community
  Tarantool shares the installation `tt binary install` makes, while a build off
  a tag, an Enterprise build, tt and TCM are installed under the full version
  they report and may sit next to a `tt binary` installation of the same
  release. In the project scope that is
  `.tarantool/versions/`, and there is no `_runtime/` in the project any more;
  a project that selects no tarantool yet gets the bundled one selected in
  `.tarantool/bin` (and `.tarantool/include`), one that does keeps its choice
  with a warning. In `user` and `system` the active links `bin/` and `include/`
  are not touched: an installed application runs on the tarantool its lock
  bundles. Several packages installed into one
  project share a `.rocks/` tree, so a dependency they lock at different
  versions is reconciled to the highest locked version satisfying every
  package's constraints, or the install fails with a breakdown. Per-package
  metadata (manifest, lock, VERSION) is recorded under `.rocks/manifests/<pkg>/`
  for inventory and dependency refcounting. In `--scope user` and
  `--scope system` the archive becomes one version of an application, laid out
  the way `tt binary` lays out its products and under the same root
  (`TT_USER_ROOT`, by default `~/.local/share/tarantool`; `TT_SYSTEM_ROOT`, by
  default `/opt/tarantool`): `<root>/versions/<pkg>/<pkg>_<version>/` holds the
  whole archive like a project — manifest, lock and `VERSION` at its top,
  `.rocks/`, `.templates/`, payload and license files — and
  `<root>/apps/<pkg>` is pointed at it, replaced atomically. Install activates
  the version it installed; there is no command to switch between installed
  versions yet. Versions live side by side and share nothing, so there is no
  reconciliation there; the version comes from the archive's `VERSION`, and
  names of `tt binary` products (`tarantool`, `tt`, `tcm`, ...) are refused.
  `--locked`
  refuses an archive whose lock diverges from its manifest, `--upgrade` installs
  only a strictly higher version (in `user` and `system`, higher than every
  installed one), `--force` reinstalls over a collision (in `user` and
  `system`, replaces that version, refused while instances of the application
  run), and `--yes` skips the reconciliation prompt. Installing several
  archives at once exits 3 when some succeed and some fail.
- `tt package list`: show the packages installed in a scope — in the project
  scope the project's own package plus every guest installed from an archive,
  read from the metadata `tt package install` records under
  `.rocks/manifests/<pkg>/`; in `user` and `system` every application under
  `<root>/versions/` (skipping the products of `tt binary`), with the active
  version `<root>/apps/<pkg>` selects and every installed one (`versions` in
  the machine formats). Nothing is fetched and nothing is resolved; this
  reports what is on disk, not what the current manifest declares. `--scope`
  selects the tree and `-o/--format` picks the output: `table` (the default),
  `json` or `yaml`.
  `--tree` renders the scope as a single tree rooted at the project's own
  package, with each guest beneath it and each rock beneath whatever requires
  it, read from the LuaRocks tree manifest. Every rock is annotated with whether
  another package holds it too — that is, whether `tt package uninstall` would
  take it along or leave it. A rock the walk cannot root, because the tree
  carries no manifest or its edges are incomplete, is still shown, under a group
  that says its parent is not recorded. In the machine formats the same
  information arrives as a top-level `rocks` index giving each rock's owners and
  what it `requires`, plus a per-package `direct` list naming the declared
  subset of the closure. The index is `rocks` rather than `dependencies` because
  a package entry already carries a `dependencies` object, and one name must not
  stand for two shapes. `--tree` applies to the project scope only: the
  versions of `user` and `system` share no dependencies.
- `tt package uninstall <name>`: remove an installed guest package — its files
  and its metadata, plus every shared dependency nothing else still needs. A
  dependency is owned by every installed package whose lock pins it, so one
  another package still holds stays in place and is reported as kept. The
  project's own package is not a guest and uninstall refuses to remove it.
  In `--scope user` and `--scope system` it removes an application: every
  installed version under `<root>/versions/<pkg>/` and `<root>/apps/<pkg>`. It
  refuses while an instance of the application runs, as a pid file under
  `<root>/var/run/<pkg>/` naming a live process tells, and it keeps the
  instance data under `<root>/var/`, the host files under `<root>/etc/<pkg>/`
  and the runtimes in `<root>/versions/` other applications may use.
  `--scope` selects the tree and `--yes` skips the confirmation prompt.
- `tt package add <name> [<constraint>]`, `tt package remove <name>` and
  `tt package update [<name>]`: edit `[dependencies]` in `app.manifest.toml`
  and re-resolve the lock. `add` takes `*` when no constraint is given, writes
  to `[dev_dependencies]` under `--dev`, and rewrites the constraint of a
  dependency that is already declared, reporting the previous one. The manifest
  is edited in place: comments, key order and the formatting of every untouched
  part of the file survive, and a declaration form that cannot be rewritten
  safely is refused by name rather than guessed at. `add` and `remove` hold
  every version the lock already pinned, so they never drag an unrelated
  dependency forward; `tt package update` is the only command that pulls fresh
  versions from the registry, and with a name it moves that one dependency and
  holds the rest.
- `tt new`: create a skeleton `app.manifest.toml` in the current directory —
  the format version, the package name, the Tarantool and tt requirements and
  an empty `[dependencies]` table for `tt package add` to write into. The name
  comes from `-n`, or from the directory when that is already a package name;
  a directory named anything else is refused with the name it would have to be,
  rather than being silently rewritten. Components and products are left as
  commented examples, since their contents depend on a layout only the author
  knows; until products are declared, the manifest has one implicit product,
  `default`, built from every component, so `tt package add` and
  `tt package build` work on the skeleton as written. An existing manifest is
  never overwritten: the command fails instead.
  This is not `tt create`, which lays out a whole application from a template.
- `tt package resolve`: rewrite `app.manifest.lock` from the manifest without
  building — the way to bring the lock back in step after editing
  `[dependencies]` by hand. Nothing is fetched into `.rocks/` and no component
  build backend runs. Every version the lock already holds is kept, exactly as
  `add` and `remove` keep them, so a hand edit to one dependency does not drag
  the others forward.
- `tt package deps`: print the dependencies the current manifest declares, per
  product plus the dev closure, each with the version the lock resolved it to
  and whether it was declared or pulled in behind a declaration. It reads the
  manifest and the lock and contacts no registry, so a lock that no longer
  matches the manifest is reported as stale rather than silently re-resolved,
  and the command works on a machine with no Tarantool installed. `-o` selects
  the format (table, json or yaml; the default is the table). With the table
  the lock state is logged to stderr, so a redirected table is the table
  alone. This is what the manifest declares — what is installed on
  disk is `tt package list`.
- `[dev_dependencies]` are now resolved and installed. They were parsed and
  validated but reached neither the lock nor `.rocks/`. The lock records them
  as a single `[[lock.dev_dependencies]]` closure, resolved against the picks
  the products just made so a rock needed by both lands in the tree once;
  `tt package build` and `tt package fetch` install them, and `tt package pack`
  keeps dev-only rocks out of the archive. A lock written before this change,
  against a manifest that declares dev dependencies, is reported stale so the
  closure is filled in on the next resolve.
- Configurable rock registries. `[platform].registries` in
  `app.manifest.toml` declares the ordered server list a project resolves
  against, the `TT_REGISTRIES` environment variable (comma-separated)
  overrides it, and `--registry <url|dir>` on `tt package build`, `fetch`,
  `pack`, `resolve`, `add`, `remove`, `update`, `search` and `download`
  overrides both. The layers replace one another rather than merging: the
  list is ordered and resolution takes the first server that has a rock, so
  an appended server would change the answer instead of adding a fallback.
  With nothing configured the built-in defaults are used, and a dependency's
  own `registry` key still wins for that dependency. An entry is an HTTP(S)
  URL or a local directory; a relative path is taken relative to the manifest
  for the manifest's own list, and to the current directory otherwise.
- `tt registry list`: print the effective rock-server list in the order it is
  queried, each server with the layer it was configured in — the flag, the
  environment, the manifest or the built-in defaults. `-o` selects the format
  (table, json or yaml; the default is the table).
- `tt package search <term>`: find rocks whose name contains the term on the
  configured servers, reporting every version each server offers. Unlike
  resolution, which stops at the first server that has a rock, a search asks
  all of them. A term that matches nothing is not an error: the table form
  says so on stderr and writes nothing to stdout, and the machine forms print
  an empty list.
- `tt package download [<name>[@<version>]...]`: fetch rock files into
  `--dir` (the current directory by default) and write the LuaRocks
  `manifest` that indexes them, so the directory is itself a rock server —
  point `--registry` or `TT_REGISTRIES` at it and the project builds with no
  network at all. With no arguments the project's locked closure is mirrored
  at its exact versions: every product's dependencies and the dev closure,
  which needs a lock, so `tt package resolve` comes first. A dependency that
  does not come from a registry has no rock file to mirror and is skipped
  with a warning. Re-running overwrites and re-indexes, so a mirror can be
  extended in place. Each written path is printed to stdout.
- `-C <dir>` on `tt package` and its subcommands, `tt new` and `tt test`:
  work on the project in `<dir>` as if it were the current directory. A
  relative path resolves against the working directory, and a path that is
  not a usable directory is refused naming the flag, rather than surfacing
  later as a missing manifest. `tt run` does not take it: that command parses
  no flags of its own, since every argument is Tarantool's.
- Exit codes of the manifest commands (`tt package`, `tt registry`, `tt new`
  and `tt test`): 1 for what the user can fix - a bad manifest, a stale lock
  under `--locked`, a dependency that does not resolve, a malformed registry
  URL; 2 for the system the command ran on - a build backend that failed, a
  rock server that cannot be reached or does not answer in time, a filesystem
  that refuses an operation for want of permission or space; 3 for a
  multi-package install that partly succeeded. `tt help errors` describes
  them, and the root help lists it under a new HELP TOPICS section.
- `tt instance status`: add `--format` (`-o`) option to support JSON and YAML
  output formats for machine-readable output.
- `tt test`: build the package in the current directory and run its tests with
  luatest. Tests come from `test/`, or from `tests/` when there is no `test/`;
  a sub-path relative to the project narrows the run to one directory or one
  file and must exist. Everything after `--` is handed to luatest, and the exit
  code is luatest's own. luatest itself need not be declared: when
  `[dev_dependencies]` does not name it and the project tree does not already
  hold it, it is resolved as an implicit `*` requirement and installed —
  without being written to `app.manifest.toml` or `app.manifest.lock`, so
  running the tests never makes the lock stale. The interpreter is chosen the
  way `tt run` chooses it.
- `--log-format text|json`: a global flag that selects the format of the log
  tt writes to stderr. `json` prints one JSON object per record, with `time`,
  `level` and `msg` keys.
- `tt connect`: add the `\history [<count>]` command to display the last
  executed commands in the interactive console, 10 unless `<count>` says
  otherwise.
- `tt package pack`: carry the project's instance templates, `.templates/`,
  at the archive root in both packing modes, whatever `[package].include`
  says. Every template is checked first: each `.templates/<name>/` must hold
  `config.yaml`, `instances.yaml` or both; `instances.yaml` must be a valid
  instance description, and `config.yaml` is needed next to it unless the
  description has only `script` and `binary` records, while `config.yaml`
  alone runs its whole topology; the description must fit the topology of a
  `config.yaml` that holds the cluster config itself, as at the start of the
  instances, while one with `config.etcd` or `config.storage` is not read; a
  broken template, or a file directly under `.templates/`, fails the pack
  naming it. Hidden entries directly under
  `.templates/`, such as `.gitkeep`, are skipped. `.templates` joins the
  names tt reserves in the archive, so `include` and `license_files` entries
  may not land on it. `tt package install --scope project` lays the
  templates into the install root, and refuses an archive carrying templates
  when the root already has a `.templates/`; the `user` and `system` scopes
  lay them into the installed version.
- `tt instance start`, `stop`, `status`, `restart`, `kill`, `logs`,
  `logrotate` and `clean`: run external processes, the records of
  `instances.yaml` with `binary`, next to the Tarantool instances and under
  the same watchdog. The process runs `binary` with `args` in `workdir`, or
  else in its data directory `<root>/var/lib/<app>/<inst>/`, which the start
  creates, with `env` on top of the environment of `tt`; relative paths are
  resolved against the application directory, and a `binary` without a slash
  is looked up in `.rocks/bin/` of the application directory, where
  executable components put the programs they build, then in `PATH` of `tt`.
  `tt instance start` reports a binary it cannot run, naming both places it
  looked in, or a `workdir` that does not exist, before it starts anything;
  it builds nothing, and when the project's manifest has an executable
  component that builds the missing binary it says to run
  `tt package build`.
  The process leads a process group of its own, which every signal reaches, so
  that a wrapper stops with the program it runs. It stops with `stop_signal`,
  SIGTERM by default, whatever stopped the watchdog, and is killed with its
  group after `stop_timeout`, 30 seconds by default; `restart` applies as to a
  Tarantool instance. Its output goes to `<root>/var/log/<app>/<inst>/tt.log`,
  and its watchdog keeps `tt.pid` and `process.pid`, the pid of the process,
  in `<root>/var/run/<app>/<inst>/`. `tt instance status` shows the state of
  the process only. `tt instance logrotate` reopens `tt.log` and sends the
  process group the new `logrotate_signal` field of the record, if it has one;
  the field is refused in the records of Tarantool instances, which get SIGHUP
  as before. `tt instance clean` removes the log and the data directory, and
  `tt instance check` checks that the binary of the process is found.
  `tt connect`, `tt play`,
  `tt instance kill --dump` and `tt replicaset rebootstrap` refuse it as not a
  Tarantool instance; the application targets of `tt replicaset` and
  `tt cluster` leave external processes out of the instances of the
  application and refuse one named explicitly. Instances start in the order of
  `instances.yaml`, and none waits for another to be ready. A process whose
  watchdog was killed by something else than `tt instance kill` runs on in its
  group: `tt instance status` does not show it running and names it in an
  alert, `tt instance start` refuses to start a second one, and
  `tt instance stop` and `tt instance kill` stop it by `process.pid`.
- `tt service systemd generate | install | uninstall <app>`: make the systemd
  units that run the instances of an application installed in the system
  scope; the user scope and projects are refused as not supported. The
  template unit `<app>@.service` runs
  `<root>/bin/tt instance start --foreground --scope system <app>:%i`, or the
  `tt` that makes it, with a warning, when the scope has no `tt`, with
  `TT_SYSTEM_ROOT` set to the root of the scope, as the user and the group
  `--run-as <user>[:<group>]` names, `tarantool:tarantool` by default, after
  `network.target`. `tt` stops the instance itself, so the
  stop signal of systemd goes to `tt` alone (`KillMode=mixed`). The drop-in
  `<app>@<instance>.service.d/tt.conf` of every instance sets `Restart=` from
  its `restart` and `TimeoutStopSec=` from its `stop_timeout`, 30 seconds by
  default, plus 5 seconds, so that systemd kills nothing before `tt` does.
  `generate` prints the units, or writes them into `--unit-dir`. `install`
  writes them into `/etc/systemd/system`, or `--unit-dir`, records the
  instances they manage and `--run-as` in `<root>/etc/<app>/systemd.yaml`,
  keeps the recorded `--run-as` when the flag is not given, warns when the
  host has no such user or group, removes the
  drop-ins of instances gone from the description, and prints the
  `systemctl` commands to run rather than running them; it refuses a unit file
  it did not write and an instance that runs without a unit. `uninstall`
  removes the units and the record, from the record alone, exits with 0
  saying so when there is no record, and refuses while an instance runs under
  its unit or the record cannot be read. `tt instance start`, `stop`, `restart`,
  `kill` and `tt replicaset rebootstrap` refuse the instances the units
  manage, pointing at `systemctl`, but for the start the unit runs, which it
  names in `TT_SYSTEMD_UNIT`; `tt instance status` shows their units in a
  `UNIT` column and a `unit` field.

### Changed

- tt takes the binary port of an instance from its config: the first unix
  socket of `iproto.listen`, unless the env of its record sets
  `TT_IPROTO_LISTEN`. `tt connect --binary`, `tt play` and the cleanup at stop
  find the socket the config puts elsewhere than the layout.
- An instance installed into the user or the system scope may have a
  relative path in its config that leads out of its version directory, such
  as `../../../var/lib/<app>/{{ instance_name }}` to the layout of the scope
  root; only one that leads into the version is refused.
- A signed install links `var/lib`, `var/run` and `var/log` of the version
  to `<root>/var/lib/<app>`, `<root>/var/run/<app>` and `<root>/var/log/<app>`.
  Tarantool Enterprise starts with an empty environment when it checks
  integrity, so the `TT_*_DEFAULT` variables tt passes the layout through do
  not reach it, and it puts the files of an instance where the defaults of
  Tarantool say, under the version; through the links they are in the layout
  of the root, and a config with no path runs checked as it runs unchecked.
  The data outlives an upgrade, and an uninstall removes the links, not the
  data. `tt instance start --integrity-check` makes the links a version
  lacks, when it may, and refuses a version with something else there; a
  signed package cannot have its own `var/`. A config may still name the
  paths itself, as the README shows.
- With the integrity checks on, the `env` of the record of a Tarantool
  instance, which does not reach Tarantool Enterprise, is refused by the pack
  of a signed package and by `tt instance start`, and so is a record that
  runs its script, whose launcher takes the script and the paths of the
  instance from the environment.
- `mage build` without `TT_CLI_BUILD_SSL`, or with `default`, builds with
  `CGO_ENABLED=0`: the default TLS is pure Go, so the binary is static and
  needs no C library; the OpenSSL builds still use cgo. `tt binary build tt`
  decides the same way.
- Integrity checks: an instance command checks each application at its own
  directory - the project, or the installed version - and at the
  directories of the runtimes it runs on, rather than at the working
  directory, and runs only what they sign: its tarantool (not one from
  `PATH`), the script of a record, the binary of an external process (looked
  up in `.rocks/bin/` only, not in `PATH`) and the tt of its watchdog. The
  pair of the host in `<root>/etc/<app>/` is not signed and is refused with
  the checks on.
- `github.com/tarantool/tt/sdk/integrity`: `Signer.Sign` signs the tree of a
  package (`SignOptions`) rather than a tt 2 environment, and no longer needs
  `tt.yaml`; `Signer.SignFiles` signs a runtime by the paths it is installed
  at; `Provider.VerifyTree` checks a whole tree before it is installed, and
  takes a symbolic link that leads nowhere for no file; and
  `InitializeIntegrityCheck` takes several roots.
- The instance commands read the cluster config of an application, a
  centralized one included, once per command rather than once per instance.
- `tt instance status`: collect instance statuses in parallel.
- `tt instance stop` and `tt instance restart`: interrupt all selected instance
  supervisors before waiting for them to exit, allowing instances to shut down
  in parallel.
- `tt instance logs --follow` / `tt tcm log --follow`: only complete lines
  are printed. A half-written line stays buffered until its newline arrives
  instead of being split into two records.
- `tt instance`: the commands that act on the instances of an application move
  under `tt instance`: `start`, `stop`, `restart`, `kill`, `status`, `logs`
  (was `tt log`), `logrotate`, `check`, `clean` and `connect`. The top-level
  `tt start`, `tt stop`, `tt restart`, `tt kill`, `tt status`, `tt log`,
  `tt logrotate`, `tt check` and `tt clean` are removed without aliases.
  `tt connect` stays as the facade of `tt instance connect`: the same command
  with the same flags. Every command of the group takes
  `--scope project|user|system` and `--template <name>`, and so do the
  commands of `tt replicaset`, `tt cluster show` and `publish`, `tt play` and
  `tt aeon connect`: an application target there is found, and its pair read,
  as the group finds and reads it, so an application of the user scope is
  reachable and the cluster commands act on the template the instances run
  from; a URI target ignores them. `tt backup start` and `finalize` take them
  for an `<APP>:<INSTANCE>` target as well. `tt cluster topology <APP>` and
  `tt backup plan <APP>` read the config of the pair of the application and
  take a relative unix socket of an instance against the application
  directory the instances run in, and
  `tt restore apply <APP>:<INSTANCE>` restores into the data directories tt
  starts the instance with, refusing one that runs. The watchdog of an
  instance runs as `tt instance start --watchdog`. `--foreground` replaces
  `-i` of `tt start`, for systemd, a container or a terminal: `tt` stays in
  the foreground while every instance runs under a watchdog of its own,
  `tt instance start --watchdog --foreground`, which does not restart it and
  leads a process group of its own with its own pid file, so that
  `tt instance stop`, `kill`, `logrotate` and `status` act on one instance
  from another terminal and Ctrl-C reaches an instance once; the output of the
  instances goes to their `tt.log` and to the standard output, prefixed with
  the instance when there are several; SIGINT, SIGTERM and SIGQUIT stop every
  instance as a stop of its watchdog does; an instance that ends does not stop
  the others, and `tt` writes with what code it exited; `tt` exits once all
  have ended, with 0 when they were stopped in time, a process killed by its
  stop signal included, with 137 when one had to be killed after its
  `stop_timeout`, and with the exit code of the first instance that ended on
  its own with another code otherwise; an instance that runs already is an
  error before anything starts. A command over several instances goes on after
  an instance it fails for, where `tt start` stopped at the first watchdog
  that did not start and `tt check` at the first file with an error, and exits
  with 3 when it failed for some of the instances, with 1 when it failed for
  all of them, and with the code of the error of the instance when it acted on
  one. `-f` is no longer the short form of follow, format or a confirmation in
  the group: `tt instance logs --follow` has no short form,
  `tt instance status -o` selects the format, and `tt instance kill` and
  `tt instance clean` take `--yes` (`-y`) where they took `-f`/`--force`.
  `stop`, `restart`, `kill` and `clean` ask for a confirmation on a terminal
  only: without a terminal on the standard input and without `--yes` they fail
  at once with code 1, pointing at `--yes`, and read no answer from the
  standard input; a no on the terminal is no failure. `--no-prompt` no longer
  confirms `tt instance stop`: `--yes` does. `tt instance clean` asks once for
  all its targets, removes nothing for an instance that never ran instead of
  failing it, and counts a running instance it leaves alone as failed; next
  to `<root>/var/lib/<app>/<instance>/` and `<root>/var/log/<app>/<instance>/`
  it removes the data directories the config of the instance moves elsewhere
  (`wal.dir`, `snapshot.dir`, `vinyl.dir`).
  Completion of a target offers the current project and the applications of
  the system scope, or of the scope `--scope` names, each followed by `:`, and
  then the instances of the pair of the application named; the usage, the
  completion and the errors spell a target `<APP>:<INSTANCE>` and `<APP>:*`,
  as do the usage and the errors of `tt replicaset`, `tt cluster show` and
  `publish` and `tt aeon connect`, which spelled it `<APP_NAME:INSTANCE_NAME>`,
  `app:instance` or `application_name:instance_name`.
  `tt instance status --details` names the tarantool every Tarantool instance
  starts on and where it comes from: the version the lock bundles or the
  highest one `[platform].tarantool` allows in the user and system scopes,
  the tarantool the project selects or the one on `PATH` in a project. The
  messages that suggest a command name the new one, as the follow-up message
  of `tt create` does.

- `tt instance start`, `stop`, `status`, `restart`, `kill`, `logs`,
  `logrotate`, `check`, `clean` and `connect`, `tt connect`, `tt play`, the
  application targets of `tt replicaset` and `tt cluster`: the instance
  commands no longer use `tt.yaml`. An application is the project found from
  the current directory upward (`app.manifest.toml`,
  `.templates/<name>/instances.yaml` or `.templates/<name>/config.yaml`), or
  `<root>/apps/<app>` of the system scope; it is named by `[package].name` of
  its manifest or after the project directory. Its instances and cluster
  config come from the pair `.templates/default/{instances,config}.yaml`, or
  `<root>/etc/<app>/` for an installed application. The record `"*"` of
  `instances.yaml` stands for every instance of the topology of `config.yaml`
  that the description does not name, started in its place in the order of
  their names, as Tarantool instances with the `restart`, `stop_signal`,
  `stop_timeout` and `env` of `"*"` (`binary` and `script` are refused in it;
  unquoted, `*:` is a YAML alias, and the error says to quote it); a record
  naming one of them replaces `"*"` for it whole. The names of the other
  records are checked against the topology, and a `script` or `binary`
  record named as an instance of the topology is an error rather than a
  silent stand-in for it. A template of `config.yaml`
  alone is the description `"*": {}` and runs the whole cluster; the pair of
  a host, which says what runs on that host, needs its `instances.yaml`,
  `"*"` or not, and is incomplete without it. A
  description with only `script` and `binary` records needs no `config.yaml`
  and does not read one, while `tt cluster show` and `publish` refuse an
  application without it. `tt cluster publish` writes the
  config of an application of the user or the system scope into its pair of
  the host, `<root>/etc/<app>/config.yaml`, and refuses one that runs from a
  template of its installed version, which is never written to, pointing at
  `<root>/etc/<app>/` and at the URI of a config storage. It reads the pair
  with its config as far as the config can be read and without checking it
  against the topology, so a pair out of step or a `config.yaml` that does
  not parse is mended by publishing, and the first config goes into an
  empty storage, and refuses a config that drops a Tarantool instance the
  description names, or that names an instance of the cluster as a `script`
  or `binary` record, naming them, while the instances `"*"` stands for may
  be dropped; publishing to a config storage by its URI checks nothing, as
  before. Into a pair whose `config.yaml` points to a config storage with
  `config.etcd` or `config.storage`, it publishes into that storage, with the
  endpoints, the prefix, the credentials and the TLS settings of the
  reference, and leaves the file and its reference as they are. Tarantool takes
  `config.etcd` and `config.storage` in Enterprise Edition alone: with a
  tarantool of Community Edition, `tt instance start`, `restart`, before it
  stops anything, and `check` refuse a Tarantool instance of the cluster
  config whose config points to a storage, saying why, while tt itself
  reads the storage in either edition. `tt instance stop`,
  `kill`, `status`, `logrotate` and `logs`, the completion of a target and
  `tt replicaset status` read `config.yaml` and the config storage it names
  as far as they can, as they did with `tt.yaml`: they find the instances by
  `instances.yaml` and their pid files, and `"*"` stands for the instances
  with a directory in `<root>/var/run/<app>/`, those that run or have run
  there, when the config cannot be read, so a broken config or an etcd that is
  down does not keep them from the running instances, which then get the
  paths of the layout, after the wait for the storage; a config that can be
  read still moves the console socket and the other paths it sets, and
  `"*"` then stands as well for the instances the topology lacks whose
  directory there holds the pid file `tt.pid`, so an instance dropped from
  the topology while it runs is still listed, stopped and looked at, with
  the fields of `"*"` and the paths of the layout, until it stops. Data,
  logs, pid files and sockets go to
  `<root>/var/{lib,log,run}/<app>/<instance>/`, where `<root>` is `.tarantool`
  in a project. Targets are `<APP>:<INSTANCE>`, `<APP>:*` and
  comma-separated lists of them, or none for every instance of the current
  project; a bare `<APP>` is refused with a hint. The restart policy is the
  `restart` field of the instance record: a restart after a crash stays on the
  version the instance started from and re-reads the pair, while
  `tt instance restart` takes the active version, and checks every target as
  `tt instance start` does, its pair, tarantool and layout, before it stops
  any: a target that cannot start fails the command with code 1, naming it,
  and nothing is stopped. `tt instance check` checks the `app.file` of each
  instance. A record with `script` is a Tarantool
  instance configured by its own Lua script, as `init.lua` and
  `<instance>.init.lua` of tt2 were: `tarantool` runs the script, relative to
  the application directory, in the application directory, and `box.cfg` takes
  the data directory and the binary port of the layout from `TT_WAL_DIR`,
  `TT_MEMTX_DIR`, `TT_VINYL_DIR` and `TT_LISTEN` for the options the script
  does not pass. The console socket and `tt.log` are those of any instance;
  `env` of the record may move the data with the same variables, relative
  paths there being refused in the `user` and `system` scopes. `TARANTOOL_CFG`
  is no longer set. `tt instance check` checks the script. `tt cluster show`
  and `publish` refuse such an instance, and the application targets of
  `tt replicaset` leave it out next to the instances of the cluster config; an
  application of such instances alone keeps them, and `tt replicaset` chooses
  the custom orchestrator for it without `--custom`. `<name>.lua`, `init.lua`
  and `<instance>.init.lua` applications next to `tt.yaml` are not recognized
  any more: a pair with a `script` record runs them.

- `tt binary install tarantool [version]`: install prebuilt Community release
  binaries and matching headers from RWS by default on Linux amd64/arm64 with
  glibc, activating the installed version in the selected scope. Omitting the
  version selects the latest stable release; `X` and `X.Y` select the latest
  matching stable release, and `X.Y.Z` selects a specific release.
  Build from source automatically when the platform or matching packages are
  unavailable. Use `--source-build` to always compile Community releases.
  Git commits, `master`, pull requests, `--local-repo`, `--dynamic`, and
  `--use-docker` also select source builds.
  Installation details about copying files, replacing installations, and
  updating active links are now shown only with `--verbose` (`-V`).
- `tt binary uninstall`: remove versions only from the selected
  `--scope project` (the default), `user`, or `system`, using the same installation
  paths as install. Version completion and automatic selection of a replacement
  after removing the active version use only that scope. Unlinking a local
  Tarantool build preserves its source files. Installation paths no longer
  depend on `env.bin_dir` or `env.inc_dir` in `tt.yaml`.
- `tt binary switch`: select installed versions only in the chosen
  `--scope project` (the default), `user`, or `system`, without falling back
  to other scopes. Switching CE or EE updates both the active executable and
  headers in that scope. Installation paths no longer depend on
  `env.bin_dir` or `env.inc_dir` in `tt.yaml`.
- `tt binary list`: show installations only in the selected `--scope project`
  (the default), `user`, or `system`. Project scope is `.tarantool/` in the
  project directory, as for `tt binary install`, without falling back to
  other scopes. Installation paths no longer depend on `env.bin_dir` in
  `tt.yaml`.
- `tt binary search`: show `[installed]` and `[active]` labels for the selected
  scope only. Scope does not affect the available versions returned by remote
  sources or the local repository.
- `tt binary install` and `tt binary search`: use `--local-dir <dir>` instead
  of `--local-repo` for EE/TCM/SDK archives. The directory contains
  `tarantool-enterprise-sdk-*.tar.gz` or `tcm-*.tar.gz` archives directly.
  For `tt` and `tarantool`, `--local-repo <dir>` expects Git source repositories
  in `<dir>/tt` or `<dir>/tarantool`. Help describes each product's input format.
  Source flags belong to product commands and must follow the product name.
- `tt binary search`: require an explicit local source directory instead of
  reading `repo.distfiles` from `tt.yaml`. Relative paths are resolved from
  the current directory.
- `tt binary install`: no longer require `tt.yaml` for EE, TCM, or SDK. The
  `--local-repo <dir>` and `--local-dir <dir>` flags take the source directory
  instead of reading `repo.distfiles`; relative paths are resolved from the
  current directory.
  Docker builds pass the directory through the flag without generating a
  `tt.yaml` configuration.
- `tt binary install`: keep each installed version under
  `<scope>/versions/<product>/<product>_<version>/`, with the executable directly
  in that directory and Tarantool headers in `include/tarantool/`. Active executable
  links are in `<scope>/bin/` and Tarantool headers selected by
  `<scope>/include/tarantool`. CE and EE keep separate version directories and
  share the active Tarantool links. Add `<scope>/bin` to `PATH` to use the
  selected executables.
  Reinstalling with `--reinstall` replaces only the selected version.
- `tt binary install tarantool-sdk [version]` (aliases: `sdk`, `SDK`) now installs
  and activates Tarantool Enterprise, tt Enterprise, TCM, and headers in the selected
  scope. This replaces downloading
  an archive to `--destination`; SDK archives can be installed from Customer
  Zone or a local directory with `--local-dir`.
  SDK removal preserves independently selected versions and tries to select
  replacements for removed active components. Unusable candidates are skipped;
  a failed replacement warns without failing removal. TCM replacements are
  compared by TCM's own version.
- `tt binary install tt-ee`: install tt Enterprise from the same SDK archive as
  Tarantool Enterprise. `tt-ee` has separate version directories and supports
  search, list, switch, and uninstall, sharing `bin/tt` with ordinary tt.
  Removing an active tt-ee version can select a remaining tt-ee or ordinary tt;
  comparisons use the executable's own version rather than its SDK bundle version.
- `tt binary install`: the first successful project installation of any product
  creates `.env/activate.sh` and `.env/deactivate.sh` in the project directory,
  preserving existing scripts.
  Activation sets the project directory, binary search path, and Tarantool header
  prefix and enables tt completion for Bash and for Zsh with `compinit` loaded;
  deactivation restores the previous shell environment. User and system
  scopes do not create these scripts.
- `tt instance start`: a failed periodic integrity check
  (`--integrity-check-period`) stops the instance for good. The watchdog kills
  it and exits, whatever `restart_on_failure` says; starting it again is up to
  an administrator. Before, an instance with `restart_on_failure: true` was
  restarted over the files that had failed the check.
- `tt tcm start --watchdog` runs TCM under the watchdog `tt instance start`
  uses:
  - `tt tcm stop` stops TCM with SIGTERM and kills its process group if it
    still runs 30 seconds later. Before, the watchdog waited for TCM without
    a limit.
  - SIGHUP and SIGQUIT sent to the watchdog stop TCM as SIGINT and SIGTERM
    do. Before, they killed the watchdog and left TCM running without it.
  - The watchdog removes `watchdog.pid` and `tcm.pid` when it exits.
  - With `--integrity-check-period`, the integrity of the environment is
    checked periodically while TCM runs, and a failed check stops TCM for
    good. With `--integrity-check` and no period, the period is one day, as
    for `tt instance start`.
- `tt tcm status` finds TCM the way `tt tcm stop` does: through
  `watchdog.pid` first, and reports TCM as `RUNNING` while the watchdog runs,
  also while it waits to restart TCM; through `tcm.pid` otherwise. With
  neither file it reports `NOT RUNNING` and exits with 0. Before, it read
  `tcm.pid` only, and failed with `path does not exist` without it.
- `tt instance stop`, `tt instance kill --dump`, `tt tcm stop` and
  `tt daemon stop` wait 35 seconds for the process to exit, longer than the 30
  seconds a watchdog gives its child before it kills it. Before, they gave up
  after 30 seconds and reported a failure while the watchdog was completing
  the stop.
- tt logs through `log/slog`. The text log keeps its look with two changes:
  a warning is marked `⚠` instead of `•`, and a message is no longer padded
  with trailing spaces. A debug line is marked `·`, the continuation lines of
  a multi-line message are indented to the message, and colour is used only
  when stderr is a terminal and `NO_COLOR` is not set.
- The spinner shown while a build step runs is drawn on stderr, and only when
  stderr is a terminal: output captured from stdout no longer carries spinner
  frames and carriage returns. It is not shown with `--log-format json` or
  `TERM=dumb`, and log lines printed while it spins stay whole.
- `tt package deps|list|search` and `tt registry list` print a table by
  default also when stdout is not a terminal; use `-o yaml` or `-o json` in
  scripts.
- A failure of the system a command ran on exits 2 from every command, not
  only from the manifest commands: a server that cannot be reached or does
  not answer in time, a host name that does not resolve, a filesystem that
  refuses an operation for want of permission or space. `tt connect` to a
  port nothing listens on exits 2 where it exited 1. Everything else that
  exited 1 still does; `tt help errors` describes the codes for all commands.
- Credentials are masked as `[REDACTED]` in every log record: the userinfo of
  URIs such as `admin:secret@host:3301` or `https://token@host`, in the
  message and in attributes, in both log formats.
- The Go module path is now `github.com/tarantool/tt/v3`, so the v3 release
  tags can be consumed with `go get`. Code importing tt packages must switch
  to the `/v3` path.
- The separate Go modules `lib/cluster`, `lib/connect`, `lib/dial` and
  `lib/integrity` moved to `sdk/` as `github.com/tarantool/tt/sdk/cluster`,
  `github.com/tarantool/tt/sdk/connect`, `github.com/tarantool/tt/sdk/dial`
  and `github.com/tarantool/tt/sdk/integrity`. Their API is alpha and may
  change incompatibly.
- Integrity checks: the root tt hands the integrity provider is the project
  directory, found from the current directory upward, or the current
  directory outside a project, instead of the directory of `tt.yaml`; a
  project that cannot be looked for fails the command while a public key
  enables the checks.
- Updated Go client dependencies to `go-tarantool/v3`, `go-config/v2`,
  `go-storage/v2`, and `go-tlsdialer/v2`.
- TLS build modes are now selected through `TT_CLI_BUILD_SSL`: leave it unset or
  set it to `default` for pure-Go `gostls`, use `openssl` for dynamically linked
  OpenSSL, or `openssl_static` for OpenSSL and a statically linked binary. The
  static mode requires OpenSSL static libraries at build time. The former `no`,
  `shared`, and `static` values are no longer accepted.
- The multi-application layout of a tt environment, `env.instances_enabled`
  of `tt.yaml`, is no longer supported.
- `tt create` needs no `tt.yaml`: it creates the application directory in
  the current directory, or under `--dst`, and no longer creates a symlink in
  `instances.enabled`. A template of your own is given by its path, a
  directory or a `.tgz`/`.tar.gz` archive (`tt create ./templates/basic`),
  instead of being looked up in the `templates` paths of `tt.yaml`; a name
  without a slash is a built-in template. The `rundir` template variable,
  which came from `app.run_dir` of `tt.yaml`, is gone. The built-in templates
  create a project that starts as it is: the pair it runs from,
  `instances.yaml` and `config.yaml`, is in `.templates/default/` rather than
  next to the code of the application, and there is no `tt.yaml`;
  `single_instance` names the files `.yaml` rather than `.yml`.
  `instances.yaml` holds `"*": {}`, every instance of the topology of
  `config.yaml` in the order of their names, rather than a list of the names
  of the topology, with a comment on giving the instances settings such as
  `restart` and on adding an external process.
- `tt create` refuses an application name that the template turns into a
  `[package].name` of `app.manifest.toml` that is not a package name
  (lowercase letters, digits and dashes, starting with a letter), and creates
  nothing. `vshard_cluster` replaces `_` in `--name` with `-`, so
  `--name my_app` is a project called `my-app`, while `--name My_App` is
  refused. Templates without a manifest take any name, as before.
- The `vshard_cluster` template depends on vshard 1.0.0 instead of 0.1.25,
  which no longer builds with CMake 4.
- `tt enable` and `tt instances` commands removed.
- `tt.yaml` is not read by any command: tt no longer looks for it from the
  current directory upward, nor at `/etc/tarantool/tt.yaml`. The `-S`
  (`--system`), `-L` (`--local`) and `-c` (`--cfg`) flags and the
  `TT_CLI_CFG` and `TT_SYSTEM_CONFIG_DIR` variables, which chose a `tt.yaml`,
  are removed, and so is the `-c` (`--config`) flag of `tt backup start` and
  `tt backup finalize`, which named the `tt.yaml` to resolve an
  `<APP>:<INSTANCE>` target with. tt no longer runs the `tt` of `bin_dir` in
  place of itself;
  `source .env/activate.sh`, which `tt binary install` creates in a project,
  puts the project's `tt` on `PATH` instead, and the `-s` (`--self`) flag,
  which kept tt from doing so, is removed too. When tt asks another `tt` for
  its version, it still passes `--self` first, so that an older `tt` does
  not answer for the `tt` of its `bin_dir`, and asks again without it only
  when that `tt` knows no such flag. The `tarantool` and `tcm` the commands
  run are the project's `.tarantool/bin/tarantool` and `.tarantool/bin/tcm`,
  which `tt binary install` and `switch` set, and otherwise the first on
  `PATH`; `tt rocks` and the `tt package` builds take the headers of a
  project `tarantool` from `.tarantool/include/tarantool`. The project is
  found from the current directory upward, as the instance commands find it.
- The deb and rpm packages no longer install `/etc/tarantool/tt.yaml`, nor
  the system tt environment it described: the `sys_env` directories under
  `/var/lib/tarantool`, `/var/run/tarantool` and `/var/log/tarantool`, and
  `/etc/logrotate.d/tt.conf`, which ran `tt -S logrotate` on their logs.
  Upgrading a deb package leaves the old configuration files in place until
  the package is purged.
- `tt cfg`, `tt cfg dump` and `tt env` commands removed along with
  `tt.yaml`: there is no tt environment left to print or to put on `PATH`.
  `source .env/activate.sh`, which `tt binary install` creates in a project,
  puts the binaries of the project on `PATH` instead of `. <(tt env)`.
- `tt rocks` queries the rock servers of `tt registry`: `TT_REGISTRIES`, or
  `[platform].registries` of `app.manifest.toml` in the current directory,
  replace the servers LuaRocks queries by default, as they replace the
  defaults of `tt package`; without either LuaRocks keeps its own. `repo.rocks`
  of `tt.yaml`, which added a server LuaRocks queried first, is not read any
  more; `TT_CLI_REPO_ROCKS` still adds one.
- `tt run` now runs the package in the current directory rather than an
  instance of a tt environment. The directory must hold `app.manifest.toml`; no
  `tt.yaml` and no tt environment are needed, and none is looked for. The
  interpreter is chosen by the rule the instance commands follow for a project:
  the one `.tarantool/bin/tarantool` in the directory selects (set by
  `tt binary install`/`switch`, and by a with-deps `tt package install` to the
  bundled runtime when the project selects none), and the first `tarantool` on
  `PATH` otherwise, with `TT_USE_SYSTEM_TARANTOOL=1` forcing `PATH` either
  way; `bin_dir` of `tt.yaml` plays no part. An installed version of an
  application (`<root>/versions/<app>/<app>_<ver>` of the user or system
  scope, or its `<root>/apps/<app>` link) is no project: it runs on the
  tarantool its lock names in that scope, never on `PATH`. `tt test` and the
  Tarantool modules get through the SDK follow the same rule. Arguments still reach
  Tarantool untouched,
  `--` included, and tt still replaces itself with the interpreter, so signals
  and the exit code are Tarantool's own.
- `tt init`, `tt pack`, and `tt build` commands removed. Use
  `tt package build` and `tt package pack` for manifest-based applications.
- `tt package list`: the table's `KIND` column is now `ORIGIN`. `kind` names
  what a component is (`library` or `binary`); what this column reports is
  where a package came from — the project itself or an installed archive.
- `tt instance status`: remove the `--pretty` option; use
  `--format pretty-table`.
- tarantoolctl layout is no longer supported.
- `tt create`: cartridge support removed.
- tarantoolctl support has been completely removed.
- Removed support for Tarantool 1.10 and 2.x. Only Tarantool 3.x is supported.
- `tt binary search` and `tt binary install`: Tarantool Enterprise and the
  Enterprise SDK below 3 are no longer listed or installed, as Community
  Edition already is not. The standalone Enterprise names `gc64-<version>` and
  `nogc64-<version>` of 2.x are gone: every version is installed without the GC
  prefix, such as `3.8.1`. Enterprise 2.x installed earlier is still listed by
  `tt binary list` and is switched to or removed by `tt binary switch` and
  `tt binary uninstall` under its installed name, such as `gc64-2.11.1`.
- `lib/cluster`: removed `EnvCollector` and the generated `ConfigEnvPaths`;
  `cli/cluster` now loads config files and environment variables via upstream
  go-config.
- `tt cluster show --validate` and `tt cluster publish` now validate against the embedded
  Tarantool JSON Schema instead of the in-tree enumerated path validator. The new validator
  is stricter (rejects unknown top-level / scope properties) and produces error messages in
  the upstream `<path> [code] <message>` format.
- `tt rocks` now runs on the pure-Go go-luarocks engine
  (`github.com/tarantool/go-luarocks`) instead of the bundled LuaRocks git submodule.
  Behavior is unchanged; the `cli/rocks/third_party/luarocks` submodule is removed.
- The `tt package` dependency pipeline (resolve, fetch, build) now runs on the
  pure-Go rock engine end to end instead of dispatching installs through the
  embedded Lua interpreter. `tt rocks` keeps the Lua engine, which is what
  serves the full LuaRocks command line.
- A rock server may now be a local directory: any entry that is a filesystem
  path or a `file://` URL — in a dependency's `registry` key or in the server
  list — is read off disk, so a directory holding a LuaRocks `manifest` works
  offline exactly as an `https://` server does.
- `tt aeon connect` is a module built on the tt SDK. `APP:INSTANCE` and
  `PATH INSTANCE` read the cluster configuration the way `tt cluster show`
  does, with the `TT_*` environment and the etcd or Tarantool config storage
  the file names; `--scope` and `--template` find the application of
  `APP:INSTANCE` as the instance commands find it, and the SDK gives a module
  the same flags (`sdk.AddAppFlags`, `sdk.ClusterSource.WithLookup`).
  `URI INSTANCE` accepts a storage URI without credentials,
  taking them from `-u`/`-p`, and a storage that cannot be read is reported
  instead of ending in a failed connection to an empty address. The SSL files
  the `--ssl*` flags name take precedence over the advertise parameters in
  every form. An external module named `aeon` replaces the command unless
  `-I` is given.
- External modules are found only in the directories listed in
  `TT_CLI_MODULES_PATH`, separated by colons. tt no longer reads module
  directories from `modules.directory` in `tt.yaml`, nor searches the default
  `modules` directory next to it. This breaks setups that keep external
  modules in the tt environment, next to `tt.yaml` or in a directory it names:
  those modules are not found until their directories are listed in
  `TT_CLI_MODULES_PATH`, and a command such a module replaced, `tt version`
  for one, runs tt's own implementation again. A relative directory in
  `TT_CLI_MODULES_PATH` is taken from the working directory, not from the tt
  environment as `modules.directory` was.
- An external module named like a top-level command replaces it whatever
  built the command, aliases included: with a `replicaset` module, `tt rs`
  runs the module. `-I` keeps every command of tt, its help included. The
  replacement is silent, as for tt's own commands before; `-V` logs it. A
  module named like another command's alias, such as `rs`, is ignored with a
  warning.
- `tt modules`, `tt help`, `tt completion` and `tt version` cannot be replaced
  by an external module: a module named so is ignored with a warning and is
  listed neither in the help nor by `tt modules list`. A module named `modules`
  no longer stops tt from running.
- The root help and the shell completion describe the command of an external
  module with the module's one-line help, as EXTERNAL COMMANDS does: a command
  a module adds had no description, and a command a module replaces kept the
  description of tt's own. tt takes only the first line of a module's `help`,
  in these lists and in `tt modules list`: a `help` of several lines broke the
  lists, and the module's own `--help` is where a longer text goes.
- An external module named like a group of commands, such as `cluster`, takes
  over the whole group: every `tt cluster ...` command line runs the module
  with the arguments that follow `cluster`. The subcommands of the group used
  to stay tt's own.
- tt reads the files of an external module through its integrity checks: the
  manifest, and the executable every time it runs it - for a command line, as
  before, and also for its `--help` and, for a module without a manifest, for
  its description. A module whose manifest or description the checks refuse is
  ignored with a warning; a run they refuse fails with the error, and a help
  they refuse prints the error instead of the module's help.

### Fixed

- `tt cluster publish` and `tt cluster show --validate` refused a valid cluster
  config of Tarantool 3 whose replicaset sets `leader`, or `bootstrap_leader`,
  with "Additional properties 'leader' do not match the schema": the config of
  each instance, folded from every level, was checked against a schema
  that has no place for keys of the replicaset. They are accepted on the
  replicaset, as Tarantool accepts them, and still refused globally, in a
  group and in an instance, where Tarantool refuses them too.
- `tt connect` and the commands that take a URI like it (`tt play`,
  `tt replicaset`) recognise a tcp address with a query, `host:port?k=v`,
  `tcp://host:port?k=v` and `user:password@host:port?k=v`, and the
  `unix/:path` form of a socket. Before, such a string was read as
  `<APP>:<INSTANCE>` and refused with `"..." cannot be an instance name`,
  quoting the password. `tt connect` takes `transport`, `ssl_key_file`,
  `ssl_cert_file`, `ssl_ca_file`, `ssl_ciphers`, `ssl_password` and
  `ssl_password_file` from the query, as it takes them from the flags of the
  same names, and refuses any other parameter. No error of a target or a URI
  quotes its password any more: those of `tt connect`, `tt play`,
  `tt instance` and the other commands that take targets, `tt replicaset`,
  `tt cluster` and the TCS endpoints of a cluster config hide the credentials
  of what the user typed.
- A path the cluster config of an instance sets with `{{ replicaset_name }}`
  or `{{ group_name }}` in it failed the instance commands with "missing
  vars": tt substitutes them as Tarantool does, as it did
  `{{ instance_name }}`, and in every key it reads, `app.file` and the paths
  it checks at start among them.
- The RSA integrity provider takes a private key in PKCS #8, what `openssl
  genrsa` of OpenSSL 3 writes unless told `-traditional`, as well as in
  PKCS #1, and a public key in PKCS #1 as well as in PKIX. A key of another
  algorithm is refused as not an RSA key.
- Signed configurations in etcd and Tarantool config storage keep the keys tt
  2 and Tarantool Enterprise Edition use: `<prefix>/hashes/sha256/<key>` and
  `<prefix>/sig/<key>` next to `<prefix>/config/<key>`. tt 3 wrote and looked
  for `<prefix>/hashes/sha256/config/<key>` and `<prefix>/sig/rsapss/config/<key>`
  instead, so it could not read a configuration tt 2 had published with
  `--with-integrity-check`, and what it published itself was not where the
  instances look for it.
- Builds with `TT_CLI_BUILD_SSL=openssl` or `openssl_static` reach a Tarantool
  config storage over TLS: they set `go_storage_ssl`, without which
  go-storage refuses TLS to it. The default pure-Go build still has no TLS to
  a config storage.
- `tt binary install`: recognize boolean flags such as `-f` and `--reinstall`
  before the product name when `--no-prompt` is set, instead of mistaking the
  product for a flag value and rejecting its options.
- `tt binary install tarantool --source-build --dynamic`: generate distribution
  artifacts before installation, fixing Tarantool 3.9 builds that failed on
  missing Lua annotation files.
- `tt cluster`: honor `https://` when connecting to etcd and Tarantool Config
  Storage, enabling TLS even without explicit `ssl_*` URL parameters. Preserve
  HTTPS for configured etcd endpoints passed to go-storage.
- `tt instance status`: include replication errors in instance status alerts.
- `tt instance logs --follow`: possible line loss/duplication on rename,
  hanging after a watched log directory is removed, and lines written just as
  the file was read to the end not showing up until the next write.
- `tt instance logs --follow` / `tt tcm log --follow`: a line could be
  delivered twice right after a log rotation, and the follow could stall,
  never picking up the rotated file (go-tail v1.4.15).
- `tt binary install`: change active executable and header links only after
  preparing all installed files, scope directories, and project environment
  scripts. This also applies to existing installations, SDKs, and `--from-build`.
- `tt binary install tarantool-ee`: reject bundles without Tarantool headers
  before replacing an installed version, and refuse to activate an incomplete
  installation. Installing TCM no longer creates unused header directories.
- `tt instance start`, `restart` and `tt run` refuse a `tarantool` without
  execute permission, the one a project selects in `.tarantool/bin/` or the
  one an installed application runs on under `<root>/versions/`, before
  they start anything. Before, `tt instance start` reported the instance
  started, and only its `tt.log` told that the watchdog could not run it.
- `tt package install --scope user|system` lays the version directory out
  with mode 0755, as every other directory of the installation. Before, it
  was 0700, and the instances of an application root installed into the
  system scope could not run under the units of `tt service systemd`, which
  run as `tarantool`: tt failed to read the application manifest.
- `tt package pack` no longer warns that it bundles "a prerelease build" when
  the runtime it bundles is a build of a release tag, such as tarantool
  `3.8.1-0-ge643f88a5e0c`, which the lock records as `3.8.1`. A build past
  its tag, or of an `-rc` or `-beta` tag, is still warned about.
- A second `tt tcm start --watchdog` in a directory where one runs is refused
  before it starts anything. Before, it started a second TCM and stopped it
  again once it found the pid file taken.
- `tt tcm start` without `--watchdog`, refused because `tcm.pid` names a
  running TCM, no longer leaves the TCM it started running.
- `tt instance stop` sent while the watchdog waits to restart a failed
  instance stops the watchdog. Before, the signal was lost: the instance was
  restarted, and `tt instance stop` gave up after 30 seconds.
- Two `tt instance start` of one instance at the same time no longer start two
  watchdogs. Every tt pid file is now owned through a lock, so of any number of
  processes starting at once exactly one takes the file, and a stale file is
  taken over under that lock instead of being removed and created again.
- A second `tt instance start` of a running instance no longer removes the pid
  file and the sockets of the watchdog that runs it.
- `tt instance kill` no longer removes the pid file and the sockets of a
  watchdog that started after the one it killed: it removes them only while
  the pid file still names the killed watchdog.
- Signals sent to an instance while its watchdog stops it are handled at once
  instead of after the stop, which could take 30 seconds.
- Table formatter: fix a potential panic when the scalar encoder receives a
  `float32` value. Format it with 32-bit precision while preserving the existing
  `float64` formatting.
- `tt package`: dependency resolution prefers released versions over `scm` and
  `dev`. LuaRocks orders a branch above every number, so an ordinary constraint
  like `>=3.0.0` used to select the development branch and pin the lock to code
  that changes underneath it. A branch is chosen only when the constraint names
  one (`==scm`) or when the rock has no released version at all, and that case
  is now reported.
- `tt package build`: a stale lock is re-resolved holding every version it
  already recorded, so a build no longer pulls newer releases from the
  registry behind the user's back. `tt package update` remains the way to move
  dependencies forward. `--locked` on a stale lock now names `tt package
  resolve` in the error instead of only reporting the staleness.
- `tt package`: a dependency declared as `*` — which is what `tt package add`
  writes when no version is given — now resolves to whatever the registry
  serves instead of failing with `cannot parse constraint "*"`. A constraint
  the resolver cannot act on is also refused before the manifest is edited, so
  a rejected `tt package add` no longer leaves a declaration behind with the
  lock out of step.
- `tt`: return non-zero exit code on unknown command.
- `tt run`: report `tarantool executable is not found` instead of a raw
  `open "": no such file or directory` error when Tarantool is missing.
- `tt help <module>` prints an external module's help to stdout, where
  `tt <module> --help` and tt's own help print; it went to stderr.
- Shell completion of the first word after `tt` no longer offers
  `tt <module>` next to the name of every external module, which showed up
  at `tt <TAB>` and after a `t`, and bash and zsh no longer fall back to file
  names there when no command matches: only commands are offered.
- `tt instance clean`: show the files it is going to remove, by instance and
  directory, before asking for confirmation, and every one of them with `-V`
  (`--verbose`). With nothing to remove it asks nothing, and with `--yes` it
  shows nothing.

## [2.11.5] - 2026-03-19

The release includes fixes identified by CVE linters.

## [2.11.4] - 2026-03-04

The auxilary release just to keep version matching with `tt-ee`
(no functional changes in `tt`).

## [2.11.3] - 2026-02-25

The release updates the dependencies and fixes discovering of the available
versions with `tt search`.

### Fixed

- `tt search`: fix unknown dev tags parsing.

## [2.11.2] - 2026-02-19

The release includes minor fixes identified by Svacer and CVE linters.

## [2.11.1] - 2026-01-19

The release allows `--cartridge-compat` flag for the `tt pack deb|rpm` commands.
Also it includes several fixes for the `tt install` and `tt pack` commands and
docker container used by these commands is upgraded to Ubuntu 20.04.

### Added

- `pack`: if the `--cartridge-compat` flag is set `pack deb` and `pack rpm`
  commands add files with application and tarantool versions to the package.

### Changed

- Container that is used to build tarantool when `--use-docker` is specified with
  `tt install` or `tt pack` command is upgraded to Ubuntu 20.04.

### Fixed

- `tt coredump`: adjust Tarantool GDB-extention to avoid load failure if `main_cord`
  symbol is `optimized out` in gdb session.
- `tt pack`: corrupts relative symbolic links.
- `tt install`: fails to install tarantool by commit hash.
- `tt install`: fails to install tarantool 3.6.0 using docker (`--use-docker`).
- `tt` can be built without linking to OpenSSL.

## [2.11.0] - 2025-09-10

The release supports Tarantool Config Storage in `tt cluster failover` commands and
introduces templates to create Tarantool Config Storage and non-vshard cluster.

### Added

- `tt create`: add template for Tarantool Config Storage.
- `tt create`: add template for non-vshard Cluster.
- `tt cluster failover`: support Tarantool Config Storage.

### Changed

- `tt cluster failover switch`: minor change in output that displays corresponding
  `switch-status` command with quoted URI argument so it could be copy-pasted for
  subsequent launch as-is.
- `tt rs vshard bootstrap`: make more informative error message when sharding roles
  are not configured (for example when launched against non-vshard cluster).

## [2.10.1] - 2025-06-26

The release introduces a fix for the `tt logrotate` command, which now properly
releases the descriptor of the old log file.

### Fixed

- `tt logrotate`: properly release descriptor of the old log file.

## [2.10.0] - 2025-06-09

The release introduces command support for working with Tarantool
Cluster Manager (TCM). Also added support for `fish` shell autocompletion.
In addition, code verification with `pre-commit` hooks has been configured.

### Added

- In commands `tt cat|play <DIR>` added options `-r`/`--recursive` to
  allow find WAL files inside nested subdirectories.
- `tt search tcm` - the command performs a search for tarantool cluster
  manager (TCM) in the customer zone or local `distfiles` directory.
- `tt install tcm` - the command performs an install for tarantool cluster
  manager (TCM) from the customer zone or local `distfiles` directory.
- `tt uninstall tcm [version]` - the command removes installed tarantool
  cluster manager from the `bin` directory.
- `tt tcm status`: added command to check TCM runtime status
  (modes: `watchdog` or `interactive`).
- `tt tcm stop`: add command for graceful termination of TCM processes
  (modes: `watchdog` or `interactive`).
- Add support manage installed `tcm` versions via `tt binaries` CLI.
- Added support for completion with shell `fish` see
  the command `tt completion fish`.
- Repository use `pre-commit` hooks to check code style.
- Added support for showing TCM logs with `tt tcm log` command.

### Changed

- `tt pack`: packs TCM config, if any.

### Fixed

- Fixed a crash in `tt aeon connect` when processing responses
  from certain SQL commands.
- `tt cat|play <DIR>` with directories handles only `.snap` or `.xlog` files.
- `rs vshard bootstrap`: ignore an error and retry within `timeout` flag
  period.
- `pack` with modules include only under root `tt` environment directory.
  Modules outside of the directory with `tt.yaml` will be ignored.
- `tt connect|replicaset|cluster|aeon|play`: fixed using of IPv6 in instance URI.
- `play`: extend error message if space to play is unavailable or user
  does not have permission to work with it, because the `net.box` module does not
  have means to distinguish between these errors.

## [2.9.1] - 2025-04-15

The release includes minor fixes identified by Svacer and CVE linters.

## [2.9.0] - 2025-04-10

The release introduces advanced features to connect to the Aeon database
and start tcm commands. Further improvements on packaging the customized
application. Major changes on working with external modules.

### Added

- `tt aeon connect` added tests for connect file/app.
- `tt modules list` added command to show available modules.
  If support extra flags:
  * `--version` - to show information about version.
  * `--path` - to show module executables.

- `tt aeon connect` added tests for connect file/app.
- `tt aeon connect`: add connection from the etcd/tcs config.
- `tt pack`: support `.packignore` file to specify files that should
  not be included in package (works the same as `.gitignore`).
- `tt tcm start`: add the tcm command.
- `tt tcm start` OR `tt tcm start --path /path/to/tcm`: added the capability
  to run TCM in interactive mode.
- `tt tcm start --watchdog`: implemented Watchdog mode for automatic
  restarting of TCM upon unexpected termination.

### Changed

- The following functions were moved from `cluster/cmd` to `lib/cluster`:
  * CreateCollector           → lib/cluster/cluster.go,
  * ConnectEtcdUriOpts        → lib/cluster/etcd.go,
  * DoOnStorage               → lib/cluster/etcd.go,
  * MakeEtcdOptsFromUriOpts   → lib/cluster/etcd.go,
  * MakeConnectOptsFromUriOpts → lib/cluster/tarantool.go,
  * ConnectTarantool          → lib/cluster/tarantool.go.
- Added new submodule `lib/connect`.

### Fixed

- `tt pack`: added TCM packing when executing the tt pack command, except
  for the flag without-binaries in this case TCM will not be in the archive.

- Arguments of an internal command are not parsed if it is forced over its
  existent external counterpart.
- aeon: fix SSL paths configuration for aeon connection.
- `tt pack rpm`: failed to pack if only one of `--preinst`/`--postinst`
  options is specified.

## [2.8.1] - 2025-03-10

The release introduces minor changes in stabilization of `tt connect`
command. Expanded possibility to connect to `aeon` base.
Improvement in the work of templates.

### Added

- `tt aeon connect`: add connection from the `app:instance_name`.
- Added support for the `{{ metricsPort }}` construct in Go text templates.
  This new function allows template users to generate a monitoring port value
  directly within their templates, providing more flexibility and simplifying
  configuration management.

### Changed

- `tt connect`: allow to disconnect with `Ctrl+C` or `Ctrl+\`
  if script execution hung.
- Moved `cluster.cmd.UriOpts` to `connect.UriOpts`.

### Fixed

- `tt connect`: return Lua parse error.
- `tt connect`: panic on render empty table.
- `tt` can be built without linking to OpenSSL.

## [2.8.0] - 2025-02-19

The release introduces an expanded ways to connect to `aeon` DB using:
configuration file and fixing of using root certificates.
TCM binary could be packed with `pack` subcommand.

### Added

- `tt pack`: added TCM file packaging.
- `tt aeon connect`: add connection from the cluster config.

### Fixed

- `tt aeon`: did not use system CAs by default.

## [2.7.0] - 2025-01-22

The release introduces an experimental support of console for AeonDB and
continues to improve `tt play` command.

### Added

- `tt aeon connect`: add support to connect Aeon database.
- `tt play`: support of the SSL parameters by using next flags:
  * `sslkeyfile` - path to a private SSL key file,
  * `sslcertfile` - path to an SSL certificate file,
  * `sslcafile` - path to a trusted certificate authorities (CA) file,
  * `sslciphers` - colon-separated list of SSL cipher suites the connection.
- `tt play`: support connection to a target instance by `application` name
  or `application:instance` name.
- `tt coredump pack`: add options to customize coredump packing:
  * `-e (--executable)`: specify Tarantool executable path.
  * `-p (--pid)`: specify PID of the dumped process.
  * `-t (--time)`: specify time of dump (seconds since the Epoch).
- `tt.yaml`: allows to specify a list of modules directories.
- Environment variable TT_CLI_MODULES_PATH can be used to specify
  an extra path with modules.

### Changed

- `tt stop/kill/clean/logrotate`: no longer need:
  * Instances scripts for multi-instance applications.
  * Cluster config for tarantool3-based cluster applications.
- `tt logrotate`: don't exit at non-running instance, just warn and proceed
  with the other instances, like `tt stop` and `tt kill` do.
- `tt coredump pack`: if `-e` option is omitted first search tarantool
  executable in tt environment then in `PATH` instead of using the hardcoded
  path `/usr/bin/tarantool`.
- `tt replicaset downgrade`: drop option `-v` (`--version`). Pass version as a
  positional argument rather than option.

### Fixed

- `tt coredump inspect`: fails for tarantool-ee coredump archive if the source
  directory is missing.
- `tt pack`: fails if `etcd` or `tcs` are present in the configuration
  and not available.

## [2.6.0] - 2024-11-29

The release introduces `upgrade` and `downgrade` subcommands for
`tt replicaset` and adds minor improves to `tt cat`, `tt play` and
`tt connect`.

### Added

- `tt replicaset downgrade`: command to downgrade the schema on a Tarantool
  cluster.
  * `-v (--version)`: (required) specify schema version to downgrade to.
  * `-r (--replicaset)`: specify the replicaset name(s) to downgrade.
  * `-t (--timeout)`: timeout for waiting the LSN synchronization (in seconds)
    (default 5).
- `tt replicaset upgrade`: command to upgrade the schema on a Tarantool
  cluster.
  * `-r (--replicaset)`: specify the replicaset name(s) to upgrade.
  * `-t (--timeout)`: timeout for waiting the LSN synchronization (in seconds)
    (default 5).
  * supports upgrading the database schema on remote cluster by upgrading
    each replicaset individually using `tt replicaset upgrade <URI>`.
- New flag `--timestamp` of `tt cat` and `tt play` commands is added to specify
  operations ending with the given timestamp. This value can be specified
  as a number or using [RFC3339/RFC3339Nano](https://go.dev/src/time/format.go)
  time format.
- `tt connect`: add new `--evaler` option to support for customizing the way
  user input is processed.
- `tt cat/play`: allows to specify a list of directories to search WAL files.

### Fixed

- `tt rocks`: don't load local configs.

## [2.5.2] - 2024-11-07

The release updates luarocks and libraries version.

### Fixed

- `tt rocks`: a wrong Lua interpreter is selected.

## [2.5.1] - 2024-10-31

The release updates the MessagePack library in a release build.

### Fixed

- Release packages were built using the outdated and buggy MessagePack
  library.

## [2.5.0] - 2024-10-15

The release introduces a set of subcommands for `replicaset roles`, improves
clarity regarding the version number to install, enhances the status display,
and adds a flag to disable interactivity when the application is stopped.

Additionally, several fixes were implemented to improve stability.

### Added

- `tt status`: display `config`, `box`, and `replication upstream` statuses.
  * `--details`: display detailed reports of errors and warnings from
    instances.
- `tt stop` confirmation prompt. `-y` option is added to accept stop without
  prompting.
- `tt cluster replicaset roles add`: command to add roles in config scope
  provided by flags.
- `tt cluster replicaset roles remove`: command to remove roles from config
  scope provided by flags.
- `tt replicaset roles add`: command to add roles in the tarantool replicaset
  with cluster config (3.0) or cartridge orchestrator.
- `tt replicaset roles remove`: command to remove roles in the tarantool
  replicaset with cluster config (3.0) or cartridge orchestrator.
- `tt install tt|tarantool <version>` - allow <version> be incomplete. So `2.3`
  will install the the last available release with specified
  <major=2>.<minor=3> in <version>.

### Fixed

- `tt console` command `\set delimiter [marker]` don't hang `tt`.
- `tt log -f` crash on removing log directory.
- `tt connect` crash due to an empty response.
- `tt start` error on start Tarantool 3 with encrypted etcd.
- `tt replicaset vshard bootstrap` unable to bootstrap large clusters due to
  a timeout.
- `tt replicaset vshard bootstrap` timeout was 3s instead of 10s.

## [2.4.0] - 2024-08-07

### Added

- `tt log`: a module for viewing instances logs. Supported options:
  * `--lines` number of lines to print.
  * `--follow` print appended data as log files grow.
- `tt connect`: support format for Tarantool tuples for Tarantool
  versions >= 3.2.
- `tt enable`: create a symbolic link in 'instances_enabled' directory to
  a script or an application directory.
- `tt replicaset bootstrap`: command to bootstrap a Cartridge cluster or an
  instance.
- `tt rs rebootstrap`: re-bootstraps an instance.
- `-s (--self)` flag to execute `tt` itself and don't search for other `tt`s
  in bin_dir provided in config.
- `tt start` interactive mode with `-i` option.

### Fixed

- Sorted by name order of columns for `table` and `ttable` formats.
- `tt switch tt`: does not work with `x.x.x` version format.
- `tt install tt` returns expected exit status code on unsuccessful dependency
  check.
- `tt pack`: failed to start instances using systemctl due to
  `permissions denied`.
- `tt uninstall tt`: does not work with `x.y.z` version format.
- Ability to update a latest version of `master` tarantool and tt with
  `tt install`.

### Changed

- Do not create Dockerfile.* in application's directory.

## [2.3.1] - 2024-06-13

### Added

- Building Linux AArch64 `tt` packages.

## [2.3.0] - 2024-06-04

### Changed

- `tt status`: displays the mode of the instance.
- `tt coredump`: enhances coredump inspection:
  * `tt coredump pack`: puts gdb.sh and GDB-extensions into the archive so
    that it contains everything necessary for convenient coredump inspection.
  * `tt coredump inspect`: allows archive path as an argument (archive should
    be created with `tt coredump pack`).
  * `tt coredump inspect`: added `-s` option to specify the location of
    tarantool sources.
- `tt cluster publish`: ability to publish a new instance config.
- `tt pack` does not create unnecessary directories and removes files that are
  required only for building from the resulting package.

### Added

- `tt cluster failover`: added supervised failover management commands.
- `tt status`: added `pretty` option for pretty-formatted table output.
- `TT_CLI_CFG`: environment variable to specify the path to the configuration
  file.
- `tt pack`: systemd unit parameterize support.
- `tt replicaset vshard`: module to manage vshard in the tarantool replicaset.
  * `tt replicaset vshard bootstrap`: command to bootstrap vshard.

### Fixed

- `tt clean` no longer tries to clean files multiple times.
- Tarantool 3 config instance fails to use 108 symbols control socket on
  `tt start`.
- Incorrect application name in case of explicit providing config path without
  directories.
- Application build failure during pack with Tarantool from the current
  environment.

## [2.2.1] - 2024-04-03

### Added

- `tt create`: added single instance application template.
- `tt replicaset promote`: command to promote an instance in the tarantool
  replicaset with cluster config (3.0) or cartridge orchestrator.
- `tt replicaset demote`: command to demote an instance in the tarantool
  replicaset with cluster config (3.0) orchestrator.
- `tt cluster replicaset`: module to manage replicaset via 3.0 cluster config
  storage.
  * `tt cluster replicaset promote`: command to promote an instance in
    the replicaset.
  * `tt cluster replicaset demote`: command to demote an instance in
    the replicaset.
- `tt connect --binary`: connect to instance using binary port.
- `tt kill`: command to stop instance(s) with SIGQUIT and SIGKILL signals.

### Changed

- `tt start` now creates binary port.

## [2.2.0] - 2024-03-06

### Changed

- `tt pack` generates a separate systemd unit for each packed application.
  Common (all instances) unit is removed.
- `tt pack` default data, run, log files location is changed for rpm/deb
  packages to `/var/[log | run | lib]/tarantool/<env_name>`
- create `/var/[log | run | lib]/tarantool/<env_name>` on target system
  for packed applications.

### Fixed

- `tt start`: not working on FreeBSD.
- `tt pack` and `tt build` fail in verbose mode with "invalid argument"
  error.
- `tt pack` packs applications, which are not valid: instances file is
  empty, for example.
- `tt pack` with `--use-docker` fails due to incompatible versions of `tt`
  between local system and docker container. Install current `tt` version
  in docker image if possible.
- `tt binaries list` invalid argument error if `tarantool` is not a symlink.
- if a user provides pre or post install script to `tt pack rpm`, it
  uses file name as a script instead of its content.

## [2.1.2] - 2024-02-02

### Added

- Built-in vshard cluster application template.
- Building tt in Linux-aarch64, FreeBSD environments.
- `tt binaries switch`: switch to installed binary.
- `tt download`: download Tarantool SDK.

### Changed

- `tt replicaset`: prefer cartridge instances without critical issues on
  it during discovery.
- `tt binaries` renamed to `tt binaries list`

### Fixed

- `tt rocks`: not working on macOs.
- `tt install tarantool` fails due to checkout error.
- `tt binaries list`: not showing `active` tag for `master` version.
- missing 3.0 SDK in search results for `tarantool-ee`.

## [2.1.1] - 2024-01-15

### Added

- Module `tt replicaset`, to manage replicasets:
  * `tt replicaset status` to show a cluster status information.

### Changed

- Disable `tt run` tarantool flag parsing.

### Fixed

- `tt start`: not working global `tt` flags.

## [2.1.0] - 2023-12-15

### Changed

- Make cartridge app dependencies less strict.
- `tt connect` auto-completion shows directories and files when there are no
  running apps.
- `tt rocks --server` now accepts several URL's.
- Disable `tt run` tarantool flag parsing.
- Now `tt run` starts instance without our wrapper.

### Added

- `tt env`: add current environment binaries location to the PATH variable.
- `tt cluster`: add an ability to specify a key for `show`/`publish` via URI.
- `tt cluster`: add an ability to publish/show configuration from tarantool
  config storage.

## [2.0.0] - 2023-11-13

### Changed

- Print log messages to stderr.
- Global flags are required to be positioned only before child
  commands. Example: `tt --cfg tt.yaml install tt`.
- tt config format: separate tt environment options from application options.
- tt version: additional version information for non-release builds.
- Working directory is changed to an application source directory.
  If the application is a script, new working directory will be created
  in instances enabled location.
- Re-worked application runtime artifacts layout: `app` section relative
  paths are considered relative to working directory, which is an application
  source directory. Application name sub-directory no longer used for relative
  paths. Default names are changed for PID-files, control sockets and log
  files.
- Enable logging to file by default for `tarantool` cluster instances.
  Default log file name for an instance is `tarantool.log`. `tarantool`'s
  stdout/stderr and `tt` logs go to `tt.log` file.
- Remove URI with credentials from console title and prompt.
- Ignore app-instance delimiters for Tarantool 3.0 instances.
- Don't use dash as an app-instance delimiter. At the same time,
  `cartridge_app-stateboard` treated as a special case.
- Log rotation functionality and configuration is removed from `tt`.
  `tt logrotate` command re-opens a log file and sends SIGHUP to the child
  `tarantool` processes.
- `tt cat`: all diagnostic messages are printed to stderr.
- Print `tarantool` stdout/stderr and watchdog logs to the same log file -
  `tt.log`.

### Added

- tt completion: added luarocks completions.
- tarantool-ee: search and install development builds.
- `tt play`: ability to pass username and password via flags and environment
  variables.
- tt cluster: credentials could be passed via environment variables and command
  flags.

### Fixed

- `tt rocks`: broken `--verbose` option.
- `tt binaries`: tarantool-ee binaries not shown.
- `tt cluster`: recognize app:instance as a etcd URL.

## [1.3.0] - 2023-09-28

### Changed

- Use CLI arg connect string for the prompt line and the title to avoid too
  long prompt line when using 'app:instance' target format.
- `tt rocks`: luarocks version has been updated to 3.9.2.

### Added

- `tt install tarantool/tt`: ability to install tarantool and tt from an
  arbitrary commit. The binary has the name tt/tarantool_ + seven-digit hash.
- New `tt pack` flag `--tarantool-version` is added to specify tarantool
  version for pack in docker. It is supported only with `--use-docker` enabled.
- Module `tt cluster`, to show or publish a cluster or an instance
  configuration.
- `tt connect`: added command `\help` to show the help with a list of available
  commands.
- `tt connect`: added command `\quit` to quit from the console.
- `tt connect`: expanded formatting modes for the interactive console.
- `tt rocks`: added `admin` commands tree.
  `tt rocks admin` implements `luarocks-admin` commands tree.

### Fixed

- Installation failure from a commit hash.
- Crash on `tt install <tool> master`.
- `--with-binaries` flag for `tt pack` not working while packing
  with `--cartridge-compat`.
- `tarantool` binary after `pack` now always named `"tarantool"`.

## [1.2.0] - 2023-08-18

### Changed

- `tt pack` now skips all `.git` files in packed environment, not only in main
  directory.
- `tt connect`: the reverse search function to work consistently with
  tarantool.

### Added

- `tt install tarantool-dev`: ability to install tarantool from the local build
  directory.
- `tt uninstall`: smart auto-completion. It shows installed versions of
  programs.
- `tt uninstall`: when removing symlinks and an existing installed version, the
  symlink will be switched to the latest installed version, so that `tt` can
  continue working with the program.
- `tt connect`: support for multi-line commands in the history.
- New `tt pack` flag `--cartridge-compat` is added to maintain backward
  compatibility with the cartridge-cli. It is supported only by `tgz` type
  packing.
- `tt pack`: added option `--without-modules` allowing not to take external
  modules into the pack bundle.
- `tt connect`: added command `\shortcuts` listing all available
  shortcuts and hotkeys in go-prompt.
- initial support for Tarantool 3.0 instances running using cluster config.

### Fixed

- `tt install tarantool`: symlink to the directory with tarantool headers
  is now updated when installing an existing version.
- `tt connect`: terminal failure after throwing an error.

## [1.1.2] - 2023-06-16

### Changed

- Set `compat.fiber_slice_default` to `new` by default in cartridge application
  template.
- Treat the directory containing the instances file (instances.y[a]ml) as
  an application.

### Added

- `tt connect`: support for the `../` and `~/` at the beginning of the URI,
  when using unix sockets.

### Fixed

- Panic in tarantool 1.10.15 static build by `tt`.
- Removed `--use-docker` flag from `tt uninstall tarantool` since it was added
  by mistake.

## [1.1.1] - 2023-06-08

### Changed

- `tt build` now hides building output if `-V` is not provided.

### Fixed

- `tt start` now does not start an instance if it is already running.
- `tt rocks` uses rocks repo path relative to tt environment config location.
- `tt connect` now does not crash on `\q` input.

### Added

- smart auto-completion for `tt start`, `tt stop`, `tt restart`, `tt connect`,
  `tt build`, `tt clean`, `tt logrotate`, `tt status`. It shows suitable apps,
  in case of the pattern doesn't contain delimiter `:`, and suitable instances
  otherwise.
- support tt environment directories overriding using environment variables:
  * TT_CLI_REPO_ROCKS environment variable value is used as rocks repository
    path if it is set and there is no tt.repo.rocks in tt configuration file or
    tt.repo.rocks directory does not include repository manifest file.
  * TT_CLI_TARANTOOL_PREFIX environment variable value is used for as tarantool
    installation prefix directory for rocks commands if it is set and tarantool
    executable is found in PATH.
- smart auto-completion for `tt create`. It shows a list of built-in templates
  and templates from the config.
- `tt connect`: support for the timestamp format in the history file.

## [1.1.0] - 2023-05-02

### Changed

- `tt install tarantool` without version specification now installs the
  latest release.
- `tt install/search tarantool-ee` now uses credentials from `tarantool.io`
  customer zone. Also, installation now requires specifying the version.
- `tt search tarantool-ee` options changed. A new `--version` flag has been
  added to allow search for a specific release. The `--dev` and `--dbg` options
  have been merged into a single `--debug` option.
- `tt search` now uses subcommands for searching tarantool/tarantool-ee/tt
  binaries

### Added

- `--dynamic` option for `tt install tarantool` command to build non-static
  tarantool executable.

### Fixed

- `tt connect` command does not break a console after executing `os.exit()`
  command anymore.

## [1.0.2] - 2023-04-21

### Fixed

- `tt cartridge` command takes into account run dir path from the `tt`
  environment. So most of the `tt cartridge` sub-commands are able to work
  without specifying `--run-dir` option.
- `tt install` command checks it's write rights to binary and include
  directories before installing binaries.

### Changed

- `tt install/uninstall` command line interface is updated. Program names
  have become sub-commands with their own options.

## [1.0.1] - 2023-04-04

### Added

- A configurable variable `cluster_cookie` for `tt create cartridge` template.
- `tt build` accepts application name for building.
- Creating wal, vinyl and memtx directories for `tt pack`. If these directories
  are not located in the same directory in the environment for packing, the
  result package will contain separate snap/vinyl/wal directories for
  corresponding artifacts.

### Fixed

- Packing symlinks into RPM package.

### Changed

- `tt uninstall` does not ask version if only one version of a program is
  installed.
- `tt rocks init` is disabled.

## [1.0.0] - 2023-03-23

### Added

- Follow-up message support for application templates.
- tarantool_layout config option to enable compatibility mode with tarantoolctl
  artifacts layout. If this option is set to `true`, `tt` will not create
  sub-directories for runtime artifacts such as control socket, pid file and
  log file. This option affects only single instance applications.
- An ability to set different directories for WAL, vinyl and snapshots
  artifacts.
- `tt instances` command to print a list of enabled applications.
- SSL options for `tt connect` command.
- An ability to pass arguments to a connect command.
- `tt binaries` command. It shows a list of installed binaries and their
  versions.

### Changed

- tt config is renamed to tt.yaml.
- Do not use `make` command options for `tarantool` build if `MAKEFLAGS`
  environment variable is set.
- `binaries`, `build`, `check`, `clean`, `create`, `install`, `instances`,
  `logrotate`, `pack`, `restart`, `run`, `start`, `status`, `stop`, `uninstall`
  require environment configuration file.

### Fixed

- Output of the `help` with all commands.
- Allow more characters for URI credentials.

## [0.4.0] - 2022-12-31

### Added

- Support of rocks repository specified in tt config.
- `cfg dump` module. It prints tt environment configuration.
- `--use-docker` option for `tt pack` for packing environments in docker
  container.
- Support of MacOS.

### Changed

- Updated cartridge-cli version to 2.12.4.

## [0.3.0] - 2022-12-05

### Added

- `tt restart` confirmation prompt. `-y` option is added to accept restart
  without prompting.
- `tt pack` will generate systemd unit for rpm and deb packages.
- `--use-docker` option for `tt install` to build Tarantool in
  Ubuntu 16.04 container.
- Ability to use the `start/stop/restart/status/check` commands without
  arguments to interact with all instances of the environment simultaneously.
- Starting from version 2.8.1, we can specify configuration parameters of
  tarantool via special environment variables.
  Added support for this feature when running older versions of tarantools via
  tt.

### Changed

- `tt cartridge` sub-commands `create`, `build`, `pack` are removed.
- `remove` command is renamed to `uninstall`.
- Updated values in system `tarantool.yaml` for `bin_dir`, `inc_dir`
  and `repo: distfiles`.

### Fixed

- Working of the `help` module with multi-level commands (commands with
  several subcommands).
- Using the system `tarantool.yaml` when installing from the repository.

## [0.2.1] - 2022-11-24

### Fixed

- Fixed building for MacOS.
- A unified error writing style has been introduced.

## [0.2.0] - 2022-11-21

### Added

- Module `tt init`, to create tt environment configuration file.
- Module `tt daemon`, to manage the `tt` daemon.
- Built-in application templates support. Cartridge application template is
  added.
- Using `default_cfg` from `.tarantoolctl` for `tarantool.yaml`
  generation in `tt init`.

### Changed

- Modules `tt start`, `tt connect` and `tt cartridge` now use relative
  paths for unix sockets. It allows to use socket paths longer than sun_path
  limit.(108/106 on linux/macOS) e.g foo/bar.sock -> ./bar.sock

## [0.1.0] - 2022-10-12

### Added

- Module `tt version`, to get information about the version of the CLI.
- Module `tt completion`, to generate autocompletion for a specified shell.
- Module `tt help`, to get information about the CLI and its modules.
- Module `tt start`, responsible for launching the instance according to the
  application file.
- Module `tt stop`, responsible for terminating the instance.
- Module `tt status`, to get information about the state of the instance.
- Module `tt restart`, responsible for restarting of the instance.
- Module `tt logrotate`, to rotate instance logs.
- Module `tt check`, to check an application file for syntax errors.
- Module `tt connect`, used to connect to a running instance.
- Module `tt rocks`, LuaRocks package manager.
- Module `tt cat`, to print into stdout the contents of .snap/.xlog files.
- Module `tt play`, to play the contents of .snap/.xlog files to another
  Tarantool instance.
- Module `tt coredump`, to pack/unpack/inspect tarantool coredump.
- Module `tt run`, to start tarantool instance using tt wrapper.
- Module `tt search`, to show available tt/tarantool versions.
- Module `tt create`, to create an application from a template.
- Module `tt build`, to build an application.
- Module `tt install`, to install tarantool/tt.
- Module `tt remove`, to remove tarantool/tt.
