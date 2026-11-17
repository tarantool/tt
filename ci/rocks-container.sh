#!/usr/bin/env bash
# Prepare a container of a Linux distribution for the rocks catalogue check:
# the distros job of .github/workflows/rocks.yml runs it in the image of each
# distribution, as root, from the root of the repository. It runs the same in
# docker by hand, on a copy of the repository the container may write to:
#
#   docker run --rm -v "$PWD:/src" -w /src rockylinux:9 \
#       bash ci/rocks-container.sh user 3.9 ./tt
#
# Usage: rocks-container.sh LEVEL SERIES TT
#
# It installs, with the package manager of the distribution (apt, dnf,
# pacman or emerge):
# - the toolchain the rocks build with: the C and C++ compilers, make, CMake,
#   git, patch, pkg-config, the archivers and the headers of OpenSSL and
#   zlib, which the GitHub runners have;
# - Python $PYTHON_VERSION (3.12 by default) through uv, with
#   test/requirements.txt in a virtual environment at $ROCKS_VENV
#   (/opt/rocks-venv by default): the pinned versions install as on the
#   runners, whatever Python the distribution has;
# - the system libraries the rocks build against, packages.<manager> of
#   test/rocks-catalog.yaml: one the distribution does not have is left out
#   and named, and the rock that needs it fails unless the catalogue skips
#   it there;
# - the Tarantool of the level, the newest release of SERIES: tt binary
#   install --scope LEVEL at project, user and system, the packages of
#   download.tarantool.org at os; on Gentoo, which builds it from the
#   overlay of Tarantool, the newest of the major version of SERIES there,
#   kept as a binary package in $ROCKS_PKGDIR when that is set.
#
# Then it prints, and writes to $GITHUB_OUTPUT when that is set, python=
# (the Python of the environment), root= (the root of the scope, empty at
# os), template= (at project, the directory of the project the Tarantool is
# installed in), tarantool= (the tarantool of the level) and binpkgs-key=
# (the cache key of $ROCKS_PKGDIR, empty but for a build of Gentoo).
set -euo pipefail

if [ $# -ne 3 ]; then
    echo "Usage: $0 LEVEL SERIES TT" >&2
    exit 2
fi
level=$1
series=$2
tt=$(realpath "$3")
pkgdir=${ROCKS_PKGDIR:+$(realpath -m "$ROCKS_PKGDIR")}
case "$level" in
    project | user | system | os) ;;
    *)
        echo "$0: the level is project, user, system or os, not $level" >&2
        exit 2
        ;;
esac
if [ "$(id -u)" -ne 0 ]; then
    echo "$0: run it as root" >&2
    exit 1
fi
cd "$(dirname "$0")/.."

# shellcheck source=/dev/null # The file of the image.
. /etc/os-release
distro=$ID${VERSION_ID:+-${VERSION_ID%%.*}}
venv=${ROCKS_VENV:-/opt/rocks-venv}
python_version=${PYTHON_VERSION:-3.12}
uv_version=0.13.8
start=$SECONDS

step() {
    echo
    echo "### $* (at $((SECONDS - start)) s)"
}

if command -v apt-get > /dev/null; then
    manager=apt
elif command -v dnf > /dev/null; then
    manager=dnf
elif command -v pacman > /dev/null; then
    manager=pacman
elif command -v emerge > /dev/null; then
    manager=emerge
else
    echo "$0: $distro has none of apt-get, dnf, pacman and emerge" >&2
    exit 1
fi

# apt -------------------------------------------------------------------------

apt_prepare() {
    export DEBIAN_FRONTEND=noninteractive
    if [ "$ID" = ubuntu ]; then
        sh ci/apt-mirrors.sh
    fi
    apt-get update -q
}

apt_install() {
    apt-get install -y -q --no-install-recommends "$@"
}

apt_available() {
    local package candidate
    for package in "$@"; do
        candidate=$(apt-cache policy "$package" 2> /dev/null | sed -n 's/^ *Candidate: //p')
        if [ -n "$candidate" ] && [ "$candidate" != "(none)" ]; then
            echo "$package"
        fi
    done
}

# dnf -------------------------------------------------------------------------

dnf_options=(
    -y -q --setopt=install_weak_deps=False --setopt=tsflags=nodocs
    --setopt=max_parallel_downloads=10
)

# The development packages of Enterprise Linux are in PowerTools (8) or CRB
# (9 and on), and most of the libraries of the rocks are in EPEL.
dnf_prepare() {
    case "$ID" in
        rocky | almalinux | centos | rhel)
            if [ "${VERSION_ID%%.*}" = 8 ]; then
                dnf_options+=(--enablerepo=powertools)
            else
                dnf_options+=(--enablerepo=crb)
            fi
            dnf "${dnf_options[@]}" install epel-release
            ;;
    esac
}

dnf_install() {
    dnf "${dnf_options[@]}" install "$@"
}

dnf_available() {
    dnf "${dnf_options[@]}" repoquery --qf '%{name}\n' "$@" 2> /dev/null | sort -u
}

# pacman ----------------------------------------------------------------------

# Arch Linux supports no partial upgrade: the first install syncs and upgrades
# the system, as the image is a week old at most.
pacman_prepare() {
    pacman_synced=
}

pacman_install() {
    if [ -z "$pacman_synced" ]; then
        pacman_synced=1
        pacman -Syu --noconfirm --needed "$@"
    else
        pacman -S --noconfirm --needed "$@"
    fi
}

pacman_available() {
    local package
    for package in "$@"; do
        if pacman -Si "$package" > /dev/null 2>&1; then
            echo "$package"
        fi
    done
}

# emerge ----------------------------------------------------------------------

# Portage builds from source: the packages come from the binary host of
# Gentoo the stage3 image is configured with (/etc/portage/binrepos.conf),
# built with the USE flags of its profile, and an atom it has no build of is
# left out. Binary packages merge in parallel.
emerge_options=(
    --getbinpkg --binpkg-respect-use=y --with-bdeps=n --noreplace --quiet --color=n
    "--jobs=$(nproc)"
)

# The image has no ebuild repository. The snapshot of it is what
# emerge-webrsync fetches, and taken as is, unpacked in place, it is ready in
# a third of the time. A container may not unshare, which the sandboxes of
# the builds do.
emerge_prepare() {
    local repo=/var/db/repos/gentoo snapshot=https://distfiles.gentoo.org/snapshots/gentoo-latest.tar.xz
    export FEATURES="${FEATURES:-} getbinpkg -ipc-sandbox -network-sandbox -pid-sandbox"
    MAKEOPTS="-j$(nproc)"
    export MAKEOPTS
    if [ -d "$repo/profiles" ]; then
        return
    fi
    local dir
    dir=$(mktemp -d)
    curl -fsSL --retry 3 -o "$dir/gentoo.tar.xz" "$snapshot" -o "$dir/gentoo.tar.xz.gpgsig" "$snapshot.gpgsig"
    GNUPGHOME=$dir gpg --batch --quiet --import /usr/share/openpgp-keys/gentoo-release.asc
    GNUPGHOME=$dir gpg --batch --quiet --verify "$dir/gentoo.tar.xz.gpgsig" "$dir/gentoo.tar.xz"
    mkdir -p "$repo"
    tar -xJf "$dir/gentoo.tar.xz" -C "$repo" --strip-components=1
    rm -rf "$dir"
}

emerge_install() {
    emerge "${emerge_options[@]}" --usepkgonly "$@"
}

# A dependency resolution takes seconds: all the atoms at once first.
emerge_available() {
    local atom
    if emerge --pretend "${emerge_options[@]}" --usepkgonly "$@" > /dev/null 2>&1; then
        printf '%s\n' "$@"
        return
    fi
    for atom in "$@"; do
        if emerge --pretend "${emerge_options[@]}" --usepkgonly "$atom" > /dev/null 2>&1; then
            echo "$atom"
        fi
    done
}

# The os level: Tarantool of the overlay of Tarantool, whose ebuilds are
# ~arch. The newest one of the major version of the series is taken, as old
# as it is, as Homebrew has its one. The overlay has no binary host, so
# Tarantool builds from source with the USE flags of the ebuild, in five
# minutes on five cores. With $ROCKS_PKGDIR set, the build is kept there as a
# binary package, which CI caches: a package of it is installed when its
# version and USE flags match, if it still runs with the libraries of the
# system.
overlay=/var/db/repos/tarantool
binpkgs_key=

emerge_tarantool_repository() {
    local uri=https://github.com/tarantool/gentoo-overlay.git
    step "Adding the overlay of Tarantool, $uri"
    mkdir -p /etc/portage/repos.conf /etc/portage/package.accept_keywords
    printf '[tarantool]\nlocation = %s\nsync-type = git\nsync-uri = %s\n' "$overlay" "$uri" \
        > /etc/portage/repos.conf/tarantool.conf
    echo "*/*::tarantool ~$(portageq envvar ARCH)" > /etc/portage/package.accept_keywords/tarantool
    git clone -q --depth 1 "$uri" "$overlay"
    git -C "$overlay" log -1 --format='%H %cd'
}

emerge_tarantool() {
    local atom="=dev-db/tarantool-${series%%.*}*" options=("${emerge_options[@]}" --usepkg)
    step "Installing $atom from the overlay${pkgdir:+, with the binary packages of $pkgdir}"
    if [ -n "$pkgdir" ]; then
        mkdir -p "$pkgdir"
        export PKGDIR=$pkgdir FEATURES="$FEATURES buildpkg"
    fi
    emerge "${options[@]}" "$atom"
    if ! tarantool --version > /dev/null; then
        echo "The tarantool of the binary package does not run: building it from source"
        emerge "${options[@]/--noreplace/--oneshot}" --usepkg-exclude dev-db/tarantool "$atom"
        tarantool --version > /dev/null
    fi
    if [ -n "$pkgdir" ]; then
        # The build, the overlay, and the libraries it was linked with.
        binpkgs_key=tarantool-gentoo-$(portageq best_version / dev-db/tarantool | sed 's,.*/tarantool-,,')
        binpkgs_key+=-$(git -C "$overlay" rev-parse --short=12 HEAD)
        binpkgs_key+=-$(ldd "$(command -v tarantool)" | awk '{ print $1 }' | sort | sha256sum | cut -c 1-12)
    fi
}

# -----------------------------------------------------------------------------

# The newest of the versions on the standard input, VERSION or
# VERSION-RELEASE, whose VERSION is of the series.
newest() {
    awk -v s="$series" '{ v = $0; sub(/-.*/, "", v) } v == s || index(v, s ".") == 1' |
        sort -V | tail -n 1
}

# The os level: on Gentoo the overlay of Tarantool, elsewhere the repository
# of download.tarantool.org, through the installer of tarantool.io. It takes Rocky Linux 8 for CentOS 8, and knows
# neither Rocky Linux 9 nor a Fedora after 38. For series 3 it configures one
# repository for every rpm distribution, linux-rpm/static, and os=almalinux
# makes it add that one alone: no repository of modules and no EPEL of its
# own, whose metadata cost a minute. The rpm of Fedora 43 and later refuses
# its packages, which carry no digest: rocks.yml has no os level of Fedora.
tarantool_repository() {
    local installer=(bash)
    case "$distro" in
        debian-* | ubuntu-*) ;;
        rocky-* | fedora-*) installer=(env os=almalinux "dist=${VERSION_ID%%.*}" bash) ;;
        gentoo*)
            emerge_tarantool_repository
            return
            ;;
        *)
            echo "$0: download.tarantool.org has no Tarantool for $distro" >&2
            exit 1
            ;;
    esac
    step "Adding the repository of Tarantool ${series%%.*} of download.tarantool.org"
    curl -fsSL --retry 3 "https://tarantool.io/release/${series%%.*}/installer.sh" |
        "${installer[@]}"
}

# The packages of the newest Tarantool of the series the repository has.
tarantool_packages() {
    local version
    case "$manager" in
        apt)
            version=$(apt-cache madison tarantool |
                awk -F' *[|] *' '/download\.tarantool\.org/ { print $2 }' | newest)
            ;;
        dnf)
            version=$(dnf "${dnf_options[@]}" repoquery --repo="tarantool_${series%%.*}" \
                --qf '%{version}-%{release}\n' tarantool | newest)
            ;;
    esac
    if [ -z "$version" ]; then
        echo "$0: download.tarantool.org has no Tarantool $series for $distro" >&2
        exit 1
    fi
    case "$manager" in
        apt) echo "tarantool=$version tarantool-dev=$version" ;;
        dnf) echo "tarantool-$version tarantool-devel-$version" ;;
    esac
}

case "$manager" in
    apt)
        toolchain=(
            ca-certificates curl gcc g++ libc6-dev make cmake git patch
            pkg-config tar unzip zip xz-utils libssl-dev zlib1g-dev
        )
        ;;
    # The images have curl, or curl-minimal, which conflicts with it.
    dnf)
        toolchain=(
            gcc gcc-c++ make cmake git patch pkgconf-pkg-config tar unzip zip
            xz findutils diffutils which openssl-devel zlib-devel
        )
        ;;
    # base-devel has the compilers, make, patch and pkgconf; base has curl,
    # OpenSSL and zlib.
    pacman) toolchain=(base-devel cmake git unzip zip) ;;
    # The stage3 has the compilers, make, patch, pkgconf, curl, unzip, OpenSSL
    # and zlib.
    emerge) toolchain=(dev-build/cmake dev-vcs/git app-arch/zip) ;;
esac

# The installer cleans the cache of dnf: it runs before anything fills it.
# On Debian and Ubuntu it needs the curl of the toolchain.
if [ "$level" = os ] && [ "$manager" = dnf ]; then
    tarantool_repository
fi
step "$distro: preparing $manager"
"${manager}_prepare"
step "Installing the toolchain: ${toolchain[*]}"
"${manager}_install" "${toolchain[@]}"
# sed reads to the end: head would leave the command a broken pipe, which
# pipefail takes for a failure.
cc --version | sed -n 1p
cmake --version | sed -n 1p
if [ "$level" = os ] && [ "$manager" != dnf ]; then
    tarantool_repository
fi

step "Installing Python $python_version and test/requirements.txt into $venv"
case "$(uname -m)" in
    x86_64) uv_sum=61fb26dabc4f0cf1d050fc6c67d40e43e4fae2994af21ebc1e050b233bf2b5ed ;;
    aarch64) uv_sum=0c6f8593fcf1eadfc60e663b685c408267559d7c5d61c689bbb9f3a7e369ba32 ;;
    *)
        echo "$0: no uv for $(uname -m)" >&2
        exit 1
        ;;
esac
uv_dir=$(mktemp -d)
curl -fsSL --retry 3 -o "$uv_dir/uv.tar.gz" \
    "https://github.com/astral-sh/uv/releases/download/$uv_version/uv-$(uname -m)-unknown-linux-musl.tar.gz"
echo "$uv_sum  $uv_dir/uv.tar.gz" | sha256sum -c --quiet -
tar -xzf "$uv_dir/uv.tar.gz" -C "$uv_dir" --strip-components=1
uv=$uv_dir/uv
"$uv" venv --managed-python --python "$python_version" "$venv"
"$uv" pip install --python "$venv/bin/python" -r test/requirements.txt
rm -rf "$uv_dir"
python=$venv/bin/python

read -r -a libraries <<< "$("$python" test/ci.py rocks-packages "$manager")"
available=()
missing=()
if [ ${#libraries[@]} -gt 0 ]; then
    mapfile -t found < <("${manager}_available" "${libraries[@]}")
    for package in "${libraries[@]}"; do
        if printf '%s\n' "${found[@]}" | grep -qxF -- "$package"; then
            available+=("$package")
        else
            missing+=("$package")
        fi
    done
fi
# At os, the packages of Tarantool come with them, but on Gentoo.
if [ "$level" = os ] && [ "$manager" != emerge ]; then
    packages=$(tarantool_packages)
    read -r -a packages <<< "$packages"
    available+=("${packages[@]}")
fi
step "Installing the libraries of the rocks: ${available[*]}"
if [ ${#available[@]} -gt 0 ]; then
    "${manager}_install" "${available[@]}"
fi
if [ ${#missing[@]} -gt 0 ]; then
    echo "$distro has no package of: ${missing[*]}"
fi
if [ "$level" = os ] && [ "$manager" = emerge ]; then
    emerge_tarantool
fi

root=
template=
case "$level" in
    project | user | system)
        version=$("$tt" binary search tarantool | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | newest)
        if [ -z "$version" ]; then
            echo "$0: no Tarantool release matches $series" >&2
            exit 1
        fi
        step "Installing Tarantool $version through tt into the $level scope"
        # The default roots, as root: the user one is under $HOME, which is
        # /github/home in a container of GitHub Actions.
        dir=$(mktemp -d)
        case "$level" in
            project)
                template=$dir
                root=$dir/.tarantool
                ;;
            user)
                case "${XDG_DATA_HOME:-}" in
                    /*) root=$XDG_DATA_HOME/tarantool ;;
                    *) root=$HOME/.local/share/tarantool ;;
                esac
                ;;
            system) root=/opt/tarantool ;;
        esac
        # The download from rws.tarantool.org times out now and then ("dial
        # tcp ...:443: i/o timeout"): it is tried three times.
        for attempt in 1 2 3; do
            if (cd "$dir" && "$tt" binary install tarantool "$version" --scope "$level"); then
                break
            fi
            if [ "$attempt" = 3 ]; then
                exit 1
            fi
            echo "$0: tt binary install failed (attempt $attempt of 3), trying again" >&2
            sleep 10
        done
        tarantool=$root/bin/tarantool
        header=$root/include/tarantool/module.h
        ;;
    os)
        tarantool=$(command -v tarantool)
        header=/usr/include/tarantool/module.h
        ;;
esac
"$tarantool" --version | sed -n 1p
ls -l "$header"

step "Done"
{
    echo "python=$python"
    echo "root=$root"
    echo "template=$template"
    echo "tarantool=$tarantool"
    echo "binpkgs-key=$binpkgs_key"
} | tee -a "${GITHUB_OUTPUT:-/dev/null}"
