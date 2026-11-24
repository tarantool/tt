#!/bin/bash
#
# Check that linux amd64 tt binaries run on any x86-64 CPU.
#
# Usage: ci/check-amd64-baseline.sh TT_BINARY...
#
# The release binaries are linked statically, so they carry the glibc
# and the OpenSSL of the build host. When those are built for a newer
# x86-64 level, as the amd64v3 packages of Ubuntu 26.04 are, tt dies
# with SIGILL before main on a CPU without AVX2 (#1475). A binary
# fails the check when:
#
# 1. the "x86 ISA needed" note of `readelf -n` names x86-64-v2, v3
#    or v4;
# 2. an OpenSSL "compiler:" string in it has a -march= other than
#    plain x86-64. Not every OpenSSL leaves this string in the binary,
#    its absence is only reported;
# 3. `tt version` does not exit with zero under qemu-x86_64 for a CPU
#    model in BASELINE_CPUS (default: "qemu64 Nehalem"): qemu64 is the
#    baseline x86-64, Nehalem is x86-64-v2 without AVX.
#
# The first two checks name the cause, but neither is enough alone: a
# static link with libcrypto.a of the amd64v3 variant still gets the
# baseline note and runs BMI2 instructions. The run under qemu is the
# proof.

set -euo pipefail

cpus=${BASELINE_CPUS:-qemu64 Nehalem}
failures=0

ok() {
    printf 'ok:   %s\n' "$*"
}

note() {
    printf 'note: %s\n' "$*"
}

fail() {
    printf 'FAIL: %s\n' "$*"
    failures=$((failures + 1))
}

check_isa_note() {
    local bin=$1 notes line

    notes=$(readelf -n "$bin" | { grep 'x86 ISA needed:' || true; })
    if [ -z "$notes" ]; then
        note "readelf -n: no \"x86 ISA needed\" note"
        return
    fi
    while IFS= read -r line; do
        line=${line#*x86 ISA needed: }
        if [[ $line =~ x86-64-v[234] ]]; then
            fail "readelf -n: x86 ISA needed: $line"
        else
            ok "readelf -n: x86 ISA needed: $line"
        fi
    done <<<"$notes"
}

check_openssl_march() {
    local bin=$1 compilers line word
    local -a words marches bad

    compilers=$(strings -a "$bin" | { grep '^compiler: ' || true; })
    if [ -z "$compilers" ]; then
        note "strings: no OpenSSL \"compiler:\" string, -march not checked"
        return
    fi
    while IFS= read -r line; do
        read -r -a words <<<"$line"
        marches=()
        bad=()
        for word in "${words[@]}"; do
            case $word in
            -march=x86-64)
                marches+=("$word")
                ;;
            -march=*)
                marches+=("$word")
                bad+=("$word")
                ;;
            esac
        done
        if [ ${#bad[@]} -ne 0 ]; then
            fail "OpenSSL compiler: ${bad[*]}, only -march=x86-64 runs on any CPU"
            printf '      %s\n' "$line"
        elif [ ${#marches[@]} -ne 0 ]; then
            ok "OpenSSL compiler: ${marches[*]}"
        else
            ok "OpenSSL compiler: no -march"
        fi
    done <<<"$compilers"
}

check_qemu() {
    local bin=$1 cpu out rc reason
    local -a cpu_list

    read -r -a cpu_list <<<"$cpus"
    for cpu in "${cpu_list[@]}"; do
        rc=0
        out=$(qemu-x86_64 -cpu "$cpu" "$bin" version 2>&1) || rc=$?
        if [ "$rc" -eq 0 ]; then
            ok "qemu-x86_64 -cpu $cpu: tt version: ${out%%$'\n'*}"
            continue
        fi
        reason="exit status $rc"
        if [ "$rc" -gt 128 ]; then
            reason="killed by SIG$(kill -l $((rc - 128)))"
        fi
        fail "qemu-x86_64 -cpu $cpu: tt version: $reason"
        if [ -n "$out" ]; then
            printf '%s\n' "$out" | tail -n 5 | sed 's/^/      /'
        fi
    done
}

if [ $# -eq 0 ]; then
    echo "usage: $0 TT_BINARY..." >&2
    exit 2
fi

missing=()
for tool in readelf strings qemu-x86_64; do
    command -v "$tool" >/dev/null || missing+=("$tool")
done
if [ ${#missing[@]} -ne 0 ]; then
    echo "error: not found in PATH: ${missing[*]}" >&2
    echo "error: readelf and strings come with binutils, qemu-x86_64 with qemu-user" >&2
    exit 2
fi

# A binary killed by SIGILL under qemu must not leave a core behind.
ulimit -c 0

for bin in "$@"; do
    echo "== $bin"
    if [ ! -f "$bin" ] || [ ! -r "$bin" ]; then
        fail "not a readable file"
        continue
    fi
    check_isa_note "$bin"
    check_openssl_march "$bin"
    check_qemu "$bin"
done

if [ "$failures" -ne 0 ]; then
    echo "error: $failures check(s) failed, see FAIL above" >&2
    exit 1
fi
echo "all checks passed"
