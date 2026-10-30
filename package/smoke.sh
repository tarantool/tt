#!/usr/bin/env bash
# Run inside a fresh Ubuntu or Rocky Linux container with packages at /packages.
#
# Install both amd64 variants through apt or dnf from a local repository, the
# way users get them from RWS. The variants must never be installed together,
# and the package manager must not swap one for the other unless asked to.
set -euo pipefail
shopt -s nullglob

format=${1:?package format is required}
repo=/tmp/repo
mkdir -p "$repo"

case "$format" in
    deb)
        default=(/packages/tt_*_amd64.deb)
        openssl=(/packages/tt-openssl_*_amd64.deb)
        ;;
    rpm)
        default=()
        openssl=(/packages/tt-openssl-*.x86_64.rpm)
        for package in /packages/tt-*.x86_64.rpm; do
            case "$package" in
                /packages/tt-openssl-*) ;;
                *) default+=("$package") ;;
            esac
        done
        ;;
    *) exit 2 ;;
esac
[ "${#default[@]}" -eq 1 ] && [ "${#openssl[@]}" -eq 1 ]
cp "${default[0]}" "${openssl[0]}" "$repo/"

# Check the relations nfpm wrote: deb packages conflict with and replace each
# other; rpm packages only conflict, as Obsoletes would let dnf swap them.
for package in "${default[0]}" "${openssl[0]}"; do
    case "$package" in
        *tt-openssl*) other=tt ;;
        *) other=tt-openssl ;;
    esac
    if [ "$format" = deb ]; then
        dpkg-deb -f "$package" Conflicts | grep -Fx "$other"
        dpkg-deb -f "$package" Replaces | grep -Fx "$other"
    else
        rpm -qp --conflicts "$package" | grep -Fx "$other"
        if rpm -qp --obsoletes "$package" | grep .; then
            echo "$package must not obsolete another package" >&2
            exit 1
        fi
    fi
done
if [ "$format" = deb ]; then
    dpkg-deb -f "${openssl[0]}" Provides | grep -Fx tt
else
    rpm -qp --provides "${openssl[0]}" | grep -Fx tt
fi

if [ "$format" = deb ]; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends dpkg-dev
    (cd "$repo" && dpkg-scanpackages --multiversion . > Packages)
    echo "deb [trusted=yes] file:$repo ./" > /etc/apt/sources.list.d/local.list
    apt-get update
    pm() {
        case "$1" in
            install) shift; apt-get install -y --no-install-recommends "$@" ;;
            upgrade) apt-get full-upgrade -y ;;
            remove) shift; apt-get remove -y "$@" ;;
        esac
    }
    installed() {
        dpkg-query -W -f='${Status}\n' "$1" 2>/dev/null | grep -qx 'install ok installed'
    }
else
    dnf install -y createrepo_c
    createrepo_c "$repo"
    printf '[local]\nname=local\nbaseurl=file://%s\ngpgcheck=0\n' "$repo" \
        > /etc/yum.repos.d/local.repo
    pm() {
        case "$1" in
            install) shift; dnf install -y --disablerepo='*' --enablerepo=local "$@" ;;
            upgrade) dnf upgrade -y --disablerepo='*' --enablerepo=local ;;
            remove) shift; dnf remove -y "$@" ;;
        esac
    }
    installed() {
        rpm -q "$1" >/dev/null 2>&1
    }
fi

# only NAME: NAME is installed and the other variant is not.
only() {
    local other=tt-openssl
    [ "$1" = tt ] || other=tt
    if ! installed "$1" || installed "$other"; then
        echo "Expected only $1 to be installed" >&2
        exit 1
    fi
}

check_cli() {
    tt version
    tt help >/dev/null
    tt completion bash >/tmp/completion.bash
    test -s /tmp/completion.bash
    test -s /usr/share/bash-completion/completions/tt
    test -s /usr/share/zsh/vendor-completions/_tt
    test -s /usr/share/fish/completions/tt.fish
    id tarantool
    test ! -e /etc/tarantool/tt.yaml
    test ! -d /var/lib/tarantool/sys_env
}

# A plain install picks the package by its name, and an upgrade keeps it.
pm install tt
only tt
check_cli
pm upgrade
only tt

if [ "$format" = deb ]; then
    # Conflicts and Replaces let apt replace the installed variant.
    pm install tt-openssl
else
    # Without Obsoletes, dnf refuses the conflicting variant unless asked
    # to swap it.
    if pm install tt-openssl; then
        echo 'dnf installed tt-openssl over tt without a swap' >&2
        exit 1
    fi
    only tt
    dnf swap -y --disablerepo='*' --enablerepo=local tt tt-openssl
fi
only tt-openssl
check_cli
pm upgrade
only tt-openssl

pm remove tt-openssl
if installed tt || installed tt-openssl; then
    echo 'Packages remain after removal' >&2
    exit 1
fi

# A plain install of the OpenSSL variant gets it, not the default one.
pm install tt-openssl
only tt-openssl
pm remove tt-openssl
