#!/usr/bin/env bash
# Regenerate the Aeon gRPC bindings in ./pb from the aeon-api-protos schema.
#
# Usage: modules/aeon/generate-pb.sh
#
# Needs only go and git: buf and both protoc plugins are run through
# `go run <module>@<version>`, so nothing is installed and every version is
# pinned here and in buf.gen.yaml. The schema is fetched by buf from the git
# commit named in buf.gen.yaml.
#
# To move to a newer schema, put the full commit hash of aeon-api-protos into
# `commit:` in buf.gen.yaml, add an `M<file>.proto=...` option for every new
# .proto file (protoc-gen-go refuses a file without one), run this script and
# commit buf.gen.yaml together with ./pb.
set -euo pipefail

# buf itself.
BUF_VERSION=v1.73.0

# protoc-gen-go formats its output with the go/printer of the toolchain it is
# built with, and the layout of doc comments differs between Go releases, so
# the toolchain is pinned as well: the output does not depend on the go
# installed on the machine. It is the Go tt is built with (GO_VERSION in
# .github/workflows); move the two together and commit the regenerated ./pb.
export GOTOOLCHAIN=go1.27.1

cd "$(dirname "$0")"
go run "github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}" generate
