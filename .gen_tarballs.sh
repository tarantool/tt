#!/bin/bash
set -u

# This script generates tarballs needed to create the Gentoo ebuilds.

die() {
    echo "$*" >&2
    exit 2
}

needs_arg() {
    if [ -z "$OPTARG" ]; then
        die "No arg for --${OPT} option"
    fi
}

TAG=""

while getopts ht:-: OPT; do
    if [ "$OPT" = "-" ]; then
        OPT="${OPTARG%%=*}"
        OPTARG="${OPTARG#$OPT}"
        OPTARG="${OPTARG#=}"
    fi

    case "$OPT" in
        (h | help)
            echo "Usage: ${0} -t <tag>"
            exit 0
            ;;
        (t | tag)
            needs_arg; TAG="$OPTARG"
            ;;
        (??*)
            die "Illegal option --$OPT"
            ;;
        (?)
            exit 1
            ;;
    esac
done

if [ $# == 0 ]; then
    die "Usage: ${0} -t <tag>"
fi

TAG_ORIG=${TAG}
TAG=${TAG//v}
TMPDIR="/tmp/gentoo_tarballs"
TT_DIR="${TMPDIR}/tt-${TAG}"

# Cleanup.
test -d $TMPDIR && rm -rf $TMPDIR
mkdir -p $TMPDIR/go-mod

echo -n "* Download sources... "
git clone https://github.com/tarantool/tt.git -b $TAG_ORIG --depth=1 --recursive $TT_DIR \
    > /dev/null 2>${TMPDIR}/log.txt
if [ "$?" != 0 ]; then
    echo "Err: "
    cat ${TMPDIR}/log.txt
    exit 1
else
    echo "Done"
fi

echo -n "* Create complete sources tarball... "
pushd ${TMPDIR} > /dev/null
tar czf tt-${TAG}-complete.tar.gz tt-${TAG}
if [ "$?" != 0 ]; then
    echo "Err"
    exit 1
else
    echo "Done"
fi
echo "* TT sources tarball: $(realpath tt-${TAG}-complete.tar.gz)"

echo -n "* Create dependency tarball... "
pushd ${TT_DIR} > /dev/null
GOMODCACHE=${TMPDIR}/go-mod go mod download -modcacherw
rc=$?
pushd ${TT_DIR}/cli/cartridge/third_party/cartridge-cli > /dev/null
GOMODCACHE=${TMPDIR}/go-mod go mod download -modcacherw
let rc+=$?

popd > /dev/null
popd > /dev/null
tar --create --auto-compress --file tt-${TAG}-deps.tar.xz go-mod
let rc+=$?
if [ $rc != 0 ]; then
    echo "Err"
    exit 1
else
    echo "Done"
fi
echo "* TT deps tarball: $(realpath tt-${TAG}-deps.tar.xz)"

popd > /dev/null
