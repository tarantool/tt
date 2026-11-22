#!/bin/sh
# tt-rock.sh builds the tt package single 0.1.0 without tt: the rockspec of the
# package runs it, so that LuaRocks builds the package from its sources. tt
# package pack generated it from app.manifest.toml; tt builds the package
# from that manifest, and does not run it.
#
#   sh ./tt-rock.sh build    builds the package into .tt-rock/stage/
#   sh ./tt-rock.sh install  copies the stage into LUADIR, LIBDIR and BINDIR
#
# LuaRocks gives the build CC, CFLAGS, LIBFLAG and LUA_INCDIR, the directory
# of the headers of Tarantool. A build of the script has no cache and no
# incremental build, and none of the checks tt makes of what a build made;
# the rocks it builds with are those LuaRocks resolved by the rockspec, not
# those of the lock of the package.
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
	TT_PROJECT_ROOT=$tt_root TT_PACKAGE=single TT_VERSION=0.1.0
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

# The component "lib", built by the tt backend "make".
tt_step_1() {
	TT_COMPONENT_NAME=lib
	TT_OUTPUT_DIR=$tt_stage/lib/single
	TT_SHARE_DIR=$tt_stage/lua/single
	TT_PROGRAMS_DIR=$tt_stage/bin
	TT_BUILD_DIR=$tt_state/build/lib
	TT_LINK_DIR=$TT_LINK_ROOT/lib
	export TT_COMPONENT_NAME TT_OUTPUT_DIR TT_SHARE_DIR TT_PROGRAMS_DIR TT_BUILD_DIR TT_LINK_DIR
	mkdir -p -- "$TT_OUTPUT_DIR" "$TT_SHARE_DIR" "$TT_PROGRAMS_DIR" "$TT_BUILD_DIR" "$TT_LINK_DIR"
	cd "$tt_root"
	(tt_export_1)
}

# The script of the export of the tt backend "make" for the component "lib".
tt_export_1() {
make -C . -f Makefile all
}

# tt_build builds the package into the stage.
tt_build() {
	tt_env
	rm -rf -- "$tt_state"
	mkdir -p -- "$tt_stage/lua" "$tt_stage/lib" "$tt_stage/bin" "$TT_LINK_ROOT"
	(tt_step_1)
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
build) tt_build ;;
install) tt_install ;;
*) tt_die "usage: sh ./tt-rock.sh build | install" ;;
esac
