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
standard library only (testify for its tests).

- `sdk` (the root package) defines the exit-code contract: the codes tt
  returns (1 for a request the caller must fix, 2 for a failure of the
  system, 3 for partial success), the `ExitError` carrier and `ExitCode`,
  under which code 1 never hides a deeper code. Classifying dial and
  filesystem errors as 2 is the core's job, not the SDK's.
- `sdk/log` is the logging facade over `log/slog`: printf-style functions
  that record the caller's source position, `Library` for loggers handed to
  third-party libraries (their Info is demoted to Debug), and the secret
  redaction rules (`Redactor`, `RegisterSecret`, `Secret`, `RedactURL`).
  `sdk/log/logtest` captures records in tests without touching
  `slog.Default`. Handlers, levels and the log format are set up by the
  core; a module never reconfigures them.
- `sdk/output` writes a command's result to stdout: `Streams`, a `Printer`
  bound to them and a format, and the `Result` interface. `ResolveFormat`
  picks the format from the `--format` flag and the command's fixed default;
  whether stdout is a terminal affects styling only, never the format. JSON
  is built in; the core adds other machine formats, such as YAML, with
  `WithEncoder`.

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
