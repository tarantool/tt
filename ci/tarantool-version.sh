#!/usr/bin/env bash
# Print the Tarantool version CI tests for an edition (ce or ee) and the alias
# min or latest, as set in tarantool-versions.env. Any other value, such as an
# exact version or master, is printed as is.
set -euo pipefail

edition=$1
requested=$2
case "$requested" in
    min | latest) ;;
    *)
        echo "$requested"
        exit 0
        ;;
esac
# macOS ships bash 3.2, which has no ${var^^}.
key=$(echo "${edition}_${requested}" | tr '[:lower:]' '[:upper:]')
version=$(sed -n "s/^$key=//p" "$(dirname "$0")/tarantool-versions.env")
if [ -z "$version" ]; then
    echo "$key is not set in tarantool-versions.env" >&2
    exit 1
fi
echo "$version"
