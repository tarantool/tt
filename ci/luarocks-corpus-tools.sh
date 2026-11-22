#!/usr/bin/env bash
# Install what the LuaRocks corpus check (test/integration/luarocks_corpus)
# needs beyond the toolchain of the rocks catalogue check: tt 2, whose tt rocks
# builds the rocks, zig, and Go and Rust where the machine has none, as in the
# containers of ci/rocks-container.sh. .github/workflows/rocks.yml runs it on
# the runners and in the containers; by hand, in a container of a
# distribution, after ci/rocks-container.sh:
#
#   bash ci/luarocks-corpus-tools.sh /opt/corpus-tools
#   . /opt/corpus-tools/env.sh
#
# Usage: luarocks-corpus-tools.sh DIR
#
# Into DIR, each download checked against its pinned sha256:
# - tt 2.15.2 of its release on Linux; on macOS, where tt 2 has no release
#   for arm64, the tt of Homebrew, unlinked;
# - zig 0.17.1 of ziglang.org, which the zig rocks need;
# - Go 1.27.4 of go.dev, when there is no go on PATH;
# - Rust stable through rustup 1.29.3, with the minimal profile, when there is
#   no cargo on PATH.
#
# Then it writes DIR/env.sh, which exports TT_TEST_TT2 and puts the
# directories of the programs first on PATH, and, when they are set, adds
# the same to $GITHUB_ENV and $GITHUB_PATH.
set -euo pipefail

if [ $# -ne 1 ]; then
    echo "Usage: $0 DIR" >&2
    exit 2
fi
mkdir -p "$1"
dir=$(cd "$1" && pwd -P)

tt2_version=2.15.2
zig_version=0.17.1
# The GO_VERSION of the workflows; go-hello needs Go 1.22 or later.
go_version=1.27.4
rustup_version=1.29.3

case "$(uname -s)/$(uname -m)" in
    Linux/x86_64)
        platform=linux-amd64
        tt2_sum=4e68cfa969c9506fdddb434b5cd3c284a8c70b8afa2d11debbc11e804cb8c03e
        zig_platform=x86_64-linux
        zig_sum=3581863e3cbcfd0fce0efc682ce7e9e93da96668a977791d356a26d2a7e9b17a
        rustup_triple=x86_64-unknown-linux-gnu
        rustup_sum=b1c8cb14e30889e7e2999e6b69e62d9e9b9ffc0f4df8180564d306d9680e6c55
        ;;
    Linux/aarch64)
        platform=linux-arm64
        tt2_sum=865cd371724db89dc6849e6cffd07871b0e31ab41586cc1943212fe6a09902dc
        zig_platform=aarch64-linux
        zig_sum=a1546d5646eb184437918d6635ca6b762a0e889eac09cef720ee7a1ed3fe7795
        rustup_triple=aarch64-unknown-linux-gnu
        rustup_sum=9f608c142e0e38a78957d0661ba1dfaf0b6fb37184cfaa4b9a0aa57af6184282
        ;;
    Darwin/arm64)
        platform=darwin-arm64
        zig_platform=aarch64-macos
        zig_sum=75f1bc7a1e9852f17ec9a0c1895f767c4ec56d46bc8a834920eece1d0359dd8d
        ;;
    *)
        echo "$0: no tools for $(uname -s) $(uname -m)" >&2
        exit 1
        ;;
esac

paths=()
env_lines=()

# fetch FILE URL [SHA256]: download URL to FILE, and check its sha256.
fetch() {
    curl -fsSL --retry 3 -o "$1" "$2"
    if [ $# -gt 2 ]; then
        if command -v sha256sum > /dev/null; then
            echo "$3  $1" | sha256sum -c --quiet -
        else
            echo "$3  $1" | shasum -a 256 -c -
        fi
    fi
}

if [ "$platform" = darwin-arm64 ]; then
    # The formula of a runner image may be one that still depends on
    # bash-completion, which conflicts with the bash-completion@2 of the
    # image: the bottle goes in without the dependencies of the formula and
    # with the one library tt links, openssl@3.
    HOMEBREW_NO_AUTO_UPDATE=1 brew install openssl@3
    HOMEBREW_NO_AUTO_UPDATE=1 brew install --ignore-dependencies tt
    # Off PATH: the tt of the tests is tt 3.
    brew unlink tt
    tt2="$(brew --prefix tt)/bin/tt"
else
    archive=tt-$tt2_version-$platform.tar.gz
    fetch "$dir/$archive" "https://github.com/tarantool/tt/releases/download/v$tt2_version/$archive" \
        "$tt2_sum"
    mkdir -p "$dir/tt2"
    tar -xzf "$dir/$archive" -C "$dir/tt2" tt
    rm "$dir/$archive"
    tt2=$dir/tt2/tt
fi
"$tt2" version
env_lines+=("TT_TEST_TT2=$tt2")

archive=zig-$zig_platform-$zig_version.tar.xz
fetch "$dir/$archive" "https://ziglang.org/download/$zig_version/$archive" "$zig_sum"
rm -rf "$dir/zig"
mkdir -p "$dir/zig"
tar -xJf "$dir/$archive" -C "$dir/zig" --strip-components=1
rm "$dir/$archive"
paths+=("$dir/zig")
"$dir/zig/zig" version

if ! command -v go > /dev/null; then
    case "$platform" in
        linux-amd64) go_sum=7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840 ;;
        linux-arm64) go_sum=3392582988a273a87ab5043ee1b2c9c17a2c3c00580bf9326c47a2e0aaaeac92 ;;
        *)
            echo "$0: no go on PATH" >&2
            exit 1
            ;;
    esac
    archive=go$go_version.$platform.tar.gz
    fetch "$dir/$archive" "https://go.dev/dl/$archive" "$go_sum"
    rm -rf "$dir/go"
    tar -xzf "$dir/$archive" -C "$dir"
    rm "$dir/$archive"
    paths+=("$dir/go/bin")
    "$dir/go/bin/go" version
fi

if ! command -v cargo > /dev/null; then
    if [ -z "${rustup_triple:-}" ]; then
        echo "$0: no cargo on PATH" >&2
        exit 1
    fi
    fetch "$dir/rustup-init" \
        "https://static.rust-lang.org/rustup/archive/$rustup_version/$rustup_triple/rustup-init" \
        "$rustup_sum"
    chmod +x "$dir/rustup-init"
    CARGO_HOME=$dir/cargo RUSTUP_HOME=$dir/rustup "$dir/rustup-init" -y -q --no-modify-path \
        --profile minimal --default-toolchain stable
    rm "$dir/rustup-init"
    env_lines+=("CARGO_HOME=$dir/cargo" "RUSTUP_HOME=$dir/rustup")
    paths+=("$dir/cargo/bin")
    CARGO_HOME=$dir/cargo RUSTUP_HOME=$dir/rustup "$dir/cargo/bin/cargo" --version
fi

{
    for line in "${env_lines[@]}"; do
        printf 'export %s=%q\n' "${line%%=*}" "${line#*=}"
    done
    for path in "${paths[@]}"; do
        # shellcheck disable=SC2016 # $PATH is of the shell that sources it.
        printf 'export PATH=%q:"$PATH"\n' "$path"
    done
} > "$dir/env.sh"
cat "$dir/env.sh"
if [ -n "${GITHUB_ENV:-}" ]; then
    printf '%s\n' "${env_lines[@]}" >> "$GITHUB_ENV"
fi
if [ -n "${GITHUB_PATH:-}" ] && [ ${#paths[@]} -gt 0 ]; then
    printf '%s\n' "${paths[@]}" >> "$GITHUB_PATH"
fi
