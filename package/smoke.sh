#!/usr/bin/env bash
# Run inside a fresh Ubuntu or Rocky Linux container with packages at /packages.
set -euo pipefail
shopt -s nullglob

format=${1:?package format is required}
case "$format" in
    deb) default=(/packages/tt_*_amd64.deb); openssl=(/packages/tt-openssl_*_amd64.deb) ;;
    rpm) default=(); openssl=(/packages/tt-openssl-*.x86_64.rpm)
         for package in /packages/tt-*.x86_64.rpm; do
             case "$package" in
                 /packages/tt-openssl-*) ;;
                 *) default+=("$package") ;;
             esac
         done ;;
    *) exit 2 ;;
esac
[ "${#default[@]}" -eq 1 ] && [ "${#openssl[@]}" -eq 1 ]

installed() {
    if [ "$format" = deb ]; then
        dpkg-query -W -f='${Status}\n' "$1" 2>/dev/null | grep -Eq '^install ok (installed|unpacked)$'
    else
        rpm -q "$1" >/dev/null 2>&1
    fi
}

install() {
    if [ "$format" = deb ]; then dpkg -i "$1"; else rpm -i "$1"; fi
}

remove() {
    for name in tt tt-openssl; do
        if installed "$name"; then
            if [ "$format" = deb ]; then dpkg -r "$name"; else rpm -e "$name"; fi
        fi
    done
}

for index in 0 1; do
    packages=("${default[0]}" "${openssl[0]}")
    names=(tt tt-openssl)
    package=${packages[$index]}
    name=${names[$index]}
    other=${names[$((1-index))]}
    if [ "$format" = deb ]; then
        dpkg-deb -f "$package" Conflicts | grep -Fx "$other"
        dpkg-deb -f "$package" Replaces | grep -Fx "$other"
        if [ "$name" = tt-openssl ]; then dpkg-deb -f "$package" Provides | grep -Fx tt; fi
    else
        rpm -qp --conflicts "$package" | grep -Fx "$other"
        rpm -qp --obsoletes "$package" | grep -Fx "$other"
        if [ "$name" = tt-openssl ]; then rpm -qp --provides "$package" | grep -Fx tt; fi
    fi
    install "$package"
    installed "$name"
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

    # dpkg can replace the first package because Conflicts and Replaces are
    # both declared. Either replacement or rejection must prevent coexistence.
    if install "${packages[$((1-index))]}"; then
        echo "Package manager replaced $name with $other"
    else
        echo "Package manager rejected co-installation"
    fi
    if installed tt && installed tt-openssl; then
        echo 'Conflicting packages were installed together' >&2
        exit 1
    fi
    remove
done
