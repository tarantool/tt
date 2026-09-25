# tt SDK

The SDK is the part of tt that a compiled-in tt module may import. A module
must not import anything else from this repository.

## Layout

Each directory is a separate Go module:

| Directory        | Module path                              |
|------------------|------------------------------------------|
| `sdk/connect`    | `github.com/tarantool/tt/sdk/connect`    |
| `sdk/dial`       | `github.com/tarantool/tt/sdk/dial`       |
| `sdk/cluster`    | `github.com/tarantool/tt/sdk/cluster`    |
| `sdk/integrity`  | `github.com/tarantool/tt/sdk/integrity`  |

- `connect` parses connection strings and URIs and resolves credentials.
- `dial` builds Tarantool dialers for the plain and SSL transports.
- `cluster` collects and publishes cluster configuration from files, etcd and
  Tarantool config storage.
- `integrity` defines the integrity-checking interface.

A contract module `github.com/tarantool/tt/sdk`, rooted at `sdk/` itself, is
planned and does not exist yet.

## Versioning

Each module is versioned on its own with tags prefixed by its directory, for
example `sdk/connect/v0.1.0`. No such tags exist yet: inside this repository
the modules resolve through `replace` directives in the `go.mod` files that
use them.

## Stability

The API of `connect`, `dial`, `cluster` and `integrity` is alpha and may change
incompatibly in any release.
