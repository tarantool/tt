#!/bin/sh
set -eu

TOOL=$(basename "$0")
WORKDIR=$(dirname "$0")
EXTS=

HELP=$(cat <<HELP
${TOOL} - gdb wrapper loading artefacts collected via tarabrt.sh

SYNOPSIS
  $0 [-h] [-d dir] [-s dir] [-x ext]...

Supported options are:
  -d DIRECTORY                  Load the artefacts from DIRECTORY, the
                                unpacked archive (default: the directory
                                of ${TOOL}).
  -s DIRECTORY                  Set mapping to source.
  -x EXT                        Load GDB-extension EXT. This option may
                                appear multiple times. Without it, the
                                extensions in ext/ of the artefacts are
                                loaded.
  -h                            Shows this message and exit.

HELP
)

# Parse CLI options.
OPTIONS=$(getopt -o d:hs:x: -n "${TOOL}" -- "$@")
eval set -- "${OPTIONS}"
while true; do
	case "$1" in
		--) shift; break;;
		-d) WORKDIR=$2; shift 2;;
		-s) SOURCES=$2; shift 2;;
		-x) EXTS="${EXTS}$2
"; shift 2;;
		-h) printf "%s\n" "${HELP}";
			exit 0;;
		*)  printf "Invalid option: $1\n%s\n" "${HELP}";
			exit 1;;
	esac
done

# Do not proceed if there are some CLI arguments left. Everything
# should be parsed before this line.
if [ $# -ne 0 ]; then
	printf "Invalid argument: $1\n%s\n" "${HELP}"
	exit 1
fi

# Check that gdb is installed.
if ! command -v gdb >/dev/null; then
	cat <<NOGDB
gdb is not installed or not found in the PATH.

Install gdb or adjust you PATH if you are using non-system gdb and
try once more.
NOGDB
	exit 1;
fi

VERSION=${WORKDIR}/version

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

SOURCES=${SOURCES:-${WORKDIR}/sources}

# Check whether Tarantool sources are setup. Otherwise, leave a
# recipe for user, how to do it.
# FIXME: This can be done automatically (simply run the commands
# below), but this obliges git to be installed. For now, it makes
# the wrapper more complex by additional checks, so this activity
# is left for the user.
if [ ! -d "${SOURCES}" ]; then
	REGEX='Tarantool( Enterprise)? \d+\.\d+\.\d+(-(alpha|beta|rc)[0-9]+|(-entrypoint))?-\d+-g\K[a-f0-9]+'
	REVISION=$(grep -oP "$REGEX" "$VERSION" || true)
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
fi

ABSWORKDIR=$(realpath "$WORKDIR")

# The gdb extensions to load: those given with -x, or those of the
# artefacts.
if [ -z "${EXTS}" ]; then
	for EXT in "${ABSWORKDIR}"/ext/*.py; do
		if [ -f "${EXT}" ]; then
			EXTS="${EXTS}${EXT}
"
		fi
	done
fi

# The separate debugging information of the tarantool, which pack
# collects where a debuginfo package installs it, under the build ID of
# the tarantool in usr/lib/debug of the artefacts. gdb looks for it
# there first, then where it looks by default; the directory is set
# before the tarantool is loaded (-iex). Without it the command is
# echo, which prints nothing.
DEBUGFILEDIR="echo"
if [ -d "${ABSWORKDIR}/usr/lib/debug" ]; then
	DEFAULTDEBUGDIR=$(gdb -batch -n -ex 'show debug-file-directory' 2>/dev/null | \
		sed -n 's/^.*"\(.*\)"\.$/\1/p')
	DEBUGFILEDIR="set debug-file-directory ${ABSWORKDIR}/usr/lib/debug"
	if [ -n "${DEFAULTDEBUGDIR}" ]; then
		DEBUGFILEDIR="${DEBUGFILEDIR}:${DEFAULTDEBUGDIR}"
	fi
fi

# The arguments of gdb are collected in the positional parameters,
# which hold no option any longer.
set --

# Define the build path to be substituted with the source path.
# XXX: Check the absolute path on the function <main> definition
# considering it is located in src/main.cc within Tarantool repo.
# A tarantool without debugging information has no line of main:
# gdb is run without the mapping then.
SUBPATH=$(gdb -batch -n -iex "${DEBUGFILEDIR}" "${ABSWORKDIR}/tarantool" \
	-ex 'info line main' | \
	grep -oP 'Line \d+ of \"\K.+(?=\/src\/main\.cc\")' || true)
if [ -n "${SUBPATH}" ]; then
	set -- "$@" -ex "set substitute-path ${SUBPATH} ${SOURCES}"
fi

# The extensions are sourced explicitly once the core is loaded:
# nothing in the archive makes gdb auto-load them. tarantool-gdb.py
# uses gdb.printing without importing it, so it is imported first.
if [ -n "${EXTS}" ]; then
	set -- "$@" -ex "python import gdb.printing"
	set -f
	OLDIFS=${IFS}
	IFS='
'
	for EXT in ${EXTS}; do
		set -- "$@" -ex "source ${EXT}"
	done
	IFS=${OLDIFS}
	set +f
fi

# Launch gdb and load coredump with all related artefacts.
#
# gdb loads libthread_db when it loads the core. The one of the
# archive, packed next to the thread library of the core, is $pdir
# under the sysroot, and loads from the archive, which the safe-path
# allows; that of the host is $sdir. It is looked for in the archive
# first: gdb searches "$sdir:$pdir" by default, the directories of
# the host first, and took the one of the host whenever it matched
# the core, as on the host that packed it, and the one of the archive
# went untried.
exec gdb -iex "${DEBUGFILEDIR}" "${ABSWORKDIR}/tarantool" \
	-ex "set sysroot ${ABSWORKDIR}" \
	-ex "add-auto-load-safe-path ${ABSWORKDIR}" \
	-ex "set auto-load libthread-db on" \
	-ex 'set libthread-db-search-path $pdir:$sdir' \
	-ex "core ${ABSWORKDIR}/coredump" \
	"$@"
