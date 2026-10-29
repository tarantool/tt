#!/usr/bin/env bash
# Run a command and retry it twice when its output shows a network failure.
# Any other failure, such as a license violation or a vulnerability, fails at
# once.
set -uo pipefail

network_errors='stream error|INTERNAL_ERROR|connection (reset|refused|timed out)'
network_errors+='|i/o timeout|TLS handshake timeout|no such host|network is unreachable'
network_errors+='|temporary failure|unexpected EOF|context deadline exceeded'
network_errors+='|HTTP[^[:digit:]]*5[0-9]{2}|[[:space:]]5[0-9]{2}[[:space:]]'

log=$(mktemp)
trap 'rm -f "$log"' EXIT
for attempt in 1 2 3; do
    if "$@" 2>&1 | tee "$log"; then
        exit 0
    fi
    if [ "$attempt" -eq 3 ] || ! grep -Eiq "$network_errors" "$log"; then
        exit 1
    fi
    echo "Network failure; retrying in 10 seconds (attempt $attempt of 3)." >&2
    sleep 10
done
