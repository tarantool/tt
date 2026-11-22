#!/bin/sh
# tt-rock.sh builds the tt package demo 1.2.3 without tt: the rockspec of the
# package runs it, so that LuaRocks builds the package from its sources. tt
# package pack generated it from app.manifest.toml; tt builds the package
# from that manifest, and does not run it.
#
#   sh ./tt-rock.sh build    builds the package into .tt-rock/stage/
#   sh ./tt-rock.sh install  copies the stage into LUADIR, LIBDIR and BINDIR
#
# LuaRocks gives the build CC, CFLAGS, LIBFLAG and LUA_INCDIR, the directory
# of the headers of Tarantool, and the install LUADIR, LIBDIR and BINDIR:
# the rockspec runs either with the argument - and writes them to its
# stdin, a line NAME=value each, in that order; without -, they are those
# of the environment. A build of the script has no cache and no
# incremental build, and none of the checks tt makes of what a build made;
# the rocks it builds with are those LuaRocks resolved by the rockspec, not
# those of the lock of the package.
#
# Without tt, the component "native", of the tt backend "shell", lacks:
#   - the cache of objects
set -eu

tt_root=`pwd`
tt_state=$tt_root/.tt-rock
tt_stage=$tt_state/stage

# tt_die message: fails the build, saying why.
tt_die() {
	printf 'tt-rock.sh: %s\n' "$*" >&2
	exit 1
}

# tt_need tool what: fails the build unless tool, which what runs, is in
# PATH.
tt_need() {
	command -v "$1" >/dev/null 2>&1 || tt_die "$2 runs $1, which is not in PATH"
}

# tt_output path dest: copies path, a file the output of a build lists, to
# dest, as tt copies the output of a backend that does not copy it itself.
tt_output() {
	[ -f "$1" ] || tt_die "the build made no $1, which its output lists"
	mkdir -p -- "${2%/*}"
	cp -- "$1" "$2"
}

# tt_lay path dest: copies path, a file of the project the layout of tt
# takes, to dest in the stage, as tt lays it out after the builds; a file
# no build made is not there, and is left out.
tt_lay() {
	[ -f "$tt_root/$1" ] || return 0
	mkdir -p -- "$tt_stage/${2%/*}"
	cp -- "$tt_root/$1" "$tt_stage/$2"
}

# tt_read name: reads the line name=value of stdin, the next variable of
# LuaRocks the rockspec gives the script, into tt_value.
tt_read() {
	IFS= read -r tt_line ||
		tt_die "stdin ends before $1=<value>, a variable of LuaRocks"
	case $tt_line in
	"$1"=*) tt_value=${tt_line#*=} ;;
	*) tt_die "stdin has \"$tt_line\" for $1=<value>, a variable of LuaRocks" ;;
	esac
}

# tt_read_end: fails unless stdin ends after the variables of LuaRocks, as
# it does unless a value of one of them has a newline.
tt_read_end() {
	! IFS= read -r tt_line ||
		tt_die "stdin has \"$tt_line\" after the variables of LuaRocks"
}

# tt_env sets the variables every build of the package has: those of a
# build of tt, of the machine the script runs on, and the toolchain of
# LuaRocks.
tt_env() {
	case `uname -s` in
	Linux)
		TT_PLATFORM_OS=linux TT_MODULE_EXT=.so
		TT_MODULE_LDFLAGS=-Wl,-Bsymbolic-functions tt_libflag=-shared
		;;
	Darwin)
		TT_PLATFORM_OS=darwin TT_MODULE_EXT=.dylib TT_MODULE_LDFLAGS=
		tt_libflag='-bundle -undefined dynamic_lookup -all_load'
		;;
	*)
		tt_die "a tt package builds on Linux and macOS, not on `uname -s`"
		;;
	esac
	case `uname -m` in
	x86_64 | amd64) TT_PLATFORM_ARCH=amd64 ;;
	aarch64 | arm64) TT_PLATFORM_ARCH=arm64 ;;
	*) TT_PLATFORM_ARCH=`uname -m` ;;
	esac
	TT_BUILD_JOBS=`getconf _NPROCESSORS_ONLN 2>/dev/null || :`
	[ -n "$TT_BUILD_JOBS" ] || TT_BUILD_JOBS=1
	TT_PROJECT_ROOT=$tt_root TT_PACKAGE=demo TT_VERSION=1.2.3
	TT_STATE_DIR=$tt_state TT_LINK_ROOT=$tt_state/link
	export TT_PROJECT_ROOT TT_PACKAGE TT_VERSION TT_PLATFORM_OS TT_PLATFORM_ARCH \
		TT_BUILD_JOBS TT_STATE_DIR TT_LINK_ROOT TT_MODULE_EXT TT_MODULE_LDFLAGS
	CC=${CC:-cc} CFLAGS=${CFLAGS-'-O2 -fPIC'} LIBFLAG=${LIBFLAG:-$tt_libflag}
	export CC CFLAGS LIBFLAG
	if [ -f "${LUA_INCDIR-}/module.h" ]; then
		TT_TARANTOOL_INCLUDE_DIR=$LUA_INCDIR
		export TT_TARANTOOL_INCLUDE_DIR
	fi
	case ${LUA_INCDIR-} in
	*/include/tarantool) TT_TARANTOOL_PREFIX=${LUA_INCDIR%/include/tarantool} ;;
	*/include/tarantool/) TT_TARANTOOL_PREFIX=${LUA_INCDIR%/include/tarantool/} ;;
	esac
	[ -z "${TT_TARANTOOL_PREFIX-}" ] || export TT_TARANTOOL_PREFIX
}

# The hook pre_build, built by the tt backend "shell".
tt_step_1() {
	export GEN='it'\''s $HOME $(echo no) `echo no` \ *
and a line'
	cd "$tt_root"
	(tt_export_1)
}

# The script of the export of the tt backend "shell" for the hook pre_build.
tt_export_1() {
sh -c 'printf 42 > native/answer.txt'
}

# The component "native", built by the tt backend "shell".
tt_step_2() {
	export CFLAGS=-O3
	TT_COMPONENT_NAME=native
	TT_OUTPUT_DIR=$tt_stage/lib/demo
	TT_SHARE_DIR=$tt_stage/lua/demo
	TT_PROGRAMS_DIR=$tt_stage/bin
	TT_BUILD_DIR=$tt_state/build/native
	TT_LINK_DIR=$TT_LINK_ROOT/native
	export TT_COMPONENT_NAME TT_OUTPUT_DIR TT_SHARE_DIR TT_PROGRAMS_DIR TT_BUILD_DIR TT_LINK_DIR
	mkdir -p -- "$TT_OUTPUT_DIR" "$TT_SHARE_DIR" "$TT_PROGRAMS_DIR" "$TT_BUILD_DIR" "$TT_LINK_DIR"
	cd "$tt_root"/native
	(tt_export_2)
	tt_output out/fast.so "$tt_stage"/lib/demo/fast.so
	tt_output out/tool "$tt_stage"/bin/tool
	tt_output out/data.json "$tt_stage"/lua/demo/data.json
	tt_output out/fast.h "$TT_LINK_DIR"/include/fast.h
}

# The script of the export of the tt backend "shell" for the component "native".
tt_export_2() {
./build.sh 'it'\''s $HOME $(echo no) `echo no` \ *
and a line'
}

# The component "calc", built by the tt backend "make".
tt_step_3() {
	export PATH=/opt/make/bin:/usr/bin:/bin
	TT_COMPONENT_NAME=calc
	TT_OUTPUT_DIR=$tt_stage/lib
	TT_SHARE_DIR=$tt_stage/lua
	TT_PROGRAMS_DIR=$tt_stage/bin
	TT_BUILD_DIR=$tt_state/build/calc
	TT_LINK_DIR=$TT_LINK_ROOT/calc
	export TT_COMPONENT_NAME TT_OUTPUT_DIR TT_SHARE_DIR TT_PROGRAMS_DIR TT_BUILD_DIR TT_LINK_DIR
	mkdir -p -- "$TT_OUTPUT_DIR" "$TT_SHARE_DIR" "$TT_PROGRAMS_DIR" "$TT_BUILD_DIR" "$TT_LINK_DIR"
	cd "$tt_root"/calc/src
	(tt_export_3)
}

# The script of the export of the tt backend "make" for the component "calc".
tt_export_3() {
make -C . -f Makefile all
}

# The component "gw", built by the tt backend "go".
tt_step_4() {
	TT_COMPONENT_NAME=gw
	TT_OUTPUT_DIR=$tt_stage/bin
	TT_SHARE_DIR=$tt_stage/lua/demo
	TT_PROGRAMS_DIR=$tt_stage/bin
	TT_BUILD_DIR=$tt_state/build/gw
	TT_LINK_DIR=$TT_LINK_ROOT/gw
	export TT_COMPONENT_NAME TT_OUTPUT_DIR TT_SHARE_DIR TT_PROGRAMS_DIR TT_BUILD_DIR TT_LINK_DIR
	mkdir -p -- "$TT_OUTPUT_DIR" "$TT_SHARE_DIR" "$TT_PROGRAMS_DIR" "$TT_BUILD_DIR" "$TT_LINK_DIR"
	cd "$tt_root"/gw
	(tt_export_4)
}

# The script of the export of the tt backend "go" for the component "gw".
tt_export_4() {
:
}

# The hook post_build, built by the tt backend "make".
tt_step_5() {
	cd "$tt_root"/tools
	(tt_export_5)
}

# The script of the export of the tt backend "make" for the hook post_build.
tt_export_5() {
cat <<EOF > done.txt
$TT_VERSION
EOF
}

# tt_build builds the package into the stage.
tt_build() {
	tt_env
	rm -rf -- "$tt_state"
	mkdir -p -- "$tt_stage/lua" "$tt_stage/lib" "$tt_stage/bin" "$TT_LINK_ROOT"
	tt_need sh 'the hook pre_build'
	(PATH=/opt/make/bin:/usr/bin:/bin; tt_need make 'the component "calc"')
	tt_need make 'the hook post_build'
	(tt_step_1)
	(tt_step_2)
	(tt_step_3)
	(tt_step_4)
	tt_lay native/out/fast.so lib/demo/out/fast.so
	tt_lay native/v1.2/x.json lua/demo/v1.2/x.json
	(tt_step_5)
}

# tt_copy dir dest: copies what dir of the stage holds into dest, when it
# holds anything.
tt_copy() {
	tt_files=`ls -A -- "$tt_stage/$1"`
	[ -n "$tt_files" ] || return 0
	mkdir -p -- "$2"
	cp -R -- "$tt_stage/$1/." "$2"
}

# tt_install copies the stage into the directories of the rock LuaRocks
# installs: lua/ into LUADIR, lib/ into LIBDIR, bin/ into BINDIR.
tt_install() {
	[ -d "$tt_stage" ] || tt_die "the package is not built: run sh ./tt-rock.sh build"
	tt_copy lua "${LUADIR:?names no directory of the Lua files of the rock}"
	tt_copy lib "${LIBDIR:?names no directory of the modules of the rock}"
	tt_copy bin "${BINDIR:?names no directory of the programs of the rock}"
}

case ${1-} in
build)
	if [ "${2-}" = - ]; then
		tt_read CC
		CC=$tt_value
		tt_read CFLAGS
		CFLAGS=$tt_value
		tt_read LIBFLAG
		LIBFLAG=$tt_value
		tt_read LUA_INCDIR
		LUA_INCDIR=$tt_value
		tt_read_end
		export CC CFLAGS LIBFLAG LUA_INCDIR
	fi
	tt_build
	;;
install)
	if [ "${2-}" = - ]; then
		tt_read LUADIR
		LUADIR=$tt_value
		tt_read LIBDIR
		LIBDIR=$tt_value
		tt_read BINDIR
		BINDIR=$tt_value
		tt_read_end
		export LUADIR LIBDIR BINDIR
	fi
	tt_install
	;;
*) tt_die "usage: sh ./tt-rock.sh build [-] | install [-]" ;;
esac
