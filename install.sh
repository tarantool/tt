#!/bin/sh
# Install tt 3 from a GitHub release, or build it from a commit.
#
#   curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
#
# Pass options after `sh -s --`:
#
#   curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | \
#       sh -s -- --version 3.0.0-alpha.1 --dir /usr/local/bin
#
#   curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | \
#       sh -s -- --commit 1508ce93
#
# The version, the commit and the directory can also be set with the
# TT_VERSION, TT_COMMIT and TT_INSTALL_DIR environment variables.
# GITHUB_TOKEN, when set, is sent to the GitHub API that lists the releases.
#
# The binary is written next to its target and renamed into place, so a
# running tt is replaced rather than overwritten in place.

set -eu

REPO="tarantool/tt"
GITHUB="https://github.com"
API="https://api.github.com"

usage() {
    cat <<EOF
Install tt 3 from a GitHub release of ${REPO}, or build it from a commit.

Usage: install.sh [--version <version> | --commit <commit>] [--dir <dir>]
                  [--openssl]

Options:
  -v, --version <version>  tt version to install, e.g. 3.0.0-alpha.1
                           (default: \$TT_VERSION or the newest tt 3 release:
                           the newest stable one, or the newest pre-release
                           while there is no stable one).
  -c, --commit <commit>    build tt from a commit of ${REPO} with go build
                           instead of installing a release (default:
                           \$TT_COMMIT): a hash, full or short, a branch or a
                           tag. Needs git and Go.
  -d, --dir <dir>          directory to put the tt binary into
                           (default: \$TT_INSTALL_DIR, or /usr/local/bin for
                           root and ~/.local/bin otherwise).
      --openssl            install the tt-openssl build, with the OpenSSL TLS
                           backend instead of the pure-Go one: Linux only for
                           a release; a build needs a C compiler and the
                           OpenSSL headers.
  -h, --help               show this help.
EOF
}

say() {
    printf 'tt-install: %s\n' "$*" >&2
}

die() {
    say "error: $*"
    exit 1
}

has() {
    command -v "$1" >/dev/null 2>&1
}

# download <url> <file> [<header>]
download() {
    dl_url=$1
    dl_file=$2
    shift 2
    if has curl; then
        curl --proto '=https' --tlsv1.2 -fsSL --retry 3 \
            ${1+-H} ${1+"$1"} -o "$dl_file" "$dl_url"
    else
        # Only the flags busybox wget knows too.
        wget -q ${1+"--header=$1"} -O "$dl_file" "$dl_url"
    fi
}

# Prints the tag of the newest tt 3 release: the newest stable one, or the
# newest pre-release while there is none. /releases/latest does not do: it
# names the newest release of any major version, and 2.x is still released.
# The API lists the releases newest first; every page is read, so a stable
# release is never missed behind a page of 2.x ones.
latest_tag() {
    page=1
    tag=
    while :; do
        url="${API}/repos/${REPO}/releases?per_page=100&page=${page}"
        json="${tmp}/releases-${page}.json"
        if [ -n "${GITHUB_TOKEN:-}" ]; then
            download "$url" "$json" "Authorization: Bearer ${GITHUB_TOKEN}"
        else
            download "$url" "$json"
        fi ||
            die "cannot list the releases of ${REPO}" \
                "(the API rate limit or a bad GITHUB_TOKEN? --version skips the lookup)"
        # A stable tt 3 release ends the search; a pre-release is kept only
        # until a stable one shows up, on this page or a later one.
        found=$(awk '
            /"tag_name":/ { split($0, f, "\""); tag = f[4] }
            /"draft":/ { draft = ($0 ~ /true/) }
            /"prerelease":/ && tag ~ /^v3\./ && !draft {
                if ($0 ~ /false/) { print "stable" tag; exit }
                if (pre == "") pre = tag
            }
            END { if (pre != "") print "pre" pre }
        ' "$json")
        case "$found" in
        stable*)
            tag=${found#stable}
            break
            ;;
        pre*)
            [ -n "$tag" ] || tag=${found#pre}
            ;;
        esac
        [ "$(grep -c '"tag_name":' "$json")" -ge 100 ] || break
        page=$((page + 1))
        [ "$page" -le 10 ] || break
    done
    [ -n "$tag" ] || die "${REPO} has no tt 3 release"
    printf '%s\n' "$tag"
}

sha256() {
    if has sha256sum; then
        sha256sum "$1" | cut -d ' ' -f 1
    elif has shasum; then
        shasum -a 256 "$1" | cut -d ' ' -f 1
    else
        die "neither sha256sum nor shasum is found"
    fi
}

# Builds tt of $commit into ${tmp}/tt as `mage build` does: with the build
# tags of the TLS backend and the version git describe gives the commit,
# stamped with -ldflags. The clone has the history and the tags describe
# needs, but not the files of other commits. A commit no branch has, such as
# one of a pull request, is fetched by its full hash.
build_commit() {
    src="${tmp}/src"
    say "cloning ${GITHUB}/${REPO}"
    git clone --quiet --filter=blob:none --no-checkout \
        "${GITHUB}/${REPO}.git" "$src" ||
        die "cannot clone ${GITHUB}/${REPO}"
    if ! git -C "$src" checkout --quiet --detach "$commit" 2>/dev/null; then
        git -C "$src" fetch --quiet --filter=blob:none origin "$commit" ||
            die "${REPO} has no commit ${commit}; a commit no branch has" \
                "needs its full hash"
        git -C "$src" checkout --quiet --detach FETCH_HEAD ||
            die "cannot check out ${commit}"
    fi
    # tt 2 builds with submodules and generated code, which mage makes.
    grep -q '^module github.com/tarantool/tt/v3$' "${src}/go.mod" ||
        die "${commit} is not a commit of tt 3, the only one this script builds"

    git_commit=$(git -C "$src" rev-parse --short HEAD)
    git_tag=$(git -C "$src" describe --tags 2>/dev/null) || git_tag=
    git_since=
    if [ -n "$git_tag" ]; then
        git_since=$(git -C "$src" rev-list --count \
            "$(git -C "$src" describe --tags --abbrev=0)..HEAD")
    fi

    tags=netgo,osusergo
    cgo=0
    if [ "$project" = tt-openssl ]; then
        tags="${tags},openssl"
        cgo=1
    fi
    pkg=github.com/tarantool/tt/v3/cli/version
    ldflags="-s -w -X ${pkg}.gitTag=${git_tag} -X ${pkg}.gitCommit=${git_commit}"
    ldflags="${ldflags} -X ${pkg}.gitCommitSinceTag=${git_since}"

    say "building ${project} at ${git_commit}${git_tag:+ (${git_tag})}"
    (cd "$src" && CGO_ENABLED=$cgo go build -trimpath -tags "$tags" \
        -ldflags "$ldflags" -o "${tmp}/tt" ./cli) ||
        die "cannot build tt at ${git_commit}"
    version=$("${tmp}/tt" version --commit 2>/dev/null) || version=$git_commit
}

# Downloads the archive of the release into ${tmp} and unpacks tt from it,
# checked against checksums.txt of the release.
install_release() {
    if [ -z "$version" ]; then
        tag=$(latest_tag)
    else
        tag=v${version#v}
    fi
    version=${tag#v}

    archive="${project}-${version}-${os}-${arch}.tar.gz"
    base="${GITHUB}/${REPO}/releases/download/${tag}"

    say "downloading ${base}/${archive}"
    download "${base}/${archive}" "${tmp}/${archive}" ||
        die "cannot download ${archive}; is ${tag} a release of ${REPO}?"
    download "${base}/checksums.txt" "${tmp}/checksums.txt" ||
        die "cannot download checksums.txt of ${tag}"

    expected=$(awk -v f="$archive" '$2 == f { print $1 }' "${tmp}/checksums.txt")
    [ -n "$expected" ] || die "${archive} is not listed in checksums.txt"
    actual=$(sha256 "${tmp}/${archive}")
    [ "$actual" = "$expected" ] ||
        die "checksum mismatch for ${archive}: expected ${expected}, got ${actual}"

    tar -xzf "${tmp}/${archive}" -C "$tmp" tt
}

version=${TT_VERSION:-}
commit=${TT_COMMIT:-}
dir=${TT_INSTALL_DIR:-}
project=tt

while [ $# -gt 0 ]; do
    case "$1" in
    -v | --version)
        [ $# -ge 2 ] || die "$1 requires a value"
        version=$2
        shift 2
        ;;
    --version=*)
        version=${1#*=}
        shift
        ;;
    -c | --commit)
        [ $# -ge 2 ] || die "$1 requires a value"
        commit=$2
        shift 2
        ;;
    --commit=*)
        commit=${1#*=}
        shift
        ;;
    -d | --dir)
        [ $# -ge 2 ] || die "$1 requires a value"
        dir=$2
        shift 2
        ;;
    --dir=*)
        dir=${1#*=}
        shift
        ;;
    --openssl)
        project=tt-openssl
        shift
        ;;
    -h | --help)
        usage
        exit 0
        ;;
    *)
        usage >&2
        die "unknown option: $1"
        ;;
    esac
done

[ -z "$version" ] || [ -z "$commit" ] ||
    die "--version and --commit exclude each other"

if [ -n "$commit" ]; then
    has git || die "git is not found; --commit builds tt from source"
    has go || die "go is not found; --commit builds tt with Go:" \
        "https://go.dev/doc/install"
else
    has curl || has wget || die "neither curl nor wget is found"
    has tar || die "tar is not found"
fi

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported OS: $(uname -s)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac

# A process translated by Rosetta 2 sees x86_64, while the arm64 build is
# the native one.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
    [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = 1 ]; then
    arch=arm64
fi

# A release has builds for these only; a commit builds wherever Go does.
if [ -z "$commit" ]; then
    [ "$os" != darwin ] || [ "$arch" = arm64 ] ||
        die "tt 3 has no build for Intel macOS; build it from a commit" \
            "with --commit v3"
    [ "$project" = tt ] || [ "$os" = linux ] ||
        die "--openssl is supported on Linux only"
fi

if [ -z "$dir" ]; then
    if [ "$(id -u)" -eq 0 ]; then
        dir=/usr/local/bin
    else
        dir=${HOME}/.local/bin
    fi
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t tt-install)
stage=
trap 'rm -rf "$tmp"; [ -z "$stage" ] || rm -rf "$stage"' EXIT
trap 'exit 1' HUP INT TERM

if [ -n "$commit" ]; then
    build_commit
else
    install_release
fi

mkdir -p "$dir" || die "cannot create ${dir}"
# A mv onto a directory puts the file into it instead of replacing it; the
# message below would name an executable that was never installed.
if [ -d "${dir}/tt" ]; then
    die "${dir}/tt is a directory, not the tt binary; remove it or pick another --dir"
fi
# The staging directory is private and unpredictable: writing to a fixed
# name could follow a symlink planted there and truncate an unrelated file.
stage=$(mktemp -d "${dir}/.tt-install.XXXXXX") || die "cannot write to ${dir}"
cp "${tmp}/tt" "${stage}/tt" || die "cannot write to ${dir}"
chmod 755 "${stage}/tt"
mv -f "${stage}/tt" "${dir}/tt" || die "cannot install to ${dir}/tt"
rmdir "$stage" 2>/dev/null || true
stage=

say "installed ${project} ${version} to ${dir}/tt"

case ":${PATH}:" in
*":${dir}:"*) ;;
*) say "${dir} is not in PATH; add it with: export PATH=\"${dir}:\$PATH\"" ;;
esac
