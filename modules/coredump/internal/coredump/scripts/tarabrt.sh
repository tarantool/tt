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
  - the files of the host given with -f, under their paths: all shared
    libraries loaded (even via dlopen(3)) at the crash moment and
    libthread_db
  - /usr/lib/debug/.build-id/XX/YYYY.debug - the separate debugging
    information of the executable, when the host has it, given with -f
    from any debug directory of the host

tt finds those files in the core dump and passes them with -f: the script
asks no debugger.

SYNOPSIS

  $0 [-h] [-c core] [-d dir] [-e executable] [-f [name=]file]...
     [-p procID] [-t datetime]

Supported options are:
  -c COREDUMP                   Use file COREDUMP as a core dump to examine.
                                See core(5) for more info. Without it, the
                                core dump of the process PID is read from
                                the standard input (see -p).

  -d DIRECTORY                  Create the resulting archive with the artefacts
                                within DIRECTORY.

  -e TARANTOOL                  Use file TARANTOOL as the executable file for
                                examining with a core dump COREDUMP. By default
                                tarantool executable path is obtained from PATH.
                                If PID is specified, the one from /proc/PID/exe
                                is chosen (see proc(5) for more info).

  -f [NAME=]FILE                Include FILE, a file of the host the core
                                dump needs, into the archive under its
                                path, or as NAME, a relative path within
                                the archive. This option may appear
                                multiple times.

  -g GDBWRAPPER                 Include GDB-wrapper script GDBWRAPPER into the
                                archive.

  -p PID                        PID of the dumped process, as seen in the PID
                                namespace in which the given process resides
                                (see %p in core(5) for more info). This flag
                                have to be set when ${TOOL} is used as
                                kernel.core_pattern pipeline script: without
                                -c, the core dump is read from the standard
                                input into DIRECTORY/COMM-core.PID.DATETIME,
                                with COMM of /proc/PID/comm, and the
                                executable is /proc/PID/exe.

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
FILES=
GDB=
PID=
TIME=$(date +%s)

# Parse CLI options.
OPTIONS=$(getopt -o c:d:e:f:g:hp:t:x: -n "${TOOL}" -- "$@")
eval set -- "${OPTIONS}"
while true; do
	case "$1" in
		--) shift; break;;
		-c) COREFILE=$2; shift 2;;
		-d) COREDIR=$2;  shift 2;;
		-e) BINARY=$2;   shift 2;;
		-f) FILES="${FILES}$2
"; shift 2;;
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
if [ -n "${BINARY}" ]; then
	BINARY=$(abspath "${BINARY}")
fi
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
# A core given with -c is packed as it is: the standard input is
# not the core then, and tt, which stores the core of a pipe
# itself, passes it so.
if [ -z "${COREFILE}" ] && [ -n "${PID}" ] && [ -d /proc/"${PID}" ]; then
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

# The tarantool of PATH is looked up when neither -e nor the process
# of a pipe gives one, so that a pipe stores the core of another
# process on a host without tarantool in PATH.
if [ -z "${BINARY}" ]; then
	if ! which tarantool; then
		[ -t 1 ] && cat <<NOEXEC
There is no Tarantool executable found in PATH. Either make sure
it is there or specify it explicitly with the '-e' option.
NOEXEC
	fi
	BINARY=$(which tarantool)
fi

if [ -z "${COREFILE}" ]; then
	[ -t 1 ] && cat <<NOCOREDUMP
There is no core dump file passed to ${TOOL}. The artefacts can't
be collected. If you see this message, check the usage by running
\`${TOOL} -h': -c option is the obligatory one.
NOCOREDUMP
	exit 1;
fi

# tt checks the core and the executable before it runs the script: that
# the core is a core dump, that the executable is an ELF executable of
# Tarantool, and that its build ID is that of the executable of the core.
# It reads the core for the files of the host to pack with it, which it
# passes with -f, so neither gdb nor file(1) is needed here.

# Resolve the host name if possible.
HOSTNAME=$(hostname 2>/dev/null || echo hostname)

# Proceed with collecting and packing artefacts.
PARTIAL=
TMPDIR=$(mktemp -d -p "${COREDIR}")

# A pack that fails, or is interrupted, leaves nothing behind in COREDIR:
# neither TMPDIR nor the archive tar was writing, which PARTIAL names
# meanwhile. An archive of the name packed before stays when the pack
# fails before tar starts. One that succeeds removes TMPDIR at the end.
cleanup() {
	STATUS=$?
	if [ "${STATUS}" -ne 0 ]; then
		rm -rf "${TMPDIR}"
		if [ -n "${PARTIAL}" ]; then
			rm -f "${PARTIAL}"
		fi
	fi
	exit "${STATUS}"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

TARLIST=${TMPDIR}/tarlist
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

# The files of the host the core needs keep their paths, under the root
# of the archive: the shared libraries the process had loaded, and
# libthread_db. NAME=FILE puts FILE into the archive as NAME, as the
# separate debugging information of the binary, which goes to
# usr/lib/debug, where gdb.sh points gdb to in the archive, from any
# debug directory of the host. They are given one per line.
set -f
OLDIFS=${IFS}
IFS='
'
for FILE in ${FILES}; do
	case "${FILE}" in
		/*) stage "${FILE#/}" "${FILE}";;
		*)  stage "${FILE%%=*}" "$(abspath "${FILE#*=}")";;
	esac
done
IFS=${OLDIFS}
set +f

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
PARTIAL=${ARCHIVE}
tar -czhf "${ARCHIVE}" -C "${TMPDIR}" -T "${TARLIST}" \
	--format=posix --sparse
PARTIAL=

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
