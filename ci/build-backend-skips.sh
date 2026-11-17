#!/usr/bin/env bash
# Check what the tests of the build backends skipped (ci/backend_skips.py) in
# the go test -json events mage unit:* wrote into TT_TEST_JSON_DIR, so that
# the tests run once: CI installs every tool they need
# (.github/actions/setup-build-toolchains), so a test skipped for a missing
# one fails the job, and so does a backend no test of which ran.
set -uo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
os=${RUNNER_OS:-$(uname -s | sed 's/Darwin/macOS/')}
dir=${TT_TEST_JSON_DIR:?names no directory of the events of mage unit:*}

shopt -s nullglob
files=("$dir"/*.json)

if [ "${#files[@]}" -eq 0 ]; then
	echo "$dir has no events of go test: run mage unit:* with TT_TEST_JSON_DIR=$dir" >&2
	exit 2
fi

cd "$root" || exit 2

exec python3 ci/backend_skips.py --os "$os" "${files[@]}"
