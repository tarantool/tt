#!/usr/bin/env bash
# Run the tests of the build backends again with -json and check what they
# skipped (ci/backend_skips.py): CI installs every tool they need
# (.github/actions/setup-build-toolchains), so a test skipped for a missing
# one fails the job. The tests ran already with mage unit:*, which reports
# no skips; this run is for the report, which is written even when a test
# fails, the script failing then too.
set -uo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
out=${RUNNER_TEMP:-$(mktemp -d)}/build-backend-tests
os=${RUNNER_OS:-$(uname -s | sed 's/Darwin/macOS/')}

mkdir -p "$out"
cd "$root" || exit 2

failed=0

# modules/build and sdk/build are modules of their own; cli/manifest/build
# is of the root one.
for dir in modules/build sdk/build .; do
	packages=./...
	if [ "$dir" = . ]; then
		packages=./cli/manifest/build/...
	fi

	name=$(echo "$dir" | tr / - | sed 's/^\.$/cli/')
	if ! (cd "$dir" && go test -tags integration -count=1 -json $packages >"$out/$name.json"); then
		echo "go test failed in $dir" >&2
		failed=1
	fi
done

python3 ci/backend_skips.py --os "$os" "$out"/*.json || failed=1

exit "$failed"
