#!/bin/sh
set -eu

TOOL=$(basename "$0")
HELP=$(cat <<HELP
$0 - Tarantool Automatic Bug Reporting Tool

This tool collects all required artefacts (listed below) and packs them into
a single archive with unified format:
  - /checklist      - the plain text file with the list of tarball contents
  - /version        - the plain text file with \`tarantool --version' output
  - /tarantool      - the executable binary file produced the core dump
  - /coredump       - the core dump file produced by the executable
  - /etc/os-release - the plain text file with OS identification data
  - all shared libraries loaded (even via dlopen(3)) at the crash moment
  - /usr/lib/debug/.build-id/XX/YYYY.debug - the separate debugging
                      information of the executable, when the host has it

SYNOPSIS

  $0 [-h] [-c core] [-d dir] [-e executable] [-p procID] [-t datetime]

Supported options are:
  -c COREDUMP                   Use file COREDUMP as a core dump to examine.
                                See core(5) for more info.

  -d DIRECTORY                  Create the resulting archive with the artefacts
                                within DIRECTORY.

  -e TARANTOOL                  Use file TARANTOOL as the executable file for
                                examining with a core dump COREDUMP. By default
                                tarantool executable path is obtained from PATH.
                                If PID is specified, the one from /proc/PID/exe
                                is chosen (see proc(5) for more info).

  -g GDBWRAPPER                 Include GDB-wrapper script GDBWRAPPER into the
                                archive.

  -p PID                        PID of the dumped process, as seen in the PID
                                namespace in which the given process resides
                                (see %p in core(5) for more info). This flag
                                have to be set when ${TOOL} is used as
                                kernel.core_pattern pipeline script.

  -t DATETIME                   Time of dump, expressed as seconds since the
                                epoch, 1970-01-01 00:00:00 +0000 (UTC).

  -x EXT                        Include GDB-extension EXT into the archive.
                                This option may appear multiple times.

  -h                            Shows this message and exit.

USAGE

  - Manual usage. User can simply pack all necessary artefacts by running the
    following command.
    $ $0 -c ./core -d /tmp

  - Automatic usage. If user faces the failures often, one can set this script
    as a pipe receiver in kernel.core_pattern syntax.
    # sysctl -w kernel.core_pattern="|$0 -d /var/core -p %p -t %t"

HELP
)

# Assign configurable parameters with the default values.
BINARY=
COREDIR=${PWD}
COREFILE=
EXTS=
GDB=
PID=
TIME=$(date +%s)

# Parse CLI options.
OPTIONS=$(getopt -o c:d:e:g:hp:t:x: -n "${TOOL}" -- "$@")
eval set -- "${OPTIONS}"
while true; do
	case "$1" in
		--) shift; break;;
		-c) COREFILE=$2; shift 2;;
		-d) COREDIR=$2;  shift 2;;
		-e) BINARY=$2;   shift 2;;
		-g) GDB=$2;      shift 2;;
		-p) PID=$2;      shift 2;;
		-t) TIME=$2;     shift 2;;
		-x) EXTS=$2:$EXTS; shift 2;;
		-h) printf "%s\n" "${HELP}";
			exit 0;;
		*)  printf "Invalid option: $1\n%s\n" "${HELP}";
			exit 1;;
	esac
done

if [ -z "${BINARY}" ]; then
	if ! which tarantool; then
		[ -t 1 ] && cat <<NOEXEC
There is no Tarantool executable found in PATH. Either make sure
it is there or specify it explicitly with the '-e' option.
NOEXEC
	fi
	BINARY=$(which tarantool)
fi

# Do not proceed if there are some CLI arguments left. Everything
# should be parsed before this line.
if [ $# -ne 0 ]; then
	printf "Invalid argument: $1\n%s\n" "${HELP}";
	exit 1;
fi

# Print the path $1 as an absolute one, resolved against the working
# directory. The artefacts are packed through symbolic links to them
# (see stage below), and a relative target would be resolved against
# the directory of the link instead.
abspath() {
	case "$1" in
		/*) printf '%s\n' "$1";;
		*)  printf '%s/%s\n' "${PWD}" "$1";;
	esac
}

COREDIR=$(abspath "${COREDIR}")
BINARY=$(abspath "${BINARY}")
if [ -n "${COREFILE}" ]; then
	COREFILE=$(abspath "${COREFILE}")
fi
if [ -n "${GDB}" ]; then
	GDB=$(abspath "${GDB}")
fi

# XXX: This section handles the case when the script is used for
# kernel.core_pattern. If PID is set and there is a directory in
# procfs with this PID, the script processes the core dumped by
# this process. If the process exe (or strictly saying its comm)
# is not 'tarantool' then the coredump is simply saved to the
# COREDIR; otherwise the dumped core is packed to the tarball.
if [ -n "${PID}" ] && [ -d /proc/"${PID}" ]; then
	BINARY=$(readlink /proc/"${PID}"/exe)
	CMDNAME=$(sed -z 's/\s$//' /proc/"${PID}"/comm)
	COREFILE=${COREDIR}/${CMDNAME}-core.${PID}.${TIME}
	cat >"${COREFILE}"
	if [ "${CMDNAME}" != 'tarantool' ]; then
		[ -t 1 ] && cat <<ALIENCOREDUMP
/proc/${PID}/comm doesn't equal to 'tarantool', so we assume the
obtained core is dumped by \`${CMDNAME}' and should be packed in
a different way. As a result it is simply stored to the file, so
you can process it on your own.

The file with core dump: ${COREFILE}
ALIENCOREDUMP
		exit 0;
	fi
fi

if [ -z "${COREFILE}" ]; then
	[ -t 1 ] && cat <<NOCOREDUMP
There is no core dump file passed to ${TOOL}. The artefacts can't
be collected. If you see this message, check the usage by running
\`${TOOL} -h': -c option is the obligatory one.
NOCOREDUMP
	exit 1;
fi

if ! command -v file >/dev/null; then
	[ -t 1 ] && cat <<NOFILE
file is not installed, but it is obligatory for checking the
artefacts to be collected.

You can proceed collecting the artefacts manually later by running
the following command:
$ ${TOOL} -e ${BINARY} -c ${COREFILE}
NOFILE
fi

if file "${COREFILE}" | grep -qv 'core file'; then
	[ -t 1 ] && cat <<NOTACOREDUMP
Not a core dump: ${COREFILE}

The given COREDUMP file is not a valid core dump (see core(5) for
more info) or not even an ELF (see elf(5) for more info). If you
see this message, check the COREDUMP file the following way:
$ file ${COREFILE}
NOTACOREDUMP
	exit 1;
fi

# Check that gdb is installed.
if ! command -v gdb >/dev/null; then
	[ -t 1 ] && cat <<NOGDB
gdb is not installed, but it is obligatory for collecting the
loaded shared libraries from the core dump.

You can proceed collecting the artefacts manually later by running
the following command:
$ ${TOOL} -e ${BINARY} -c ${COREFILE}
NOGDB
	exit 1;
fi

# XXX: There is annoying bug in file(1), making it claim that PIE
# is a shared object, but not an executable one. Strictly saying,
# there was no PIE support until 5.34 at all, but the whole thing
# didn't work until 5.36. Unfortunately, there are distros (e.g.
# CentOS 8) providing file utility without valid PIE support, but
# compiling PIE binaries. As a result we ought to respect both
# behaviours for the check below. For more info, see the highly
# detailed answer on Stack Overflow below.
# https://stackoverflow.com/a/55704865/4609359
if file -L "${BINARY}" | grep -qvE 'executable|shared object'; then
	[ -t 1 ] && cat <<NOTELF
Not an ELF file: ${BINARY}

The given BINARY file is not an ELF (see elf(5) for more info).
If you see this message, check the BINARY file the following way:
$ file ${BINARY}
NOTELF
	exit 1;
fi

if gdb -batch -n "${BINARY}" -ex 'info symbol tarantool_version' 2>/dev/null | \
	grep -q 'tarantool_version in section .text'
then
	# XXX: This is a very ugly hack to implement 'unless'
	# operator in bash for a long pipeline as a conditional.
	:
else
	[ -t 1 ] && cat <<NOTARANTOOL
Not a Tarantool binary: ${BINARY}

The given BINARY file is not a Tarantool executable: there is no
signature symbol in the binary file. If you see this message,
check the BINARY file the following way:
$ ${BINARY} --help
NOTARANTOOL
	exit 1;
fi

# Resolve the host name if possible.
HOSTNAME=$(hostname 2>/dev/null || echo hostname)

# Proceed with collecting and packing artefacts.
TMPDIR=$(mktemp -d -p "${COREDIR}")
TARLIST=${TMPDIR}/tarlist
LIBLIST=${TMPDIR}/liblist
OSRELEASE=/etc/os-release
ARCHIVENAME=tarantool-core-${PID:-N}-$(date +%Y%m%d%H%M -d @"${TIME}")-${HOSTNAME%%.*}
ARCHIVE=${COREDIR}/${ARCHIVENAME}.tar.gz
# The archive is packed from STAGE, its root: every artefact is a
# symbolic link there, under the name it has in the archive, which tar
# follows (-h). The names are made here rather than by --transform of
# the paths of the artefacts, so no path can rename another member:
# the expression of a relative core path, `core', matched inside
# every path with "core" in it, as /var/lib/systemd/coredump.
STAGE=${TMPDIR}/${ARCHIVENAME}
VERSION=${STAGE}/version

mkdir "${STAGE}"

# stage NAME FILE puts FILE into the archive as NAME, a path within
# the root of the archive. A name staged already is skipped, so a file
# listed twice is packed once.
stage() {
	if [ -e "${STAGE}/$1" ] || [ -L "${STAGE}/$1" ]; then
		return 0
	fi
	mkdir -p "$(dirname "${STAGE}/$1")"
	ln -s "$2" "${STAGE}/$1"
	echo "${ARCHIVENAME}/$1" >>"${TARLIST}"
}

# Dump the version to checkout the right commit later.
"${BINARY}" --version >"${VERSION}"
echo "${ARCHIVENAME}/version" >>"${TARLIST}"

# Collect the most important artefacts.
stage "${OSRELEASE#/}" "${OSRELEASE}"
stage tarantool "${BINARY}"
stage coredump "${COREFILE}"

# The separate debugging information of the binary, when the host has
# it: a debuginfo package installs it under a debug directory of gdb,
# /usr/lib/debug by default, by the build ID of the binary, which may
# itself carry none. It goes where gdb.sh points gdb to, under the same
# name in usr/lib/debug of the archive.
BUILDID=$(file -L "${BINARY}" | \
	sed -n 's/.*BuildID\[[^]]*\]=\([0-9a-f][0-9a-f][0-9a-f]*\).*/\1/p')
if [ -n "${BUILDID}" ]; then
	BUILDIDFILE=.build-id/$(echo "${BUILDID}" | cut -c1-2)/$(echo "${BUILDID}" | cut -c3-).debug
	DEBUGDIRS=$(gdb -batch -ex 'show debug-file-directory' 2>/dev/null | \
		sed -n 's/^.*"\(.*\)"\.$/\1/p')
	set -f
	OLDIFS=${IFS}
	IFS=:
	for DEBUGDIR in ${DEBUGDIRS:-/usr/lib/debug}; do
		if [ -f "${DEBUGDIR}/${BUILDIDFILE}" ]; then
			stage "usr/lib/debug/${BUILDIDFILE}" "$(abspath "${DEBUGDIR}/${BUILDIDFILE}")"
			break
		fi
	done
	IFS=${OLDIFS}
	set +f
fi

SEPARATOR1='Shared Object Library'
SEPARATOR2='Shared library is missing debugging information'
# XXX: This is kinda "postmortem ldd": the command below dumps the
# full list of the shared libraries the binary is linked against
# or those loaded via dlopen at the platform runtime.
# This is black voodoo magic. Do not touch. You are warned.
if gdb -batch -n "${BINARY}" -c "${COREFILE}" -ex 'info shared'    | \
	sed -n "/${SEPARATOR1}/,/${SEPARATOR2}/p;/${SEPARATOR2}/q" | \
	awk '{ print $NF }' | grep '^/' >"${LIBLIST}"
then
	# XXX: This is a very ugly hack to implement 'unless'
	# operator in bash for a long pipeline as a conditional.
	:
else
	[ -t 1 ] && cat <<COREMISMATCH
Core dump file is produced by the different Tarantool executable.

Looks like '${COREFILE}' is not generated by \`${BINARY}'.
If you see this message, please check that the given COREDUMP
is produced by the specified BINARY.
There are some temporary artefacts in ${TMPDIR}.
Remove it manually if you don't need them anymore.
COREMISMATCH
	exit 1;
fi

# The shared libraries keep their paths, under the root of the archive.
while IFS= read -r LIB; do
	stage "${LIB#/}" "${LIB}"
done <"${LIBLIST}"

# XXX: This is poor man's libthread_db location resolution.
# I'm sure there are many ways to implement this, but most of them
# requires additional tools, libs, etc. The one implemented below
# is quite simple, robust and requires nothing more than being
# already used above.
GREETING=$(gdb "${BINARY}" -c "${COREFILE}" -ex 'quit' 2>/dev/null)
LIBTHREAD_DB=
PATTERN1='[Thread debugging using libthread_db enabled]'
PATTERN2='Using host libthread_db library ".*"\.'

if echo "${GREETING}" | grep -F "${PATTERN1}" >/dev/null; then
	LIBTHREAD_DB=$(echo "${GREETING}" | grep "${PATTERN2}" | \
		awk -F'"' '{ print $2 }')
	if [ -n "${LIBTHREAD_DB}" ]; then
		stage "${LIBTHREAD_DB#/}" "${LIBTHREAD_DB}"
	fi
fi

if [ -z "${LIBTHREAD_DB}" ]; then
	[ -t 1 ] && cat <<NOLIBTHREAD_DB
Failed to find libthread_db.so. Brace yourselves, thread debugging
will be a challenging exercise.

This is just a notice, not an error.
NOLIBTHREAD_DB
	# XXX: Not an error, proceed packing.
fi

# Include GDB-wrapper script
if [ -n "${GDB}" ] && [ -f "${GDB}" ]; then
	stage "$(basename "${GDB}")" "${GDB}"
fi

# Include GDB-extensions (if any)
while [ -n "${EXTS}" ]; do
	EXT=$(abspath "${EXTS%%:*}") # pick the first
	if [ -f "${EXT}" ]; then
		stage "ext/$(basename "${EXT}")" "${EXT}"
	else
		[ -t 1 ] && cat <<MISSINGEXT
Cannot include extension '${EXT}': No such file or directory.
MISSINGEXT
		exit 1
	fi
	EXTS=${EXTS#*:} # drop the first
done

# Pack everything listed in TARLIST file into a tarball: the names
# there are those of the archive, relative to TMPDIR, the directory of
# STAGE, and each but the version a symbolic link that tar follows.
# A core dump is mostly holes: the pages of the mappings the process
# never touched. --sparse stores the data of a file and a map of its
# holes, so tar neither reads nor compresses the zeros of the holes. The
# posix format keeps the entry of such a file a regular one with the map
# in its extended header, which GNU tar, bsdtar and the archive/tar of Go
# read; tt coredump unpack refuses the old GNU sparse entries of the gnu
# format.
tar -czhf "${ARCHIVE}" -C "${TMPDIR}" -T "${TARLIST}" \
	--format=posix --sparse

[ -t 1 ] && cat <<FINALIZE
The resulting archive is located here: ${ARCHIVE}
The archive contents are listed below:
$(tar -tzf "${ARCHIVE}")

If you want to upload it, choose the available resource
and run the following command:
$ curl -T ${ARCHIVE} <resource-uri>
FINALIZE

# Cleanup temporary files: the lists, the version and the links of the
# staging directory, not the files they point to.
rm -rf "${TMPDIR}"
