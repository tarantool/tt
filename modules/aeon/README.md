# The aeon module

`tt aeon connect` opens an interactive SQL console to an Aeon router over
gRPC. It is a tt module: `aeon.New` is its constructor, and tt's main package
lists it in the table of modules it hands to the core.

This directory is a Go module of its own,
`github.com/tarantool/tt/modules/aeon`. Its `go.mod` requires the tt SDK
(`github.com/tarantool/tt/sdk` and the modules under `sdk/`) and never tt's
root module, so the compiler refuses any import of tt's internals: the SDK is
all the module sees. Inside the tt repository the SDK modules come from their
directories through `replace`.

- `aeon.go`, `connect.go` - the commands and how the arguments of
  `tt aeon connect` resolve to an address: an Aeon URL as it is, or the
  `roles_cfg/aeon.grpc/advertise` section of an instance in a cluster
  configuration read through the SDK's `Services.ClusterConfig`.
- `internal/client` - the gRPC client and the console's command processor.
- `pb` - the generated gRPC bindings, also used by the mock Aeon server of the
  integration tests (`test/integration/aeon/server`).

## Aeon gRPC bindings

The Go bindings in `pb/` are generated from the
[aeon-api-protos](https://github.com/tarantool/aeon-api-protos) schema and are
committed. Building, testing and linting tt never needs the schema itself.

### Regenerate `pb`

`buf.gen.yaml` names the schema commit, `generate-pb.sh` runs
[buf](https://buf.build) and both protoc plugins through `go run` at pinned
versions, so only `go` and `git` are required:

```sh
modules/aeon/generate-pb.sh
```

CI runs the same script (`.github/workflows/aeon-pb.yml`) and fails when its
output differs from the committed `pb/`.

### Move to a newer schema

1. Put the full commit hash of aeon-api-protos into `commit:` in
   `modules/aeon/buf.gen.yaml`. A new `.proto` file also needs its own
   `M<file>.proto=...` option there.
2. Run `modules/aeon/generate-pb.sh` and commit `buf.gen.yaml` together with
   `pb/`.
3. Make the matching changes to the `tt aeon connect` code.
