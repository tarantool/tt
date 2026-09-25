# Aeon gRPC bindings

The Go bindings in `pb/` are generated from the
[aeon-api-protos](https://github.com/tarantool/aeon-api-protos) schema and are
committed. Building, testing and linting tt never needs the schema itself.

## Regenerate `pb`

`buf.gen.yaml` names the schema commit, `generate-pb.sh` runs
[buf](https://buf.build) and both protoc plugins through `go run` at pinned
versions, so only `go` and `git` are required:

```sh
modules/aeon/generate-pb.sh
```

CI runs the same script (`.github/workflows/aeon-pb.yml`) and fails when its
output differs from the committed `pb/`.

## Move to a newer schema

1. Put the full commit hash of aeon-api-protos into `commit:` in
   `modules/aeon/buf.gen.yaml`. A new `.proto` file also needs its own
   `M<file>.proto=...` option there.
2. Run `modules/aeon/generate-pb.sh` and commit `buf.gen.yaml` together with `pb/`.
3. Make the matching changes to the `tt aeon connect` code.
