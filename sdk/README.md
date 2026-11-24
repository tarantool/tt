# tt SDK

The SDK is the part of tt that a compiled-in tt module may import. A module
must not import anything else from this repository.

## Layout

Each directory is a separate Go module:

| Directory        | Module path                              |
|------------------|------------------------------------------|
| `sdk`            | `github.com/tarantool/tt/sdk`            |
| `sdk/connect`    | `github.com/tarantool/tt/sdk/connect`    |
| `sdk/dial`       | `github.com/tarantool/tt/sdk/dial`       |
| `sdk/cluster`    | `github.com/tarantool/tt/sdk/cluster`    |
| `sdk/integrity`  | `github.com/tarantool/tt/sdk/integrity`  |
| `sdk/backup`     | `github.com/tarantool/tt/sdk/backup`     |
| `sdk/tuple`      | `github.com/tarantool/tt/sdk/tuple`      |
| `sdk/fetch`      | `github.com/tarantool/tt/sdk/fetch`      |
| `sdk/build`      | `github.com/tarantool/tt/sdk/build`      |

- `connect` parses connection strings and URIs and resolves credentials.
- `dial` builds Tarantool dialers for the plain and SSL transports.
- `cluster` collects and publishes cluster configuration from files, etcd and
  Tarantool config storage.
- `integrity` defines the integrity-checking interface. Its disabled
  provider, the one tt uses unless its distribution chooses another with
  `core.WithIntegrity`, checks nothing; its RSA provider signs the tree of a
  package, the runtimes it bundles and published data with an RSA private
  key and checks them with the public key, an application together with the
  runtimes it runs on.
- `backup` holds what tt backup and tt restore share: `archive` packs snap
  and xlog files with a manifest fragment into a `.tar.zst` archive and
  unpacks it, and `storage` is the object storage of backups, with a
  filesystem (`storage/fs`) and an S3 (`storage/s3`) backend.
- `tuple` decodes the MessagePack values Tarantool stores - tuples, keys,
  update operations, the bodies of journal rows - keeping MP_STR apart from
  MP_BIN, MP_UINT from MP_INT and the order of a map, with the extensions
  as the types of go-tarantool, and renders them, or what go-tarantool
  decodes, as text: `Text` for a scalar, `AppendJSON`, `YAMLNode` and
  `AppendLua`, a Lua expression that builds the value in Tarantool. It
  writes them with no loss as well: `AppendTaggedJSON` and `ParseTaggedJSON`
  for JSON that tags the values JSON has no type for, `AppendField` and
  `ParseField` for the text of a field of a space by its type, as a cell of
  CSV holds it. `tt data cat` prints journal rows with it; the export and
  import of tt-ee can read and write their data with it.
- `fetch` downloads files over HTTP, with the standard library only: a
  `Client` with a timeout and a bound on the size of a body, whose replies
  other than 200 OK are an `HTTPError`; a `Fetcher` for a file of a known
  SHA-256, which checks it and keeps it in a cache directory named by its
  SHA-256, trusting a cached copy only when its own SHA-256 matches; and
  `UnpackFile`, which takes one file out of a `.tar.gz` release archive in
  memory. `tt binary install` downloads the prebuilt Tarantool and tt
  through it, `tt wasm` and `tt emmylua` their release assets.
- `build` is the contract of a build backend, with the standard library
  only: a `Backend` - its name, the types of what its builds make
  (`Produces`: modules, data files, programs - a closed set, the places of
  a rock, which a backend picks from and does not add to), the
  configuration its fields of a build block decode into and the `New` that
  checks them and returns the `Build` (`Typed` makes it of a function of
  the configuration's own type), and whether lifecycle hooks may name
  it - the `Context` a build or a hook runs in, with the directories, the
  target, the Tarantool it is built for and the commands of the build, and
  no fact of one language or of the deprecated `kind`, which the core keeps
  to itself, telling a backend what a component `Wants`, and a `FileCheck` of the files of
  a package by their suffix - those the components lay out and the data
  files their builds make, whatever backend made them; a warning of it is
  shown and an error fails the build. The build backends of tt are written
  against it
  alone: they are the standard module `build` (`modules/build`), and the
  core has no backend of its own. `buildtest` checks a build block for a
  backend and runs its build on temporary directories, as the core does. A
  module provides backends and file checks through the `Module` its
  `ModuleConstructor` returns.

## Contract module

`github.com/tarantool/tt/sdk`, rooted at `sdk/` itself, is the contract
between tt and its modules. The nested `connect`, `dial`, `cluster`,
`integrity`, `backup`, `tuple`, `fetch` and `build` are not part of it:
each has its own `go.mod`. It depends on the standard library,
`github.com/spf13/cobra`, `github.com/spf13/pflag`, the core package of
`github.com/tarantool/go-config/v2` (not its Tarantool builder),
`github.com/tarantool/tt/sdk/connect` and `github.com/tarantool/tt/sdk/build`,
whose types appear in it (testify for its tests);
`sdk/formatter` adds `github.com/jedib0t/go-pretty`, `go.yaml.in/yaml/v3` and
`go.yaml.in/yaml/v2`,
`sdk/console` adds `github.com/tarantool/go-prompt` and `golang.org/x/term`.
Cobra, pflag and go-config types are part of the contract, so the module
pins their versions: a module builds against the cobra and the go-config the
core uses, and the core never requires older ones.

- `sdk` (the root package) defines the module contract. A module is a
  `Constructor`: a function that takes the core's `Services` and returns
  `Mount`s - a fresh cobra command and the path of the group it goes under,
  groups nobody provides being created. A module that provides build
  backends or file checks as well is a `ModuleConstructor`, which returns
  a `Module` of its `Commands`, `Backends` and `FileChecks`; the core takes
  either kind of constructor. `Services`, and the `Project` it
  returns, are implemented by the core and by `sdk/sdktest` only, so methods
  may be added to them: `Log` (the module's logger, valid at any time),
  and, once tt is configured, `Tarantool` (the `Tarantool` to run, with its
  `Path` and structured `TarantoolVersion`), `Integrity` (`Open` a file
  through the integrity checks), `Project` (the directory of the command,
  `Dir`, and the project it is in, `Root`, as the project commands of tt
  find it, which the other methods work on: `Manifest`, what its manifest
  declares - the package, the dependencies with their version, registry
  and path, the components with their path, include, exclude, namespace
  and build block, its common fields and the raw fields of its backend,
  which `ManifestBuild.Decode` decodes as the core does - parsed and not
  checked; `AddDependency`, which declares a `Dependency` in the manifest -
  in the long form with a `Registry`, in `[dev_dependencies]` with `Dev` -
  setting the constraint and the registry of a declaration there, and
  re-resolves its lock as `tt package add` does, reporting in a
  `DependencyChange` whether the name was declared already, with what, and
  how the locked versions moved; `RemoveDependency`, as `tt package
  remove`; `AddComponent`, which appends a `Component` - a comment, its path
  and include and a `ComponentBuild` of fields in their order, tables
  included - to the manifest, every other line kept, and checks it with the
  backends of the tt; `UpdateComponent`, which changes the fields of a
  component where they are written, and `RemoveComponent` - the three
  resolve nothing and need no Tarantool; `Resolve`, which resolves the lock
  as `tt package resolve` does, and without a Tarantool as a build does;
  `ReadLock`, what the lock pins and whether it is fresh; `Build`, which
  builds the package or the components of `BuildOptions` with the code of
  `tt package build`, arguments passed through to the build of one, and
  reports what each made and whether it was up to date; `BuildContext`,
  the `build.Context` a build of a component runs in, and its build, with
  nothing built; `Install`, which fills `.rocks/` from the lock as `tt
  package fetch` does; those that resolve take the rock servers
  `--registry` would give, whose help is `RegistryFlagUsage`, and report
  the moves of the lock, each a `DependencyMove` that prints as tt package
  prints one; `ScopeDir`, its `.tarantool`, the project scope; and
  `PrepareEnvironment`, which writes
  `.env/activate.sh` and `deactivate.sh` as the first `tt binary install`
  into the project does), `Rocks` (`Index` makes a directory a rock server:
  the LuaRocks manifests and `tt-rocks-index.json`, as `tt package index`
  writes them, or with `IfIndexed` the index only where there is one),
  `Confirm` (a yes/no prompt on stderr that answers the given fallback
  under `--no-prompt`), `Streams` (the standard streams and a `Printer` with
  the core's formats), `ClusterConfig` (the cluster configuration of a
  `ClusterSource` as Tarantool 3 sees it, an immutable go-config snapshot,
  with the directory of the file it was read from), `Connect` (a `Conn` to
  an instance, as `tt connect` reaches it without `--binary`: an
  `app:inst` target at its console socket, as the admin, a URI over
  iproto, with the credentials of the URI, of `ConnectOptions` or of
  `TT_CLI_USERNAME` and `TT_CLI_PASSWORD`; `AddConnectFlags` declares the
  flags of `tt connect` bound to the options. `Conn.Eval` runs a Lua chunk
  with arguments and returns every value it returns, nils included, as the
  same Go values over either transport - integers as `int64`, strings
  byte for byte, tables as `[]any` or `map[any]any` - and a Lua error as
  an error that wraps `ErrEval` and reads as its message alone; the
  deadline of its context bounds the wait for the answer), `LocalInstances`
  (the instances of this host the targets select, as `tt instance status`
  selects them with `--scope` and `--template`, every instance of the
  project for none, each a `LocalInstance` with its name, the working
  directory of its process, where a core of a relative
  `kernel.core_pattern` goes, its run directory, `tt.log`, the file
  Tarantool logs into of its own, if any, and the crashes its watchdogs
  recorded, the last ten exits by a signal with the PID, the signal and
  whether a core was dumped, running or not, or in `CrashesErr` why they
  cannot be read, as those of another user; nothing connects to them)
  and `Exit` (reports an error and ends the process with tt's code for it,
  for code such as a prompt callback that cannot return the error).
  `ErrNotFound` is wrapped by what is not found.
  `CoreVersion` reports the version of the tt core the binary was built
  with, from the build info.
- `sdk` also defines the exit-code contract: the codes tt returns (1 for a
  request the caller must fix, 2 for a failure of the system, 3 for partial
  success), the `ExitError` carrier and `ExitCode`, under which code 1 never
  hides a deeper code. Classifying dial and filesystem errors as 2 is the
  core's job, not the SDK's. `UsageError` (`WithUsage`, `Usagef`) is an
  error in how a command was invoked: tt logs it and prints the command's
  usage. `Reported` marks an error the user has seen already, such as the
  failure a child program printed itself: tt prints nothing for it and
  exits with its code (`IsReported` tells one).
- `sdk` also names cluster configurations. A `ClusterSource` is an
  application of the tt environment (`AppSource`), a cluster config file
  (`FileSource`) or an etcd or Tarantool config storage URI with its
  `Credentials` (`StorageSource`); `ParseClusterSource` classifies a
  command-line argument the way `tt cluster show` does - a storage URI, else
  an existing regular file, else an application. Sources are comparable.
  `AddAppFlags` declares `--scope` and `--template` on a command, bound to an
  `AppLookup`, with the help and the completion of tt's instance commands,
  and `ClusterSource.WithLookup` makes an `AppSource` find its application as
  they say. `SplitInstance` splits `app:instance`. `Instances` lists the
  instances of a cluster configuration (a go-config `Config` with the
  Tarantool hierarchy) and `InstanceConfig` returns one instance's configuration resolved through
  global, group, replicaset and instance; an unknown instance wraps
  `ErrNotFound`.
- `sdk/bundle` is the contract of a source of prebuilt archives, where
  `tt binary install` and `tt binary search` find the builds of a product:
  a `Source` lists the `Artifact`s of a `Query` - a product, a platform as
  `runtime.GOOS` and `runtime.GOARCH` name it, a `Channel` and an optional
  release line - and fetches the bytes of one of them; an artifact carries
  its version, its flavours (`gc64`, `debug`), its name and, when the source
  knows them, its size and SHA-256, which tt checks, and a `Locator` only
  the source reads. A `FlagSource` adds flags of its own, such as the
  credentials it logs in with, to the commands of its products. A source may
  list an archive it cannot confirm without downloading it; `Fetch` of one
  it does not have wraps `ErrNotFound`. The package defines the archives of
  the Community products a source delivers: `tarantool` with the executable
  at the root and the headers in `include/tarantool/`, `tt` with the
  executable at the root. tt installs these from a source for a release tag
  and builds them from source when the source has no build for the
  platform. The sources are the distribution's choice
  (`core.WithBundleSources`); tt has the customer zone of tarantool.io, the
  debs of Tarantool on rws.tarantool.org and the releases of tt on GitHub by
  default.
- `sdk/console` is the interactive console `tt aeon connect` runs: a
  `Console` built by `NewConsole` from `ConsoleOpts` reads statements with
  line editing, completion and history on a terminal (line by line from a
  pipe), hands each to a `Handler` and prints its result in a `Format`
  (`FormatAsTable`, or a result's own `Formatter`). `History` keeps the
  statements in a file, the format tt connect uses. The console ends the
  process through the `Exit` function its options must carry when the
  handler's connection closes, since the prompt library it runs on has no
  caller to return to.
- `sdk/execfile` writes a file a process may run - a program, a script, a
  tool unpacked from an archive - with no fork of the process in between
  (`Write`, `WriteFile`): Linux refuses to run a file another process holds
  open for writing (`ETXTBSY`), and a process forked while the file is open
  holds it until it calls exec. The core and the modules run in one
  process, so they all write such files through it. It depends on the
  standard library only.
- `sdk/formatter` renders the YAML document a Tarantool console answers
  with: `MakeOutput` prints it as YAML, as a Lua expression or as tables
  (`Format`, parsed from its name by `ParseFormat`), and `Opts` sets the
  tables' pseudographics, maximum column width and `TableDialect` (terminal,
  Markdown or Jira). The output is byte for byte what tt's consoles print.
- `sdk/log` is the logging facade over `log/slog`: printf-style functions
  that record the caller's source position, `Library` for loggers handed to
  third-party libraries (their Info is demoted to Debug), `Spinner` for
  progress while a long step runs (drawn on stderr only when it is a
  terminal, kept below the log lines; a handler offers it by implementing
  `StatusHandler`), and the secret redaction rules (`Redactor`,
  `RegisterSecret`, `Secret`, `RedactURL`).
  `sdk/log/logtest` captures records in tests without touching
  `slog.Default`. Handlers, levels and the log format are set up by the
  core; a module never reconfigures them.
- `sdk/output` writes a command's result to stdout: `Streams`, a `Printer`
  bound to them and a format, and the `Result` interface. `BindFormat`
  declares the `-o/--format` flag with the formats a command accepts and its
  fixed default, so every command spells the flag the same way;
  `ResolveFormat` picks the format from a flag value and that default.
  Whether stdout is a terminal affects styling only, never the format. JSON
  is built in; the core adds other machine formats, such as YAML
  (`FormatYAML`, named here and encoded by the core), with `WithEncoder`.
  `Emit` writes a result whole or not at all; a result too
  large to hold goes item by item through `Printer.Stream` - JSON Lines in
  JSON, one `Human` rendering per item for people, and in a core format
  only when the core adds a stream encoder with `WithStreamEncoder`. A
  stream is not all-or-nothing: on failure the items written stay, and the
  exit code tells the consumer the stream was cut. `Normalize` makes values
  decoded from MessagePack encodable - maps keyed by interfaces, integers or
  bools get string keys, colliding keys are an error - and the JSON encoder
  applies it to everything it writes.
- `sdk/scope` is the one rule of what a project is and where its scopes
  are, which the core and the modules share: `FindProject` climbs from a
  directory to the nearest one with a project marker - an
  `app.manifest.toml`, or an `instances.yaml` or a `config.yaml` of a
  template under `.templates/` - and finds none inside a version installed
  into the user or system scope (`InstalledTree`, `FindInstallation`);
  `ProjectRoot`, `UserRoot` and `SystemRoot` are the roots of the scopes,
  the last two moved by `TT_USER_ROOT` and `TT_SYSTEM_ROOT`, with the names
  of the directories under them, and `VarLinks`, `LinkVar` and
  `CheckVarLinks` the links of an installed version to the runtime data of
  its instances. `Project().Root()` is the project `FindProject` finds from
  `Project().Dir()`. It depends on the standard library only.

- `sdk/sdktest` fakes the core for a module's tests. `New` returns
  `Services` that, like the core's, panic on everything but `Log` until
  `Start`; options set the Tarantool (`WithTarantool`), the project
  directory (`WithProject`, a temporary directory by default), the answers
  `Confirm` returns in order (`WithAnswers`) or `--no-prompt`
  (`WithNoPrompt`), stdin (`WithStdin`), how integrity opens files
  (`WithIntegrity`) and the cluster configuration a source returns, from
  YAML built with the Tarantool hierarchy (`WithClusterConfig`, with the
  directory it reports set by `WithClusterConfigDir`; any other source is not
  found). `Connect` connects to nothing: the `Conn` of a target
  `WithInstance` names answers every `Eval` with the function it gives,
  which returns `EvalError` for a Lua error, `WithConnectError` makes a
  target fail, any other target is not found, and the calls and the
  connections left open are recorded (`Connects`, `Evals`, `Conns`); an
  `Eval` its context ends closes the `Conn`, as the core's does.
  `LocalInstances` selects among the instances `WithLocalInstances` gives,
  as the core selects, all of them for no target, the applications in the
  order the targets name them, and records the calls
  (`LocalInstancesCalls`); a target that selects none is not found, and one
  that is no instance target, as `app` or `app:`, is refused with the
  message of the core. The
  project reads, edits and installs nothing: its root is the
  project directory or the one `WithRoot` sets, its manifest the one
  `WithManifest` gives - empty by default, none with `WithNoManifest` - with
  the names `WithDeclared` declares, and `AddDependency`,
  `RemoveDependency`, `AddComponent`, `UpdateComponent` and
  `RemoveComponent` change it as the core would; what it is asked is
  recorded (`AddedDependencies`, `RemovedDependencies`, `AddedComponents`,
  `UpdatedComponents`, `RemovedComponents`, `Resolves`, `Builds`,
  `Installs`, `PreparedEnvironments`, and `Indexed` for `Rocks().Index`),
  `AddDependency`, `RemoveDependency` and `Resolve` report the moves
  `WithMoves` gives, the two fail with `WithAddDependencyError` and
  `Resolve` with `WithResolveError`, `ReadLock` returns the lock `WithLock`
  gives, and `Build`, `BuildContext`, `Install` and `Index` run the
  functions `WithBuild`, `WithBuildContext`, `WithInstall` (in the root)
  and `WithIndex` give, to do what the core would.
  `Stdout`, `Stderr` and `Records` return what the
  module wrote and logged. `Run` hangs a constructor's mounts on a root,
  creating the groups on their paths, starts the services and executes a
  command line; `RunModule` does so for a `ModuleConstructor`, and fails
  the test on a backend or a file check of its `Module` the core would
  refuse. `Exit` ends the command, not the test binary: `Run` reports
  it with `Result.Exited` and the error given. Its printers encode the human
  format and JSON; YAML is the core's. Its root does not traverse the groups,
  as the root of tt does: a non-persistent flag of a group, which tt parses
  between the group and the subcommand, is unknown there, so a flag the
  subcommands of a group share belongs in its persistent flags.

stdout carries only a command's result. Diagnostics, progress and prompts go
to stderr.

A first module, from `go mod init` to a distribution, is in
[Write a tt module](https://tarantool.github.io/tt/how-to/write-a-module/).

## Versioning

Each module is versioned on its own with tags prefixed by its directory, for
example `sdk/connect/v0.1.0`; the contract module's tags are `sdk/v0.x.y`.
No such tags exist yet: inside this repository the modules resolve through
`replace` directives in the `go.mod` files that use them, and their own
`go.mod` files require each other at placeholder versions (`v0.0.0-0`) that
exist nowhere else. A placeholder sorts below every pseudo-version, so that
a version a module outside the repository asks for wins over it.

A module outside this repository therefore requires the SDK modules at a
commit of tt, every one it uses at the same commit, and `sdk/build` and
`sdk/connect` whenever it uses `sdk` (`sdk/connect` and `sdk/dial` for
`sdk/cluster`), so that the pseudo-version takes the place of the
placeholder:

```sh
go get github.com/tarantool/tt/sdk@<commit> github.com/tarantool/tt/sdk/build@<commit> \
    github.com/tarantool/tt/sdk/connect@<commit>
```

The distribution build builds every module on the SDK at the commit of its
core, whatever the module requires, so keep the commit the one of the core
the module is built on. To work against a local checkout of tt, on changes
of the SDK not pushed yet, `tt binary workspace` writes a `go.work` that
replaces every module of the checkout with its directory, with no version;
`go work use` of the SDK directories fails while the module requires a
commit Go cannot download. See
[Develop together with the core](https://tarantool.github.io/tt/how-to/write-a-module/#develop-together-with-the-core).

## Stability

The API of every SDK module is alpha and may change incompatibly in any
release.
