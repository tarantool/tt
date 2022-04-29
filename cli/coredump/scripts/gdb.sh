#!/bin/sh
set -eu

FOLDERPATH=$COREFOLDER_ENV

# Check that gdb is installed.
if ! command -v gdb >/dev/null; then
	cat <<NOGDB
gdb is not installed or not found in the PATH.

Install gdb or adjust you PATH if you are using non-system gdb and
try once more.
NOGDB
	exit 1;
fi

VERSION=${FOLDERPATH}/version

# Check the location: if the coredump artefacts are collected via
# `tarabrt.sh' there should be /version file in the root of the
# unpacked tarball. Otherwise, there is no guarantee the coredump
# is collected the right way and we can't proceed loading it.
if [ ! -f "${VERSION}" ]; then
	cat <<NOARTEFACTS
${VERSION} file is missing.

If the coredump artefacts are collected via \`tararbrt.sh' tool
there should be /version file in the root of the unpacked tarball
(i.e. ${PWD}).
If version file is missing, there is no guarantee the coredump
is collected the right way and its loading can't be proceeded
with this script. Check whether current working directory is the
tarball root, or try load the core dump file manually.
NOARTEFACTS
	exit 1;
fi

SOURCES=${FOLDERPATH}/sources

# Check whether Tarantool sources are setup. Otherwise, leave a
# recipe for user, how to do it.
# FIXME: This can be done automatically (simply run the commands
# below), but this obliges git to be installed. For now, it makes
# the wrapper more complex by additional checks, so this activity
# is left for the user.
if [ ! -d "${SOURCES}" ]; then
	REGEX='Tarantool \d+\.\d+\.\d+(-(alpha|beta|rc)[0-9]+|(-entrypoint))?-\d+-g\K[a-f0-9]+'
    REVISION=$(grep -oP "$REGEX" "$VERSION")
	cat <<SOURCES
================================================================================

Do not forget to properly setup the environment:
* git clone https://github.com/tarantool/tarantool.git ${SOURCES}
* cd !$
* git checkout ${REVISION}
* git submodule update --recursive --init
* cd -

================================================================================
SOURCES
	exit 1;
fi

# Define the build path to be substituted with the source path.
# XXX: Check the absolute path on the function <main> definition
# considering it is located in src/main.cc within Tarantool repo.
SUBPATH=$(gdb -batch -n ${FOLDERPATH}/tarantool -ex 'info line main' | \
	grep -oP 'Line \d+ of \"\K.+(?=\/src\/main\.cc\")')

# Launch gdb and load coredump with all related artefacts.
gdb ${FOLDERPATH}/tarantool \
    -ex "set sysroot ${FOLDERPATH}" \
    -ex "set substitute-path ${SUBPATH} sources" \
    -ex "add-auto-load-safe-path ${FOLDERPATH}" \
    -ex "set auto-load libthread-db on" \
    -ex "core ${FOLDERPATH}/coredump"
