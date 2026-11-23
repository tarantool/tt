#!/usr/bin/env bash
# Build Tarantool CE from the source of a release as a PIE with its debugging
# information, the tarantool test/integration/coredump/test_gdb_tools.py takes
# a core of with TT_TEST_PIE_TARANTOOL:
#
#   ci/build-tarantool-pie.sh <version> <dest>
#
# The official packages of Tarantool 3.x are static executables, linked at the
# addresses they run at; a tarantool a user builds is usually a PIE, which gdb
# relocates as it reads a core, and an extension that keeps an address it read
# before breaks on it alone. The coredump-pie job of Full CI runs the tests on
# the one this script builds.
#
# <version> is a release, x.y.z: its tag of tarantool/tarantool is cloned with
# the submodules and built out of tree, RelWithDebInfo, as a user builds it;
# the compilers of Ubuntu and Debian make a PIE by default. <dest>/bin/tarantool
# is checked to be one, of type ET_DYN, dynamically linked, with .debug_info,
# and to print the version of the tag. Its path is printed, for
# TT_TEST_PIE_TARANTOOL:
#
#   TT_TEST_PIE_TARANTOOL=$(ci/build-tarantool-pie.sh 3.9.0 ~/tarantool-pie)
#
# <dest> is the cache: a <dest>/bin/tarantool there is only checked, and is
# written last, so that a build that fails leaves none. CI caches <dest> by the
# version, the runner and the hash of this script. The binary links the
# libraries of the system it is built on (ICU, readline, OpenSSL), and runs on
# that system alone.
#
# The sources and the build tree go to TARANTOOL_PIE_BUILD_DIR, <dest>.build by
# default, removed after a build that succeeds; the logs of cmake are there.
# TARANTOOL_PIE_GIT is the repository cloned, tarantool/tarantool on GitHub by
# default.
#
# What the build needs on Ubuntu, which .github/actions/prepare-ce-test-env
# installs: build-essential, cmake 3.18 or later, git, autoconf, automake and
# libtool (the bundled libunwind), libreadline-dev, libncurses-dev,
# libicu-dev, libssl-dev, zlib1g-dev; readelf of binutils for the checks.
set -euo pipefail

if [ $# -ne 2 ]; then
    echo "usage: $0 <version> <dest>" >&2
    exit 2
fi
version=$1
dest=$2
if ! [[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "$0: $version is no release x.y.z" >&2
    exit 2
fi
git_url=${TARANTOOL_PIE_GIT:-https://github.com/tarantool/tarantool.git}

log() { echo "build-tarantool-pie.sh: $*" >&2; }

mkdir -p "$dest/bin"
dest=$(cd "$dest" && pwd)
work=${TARANTOOL_PIE_BUILD_DIR:-$dest.build}
bin=$dest/bin/tarantool

if [ ! -x "$bin" ]; then
    rm -rf "$work"
    mkdir -p "$work"
    src=$work/src
    build=$work/build
    # GitHub fails a clone now and then: try three times.
    for attempt in 1 2 3; do
        log "cloning $version of $git_url, attempt $attempt"
        rm -rf "$src"
        # The tag of a shallow clone is enough for the version, which
        # CMakeLists.txt takes from git describe.
        if git -c advice.detachedHead=false clone --quiet --depth 1 \
            --branch "$version" --recurse-submodules --shallow-submodules \
            "$git_url" "$src" &&
            # LuaJIT takes its own version from git describe in its submodule
            # (cmake/SetVersion.cmake), which needs its history back to a tag.
            git -C "$src/third_party/luajit" fetch --quiet --unshallow --tags; then
            break
        fi
        if [ "$attempt" -eq 3 ]; then
            exit 1
        fi
        sleep 10
    done
    cmake_version=$(cmake --version)
    log "building $version with ${cmake_version%%$'\n'*} on $(nproc) CPUs"
    started=$(date +%s)
    if ! cmake -S "$src" -B "$build" -DCMAKE_BUILD_TYPE=RelWithDebInfo \
        > "$work/cmake.log" 2>&1; then
        tail -50 "$work/cmake.log" >&2
        exit 1
    fi
    if ! cmake --build "$build" --target tarantool --parallel "$(nproc)" \
        > "$work/build.log" 2>&1; then
        tail -50 "$work/build.log" >&2
        exit 1
    fi
    log "built in $(($(date +%s) - started)) s"
    cp "$build/src/tarantool" "$bin.tmp"
    mv "$bin.tmp" "$bin"
    rm -rf "$work"
fi

fail() {
    log "$bin: $*"
    exit 1
}
# The output is taken whole rather than piped to grep -q, which may exit
# before readelf has written all of it and fail the pipe.
header=$(readelf -h "$bin") || fail "is no ELF file"
segments=$(readelf -l "$bin")
sections=$(readelf -S "$bin")
[[ $header =~ Type:\ +DYN ]] || fail "is no PIE: not of type ET_DYN"
[[ $segments == *INTERP* ]] || fail "is not dynamically linked"
[[ $sections == *.debug_info* ]] || fail "has no .debug_info"
printed=$("$bin" --version) || fail "does not run"
printed=${printed%%$'\n'*}
case "$printed" in
    "Tarantool $version-0-g"*) ;;
    *) fail "prints the version $printed, not that of the tag $version" ;;
esac
log "$printed, a PIE with .debug_info"
echo "$bin"
