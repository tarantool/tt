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

- `connect` parses connection strings and URIs and resolves credentials.
- `dial` builds Tarantool dialers for the plain and SSL transports.
- `cluster` collects and publishes cluster configuration from files, etcd and
  Tarantool config storage.
- `integrity` defines the integrity-checking interface.

## Contract module

`github.com/tarantool/tt/sdk`, rooted at `sdk/` itself, is the contract
between tt and its modules. The nested `connect`, `dial`, `cluster` and
`integrity` are not part of it: each has its own `go.mod`. It depends on the
standard library, `github.com/spf13/cobra` and `github.com/spf13/pflag`
(testify for its tests). Cobra and pflag types are part of the contract, so
the module pins their versions: a module builds against the cobra the core
uses, and the core never requires an older one.

- `sdk` (the root package) defines the module contract. A module is a
  `Constructor`: a function that takes the core's `Services` and returns
  `Mount`s - a fresh cobra command and the path of the group it goes under,
  groups nobody provides being created. `Services` is implemented by the core
  and by `sdk/sdktest` only: `Log` (the module's logger, valid at any time),
  and, once tt is configured, `Tarantool` (the `Tarantool` to run, with its
  `Path` and structured `TarantoolVersion`), `Integrity` (`Open` a file
  through the integrity checks), `Project` (the project `Dir`), `Confirm` (a
  yes/no prompt on stderr that answers the given fallback under
  `--no-prompt`) and `Streams` (the standard streams and a `Printer` with the
  core's formats). `ErrNotFound` is wrapped by what is not found.
  `CoreVersion` reports the version of the tt core the binary was built
  with, from the build info.
- `sdk` also defines the exit-code contract: the codes tt returns (1 for a
  request the caller must fix, 2 for a failure of the system, 3 for partial
  success), the `ExitError` carrier and `ExitCode`, under which code 1 never
  hides a deeper code. Classifying dial and filesystem errors as 2 is the
  core's job, not the SDK's. `UsageError` (`WithUsage`, `Usagef`) is an
  error in how a command was invoked: tt logs it and prints the command's
  usage.
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

- `sdk/sdktest` fakes the core for a module's tests. `New` returns
  `Services` that, like the core's, panic on everything but `Log` until
  `Start`; options set the Tarantool (`WithTarantool`), the project
  directory (`WithProject`, a temporary directory by default), the answers
  `Confirm` returns in order (`WithAnswers`) or `--no-prompt`
  (`WithNoPrompt`), stdin (`WithStdin`) and how integrity opens files
  (`WithIntegrity`). `Stdout`, `Stderr` and `Records` return what the module
  wrote and logged. `Run` hangs a constructor's mounts on a root, creating
  the groups on their paths, starts the services and executes a command
  line. Its printers encode the human format and JSON; YAML is the core's.

stdout carries only a command's result. Diagnostics, progress and prompts go
to stderr.

## Versioning

Each module is versioned on its own with tags prefixed by its directory, for
example `sdk/connect/v0.1.0`; the contract module's tags are `sdk/v0.x.y`.
No such tags exist yet: inside this repository the modules resolve through
`replace` directives in the `go.mod` files that use them.

## Stability

The API of every SDK module is alpha and may change incompatibly in any
release.
