#!/usr/bin/env bash
# Run the tests of the build backends again with -json and check what they
# skipped (ci/backend_skips.py): CI installs every tool they need
# (.github/actions/setup-build-toolchains), so a test skipped for a missing
# one fails the job. The tests ran already with mage unit:*, which reports
# no skips; this run is for the report.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
out=${RUNNER_TEMP:-$root/.cache}/build-backend-tests
os=${RUNNER_OS:-$(uname -s | sed 's/Darwin/macOS/')}

mkdir -p "$out"
cd "$root"

(cd modules/build && go test -tags integration -count=1 -json ./... >"$out/modules.json")
(cd sdk && go test -tags integration -count=1 -json ./build/... >"$out/sdk.json")
go test -tags integration -count=1 -json ./cli/manifest/build/... >"$out/cli.json"

python3 ci/backend_skips.py --os "$os" "$out"/modules.json "$out"/sdk.json "$out"/cli.json
