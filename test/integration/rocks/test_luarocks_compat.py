"""The source rocks of tt packages under a plain LuaRocks: tt rocks of tt 2.

tt package pack --format rock-src makes a source rock whose rockspec builds
the package without tt, by the script of sh it carries. tt rocks of tt 2 is
LuaRocks 3.9.2 and knows nothing of tt 3: it installs the source rock, and
builds the bare rockspec from the git repository of the package, into the
files of the binary rock of the same pack.

TT_TEST_TT2 names the tt 2 executable; CI sets it, and the tests are skipped
without it. tt 2 runs LuaRocks for the tarantool on PATH, and builds against
its headers as tt 2 finds them: those of TT_CLI_TARANTOOL_PREFIX, or of the
prefix tarantool --version says it was built for, or of /usr. The tests are
skipped without them too, and a fixture without its tools is.
"""

import os
import platform
import re
import shutil
import subprocess
from dataclasses import dataclass, field
from pathlib import Path

import pytest

from utils import run_command_and_get_output

TT2 = os.environ.get("TT_TEST_TT2", "")

pytestmark = pytest.mark.skipif(
    not TT2,
    reason="TT_TEST_TT2 names no tt 2, whose tt rocks is the plain LuaRocks 3.9.2 of the tests",
)

DARWIN = platform.system() == "Darwin"

# What Tarantool loads a native module from, and the magic of a 64-bit
# Mach-O file, of the little-endian machines, and its filetype of a bundle,
# which the -bundle of LIBFLAG of LuaRocks links on macOS.
MODULE_EXT = ".dylib" if DARWIN else ".so"
MH_MAGIC_64 = b"\xcf\xfa\xed\xfe"
MH_BUNDLE = 8

# The sections of a rock and where tt rocks installs each, under .rocks/.
# share/tarantool/rocks/ is the metadata of LuaRocks, the rock's own.
SECTIONS = {"lua": ("share", "tarantool"), "lib": ("lib", "tarantool"), "bin": ("bin",)}
METADATA = "rocks"

# The version the git tag of a fixture gives its package.
VERSION = "1.0.0"

# The module fast of C of the package %s, whose answer() returns 42. It
# includes the headers of Tarantool, as a module does.
FAST_C = """#include <lua.h>
#include <lauxlib.h>
#include <module.h>

static int answer(lua_State *L)
{
	lua_pushinteger(L, 42);
	return 1;
}

int luaopen_%s_fast(lua_State *L)
{
	lua_newtable(L);
	lua_pushcfunction(L, answer);
	lua_setfield(L, -2, "answer");
	return 1;
}
"""


@dataclass
class Package:
    """A fixture: a package of a git repository whose HEAD is the tag of
    VERSION, the files a source rock of it installs, and what it prints in
    Tarantool."""

    name: str
    # The manifest after [platform].
    body: str
    files: dict[str, str]
    # The programs its build needs.
    tools: tuple[str, ...]
    # The files of each section of the rock, by path.
    lua: set[str]
    lib: set[str] = field(default_factory=set)
    bin: set[str] = field(default_factory=set)
    # A Lua chunk run in Tarantool beside the tree of the source rock, and
    # what it prints.
    script: str = ""
    printed: str = ""
    # What git ignores of what its build makes in the sources.
    gitignore: str = ""


PURE = Package(
    name="purelib",
    body="""
[components.purelib]
path = 'purelib'
include = ['*.lua', '*.json']

[components.roles]
path = 'roles'
namespace = 'roles'
""",
    files={
        "purelib/init.lua": "return { answer = 42 }\n",
        "purelib/data/schema.json": "{}\n",
        "roles/purelib.lua": "return { role = 'purelib' }\n",
    },
    tools=(),
    lua={
        "purelib/init.lua",
        "purelib/_version.lua",
        "purelib/data/schema.json",
        "roles/purelib.lua",
    },
    script="print(require('purelib').answer, require('roles.purelib').role, "
    "require('purelib._version').version)",
    printed=f"42\tpurelib\t{VERSION}",
)

# A package with no build block and a file at a path build.install cannot
# say, of a directory with dots in its name, which the script lays out.
MIGRATIONS = Package(
    name="migr",
    body="""
[components.migr]
path = 'migr'

[components.migrations]
path = 'migrations'
namespace = 'migr'
include = ['*.sql']
""",
    files={
        "migr/init.lua": "local fio = require('fio')\n"
        "local dir = fio.dirname(debug.getinfo(1, 'S').source:sub(2))\n"
        "return { up = fio.path.exists(fio.pathjoin(dir, '1.0.0', 'up.sql')) }\n",
        "migrations/1.0.0/up.sql": "create table t(id int);\n",
    },
    tools=(),
    lua={"migr/init.lua", "migr/_version.lua", "migr/1.0.0/up.sql"},
    script="print(require('migr').up)",
    printed="true",
)

LUA_C = Package(
    name="cmod",
    body="""
[components.cmod]
path = 'cmod'

[components.cmod.build]
backend = 'lua-c'
module = 'fast'
sources = ['fast.c']
""",
    files={"cmod/init.lua": "return require('cmod.fast')\n", "cmod/fast.c": FAST_C % "cmod"},
    tools=("cc",),
    lua={"cmod/init.lua", "cmod/_version.lua"},
    lib={f"cmod/fast{MODULE_EXT}"},
    script="print(require('cmod').answer())",
    printed="42",
)

# A pre_build hook writes answer.txt at the root; a build of shell compiles
# a program printing it, with the CC of LuaRocks or its own, and one of make,
# in the namespace of the package, writes a Lua module of it and a data file
# of TT_PACKAGE, which its output sends to share.
SCRIPTED_MAKEFILE = """all:
\tprintf 'return { answer = %s }\\n' "$$(cat ../answer.txt)" > answer.lua
\tprintf '{"package": "%s"}\\n' "$$TT_PACKAGE" > calc.json
"""

SCRIPTED_BUILD_SH = """set -eu
mkdir -p out
printf '#define ANSWER %s\\n' "$(cat ../answer.txt)" > out/answer.h
${CC:-cc} -I out -o out/scripted-tool tool.c
"""

SCRIPTED_TOOL_C = """#include <stdio.h>
#include "answer.h"

int main(void)
{
	printf("%d\\n", ANSWER);
	return 0;
}
"""

SCRIPTED = Package(
    name="scripted",
    body="""
[components.scripted]
path = 'scripted'

[components.tool]
path = 'tool'

[components.tool.build]
backend = 'shell'
command = 'sh'
args = ['./build.sh']
output = [{ path = 'out/scripted-tool', to = 'bin' }]

[components.calc]
path = 'calc'
namespace = 'scripted'

[components.calc.build]
backend = 'make'
make_target = 'all'
output = [{ path = 'answer.lua', to = 'share' }, { path = 'calc.json', to = 'share' }]

[hooks.pre_build]
backend = 'shell'
command = 'sh'
args = ['-c', 'printf 42 > answer.txt']
""",
    files={
        "scripted/init.lua": "return { answer = require('scripted.answer').answer }\n",
        "tool/build.sh": SCRIPTED_BUILD_SH,
        "tool/tool.c": SCRIPTED_TOOL_C,
        "calc/Makefile": SCRIPTED_MAKEFILE,
    },
    tools=("cc", "make"),
    lua={"scripted/init.lua", "scripted/_version.lua", "scripted/answer.lua", "scripted/calc.json"},
    bin={"scripted-tool"},
    script="print(require('scripted').answer)",
    printed="42",
    gitignore="/answer.txt\n/tool/out/\n/calc/answer.lua\n/calc/calc.json\n",
)

# The module of cmake is found against the headers find_path finds on
# CMAKE_PREFIX_PATH, where the prefix of Tarantool is.
CMAKE_LISTS = """cmake_minimum_required(VERSION 3.15)
project(cmk C)

find_path(TARANTOOL_INCLUDE_DIR tarantool/module.h)
if(NOT TARANTOOL_INCLUDE_DIR)
  message(FATAL_ERROR "no tarantool/module.h on CMAKE_PREFIX_PATH")
endif()

add_library(fast MODULE fast.c)
set_target_properties(fast PROPERTIES PREFIX "" SUFFIX ".so")
target_include_directories(fast PRIVATE "${TARANTOOL_INCLUDE_DIR}/tarantool")
if(APPLE)
  target_link_options(fast PRIVATE -undefined dynamic_lookup)
endif()
"""

CMAKE = Package(
    name="cmk",
    body="""
[components.cmk]
path = 'cmk'

[components.cmk.build]
backend = 'cmake'
output = ['fast.so']
""",
    files={
        "cmk/init.lua": "return require('cmk.fast')\n",
        "cmk/CMakeLists.txt": CMAKE_LISTS,
        "cmk/fast.c": FAST_C % "cmk",
    },
    tools=("cc", "cmake"),
    lua={"cmk/init.lua", "cmk/_version.lua"},
    lib={f"cmk/fast{MODULE_EXT}"},
    script="print(require('cmk').answer())",
    printed="42",
)

# A module of lua-c with static = true, which tt alone builds. Only its
# source rock is packed, which compiles nothing.
STATIC = Package(
    name="staticmod",
    body="""
[components.staticmod]
path = 'staticmod'

[components.staticmod.build]
backend = 'lua-c'
module = 'fast'
sources = ['fast.c']
static = true
""",
    files={"staticmod/fast.c": FAST_C % "staticmod"},
    tools=(),
    lua=set(),
)


def tarantool_headers() -> Path:
    """The headers of Tarantool tt 2 builds against: include/tarantool of
    TT_CLI_TARANTOOL_PREFIX, or of the prefix tarantool --version says, or of
    /usr when that is no directory."""
    prefix = os.environ.get("TT_CLI_TARANTOOL_PREFIX", "")
    if not prefix:
        version = subprocess.run(["tarantool", "--version"], capture_output=True, text=True)
        built = re.search(r"-DCMAKE_INSTALL_PREFIX=(/\S*)", version.stdout)
        prefix = built.group(1) if built and os.path.isdir(built.group(1)) else "/usr"

    return Path(prefix) / "include" / "tarantool"


@pytest.fixture(scope="module")
def tt2():
    """The tt 2 of TT_TEST_TT2, with a tarantool and its headers."""
    if shutil.which("tarantool") is None:
        pytest.skip("no tarantool in PATH, which tt 2 runs LuaRocks for")
    headers = tarantool_headers()
    if not (headers / "module.h").is_file():
        pytest.skip(f"no module.h in {headers}, the headers of Tarantool tt 2 builds against")

    rc, output = run_command_and_get_output([TT2, "version"])
    assert rc == 0, output
    assert re.search(r"Tarantool CLI version 2\.", output), f"TT_TEST_TT2 is no tt 2: {output}"
    return TT2


def write_package(root: Path, package: Package) -> Path:
    """Write the package as a git repository under root, whose rockspec builds
    it from there, and return it."""
    missing = [tool for tool in ("git", *package.tools) if shutil.which(tool) is None]
    if missing:
        pytest.skip(f"no {', '.join(missing)} for the package {package.name}")

    project = root / package.name
    manifest = f"""manifest_version = '0.1'

[package]
name = '{package.name}'
license = 'MIT'
license_files = ['LICENSE']
repository = 'git+file://{project}'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0-alpha'
"""
    files = {
        "app.manifest.toml": manifest + package.body,
        ".gitignore": "/_build/\n/.rocks/\n/app.manifest.lock\n/.tt-rock/\n" + package.gitignore,
        "LICENSE": "license\n",
        **package.files,
    }
    for path, text in files.items():
        (project / path).parent.mkdir(parents=True, exist_ok=True)
        (project / path).write_text(text)

    git = ["git", "-c", "user.email=t@t", "-c", "user.name=t", "-c", "init.defaultBranch=main"]
    for args in (["init", "-q"], ["add", "-A"], ["commit", "-qm", "init"], ["tag", f"v{VERSION}"]):
        subprocess.run([*git, *args], cwd=project, check=True)

    return project


def pack(tt_cmd, project: Path, out: Path, *formats: str) -> str:
    """Pack the formats of the project in one pack into out, and return what
    it printed."""
    rc, output = run_command_and_get_output(
        [tt_cmd, "package", "pack", *[f"--format={fmt}" for fmt in formats], "--out", str(out)],
        cwd=project,
        env=dict(os.environ, PWD=str(project)),
    )
    assert rc == 0, output
    return output


def rock_of(out: Path, package: Package, suffix: str) -> Path:
    """The file of the rock of the package in out: .src.rock, .rockspec, or
    the binary rock for .rock."""
    found = [
        path
        for path in out.glob(f"{package.name}-{VERSION}-1.*")
        if path.name.endswith(suffix) and (suffix != ".rock" or not path.name.endswith(".src.rock"))
    ]
    assert len(found) == 1, found
    return found[0]


def hostile_dir(root: Path) -> tuple[Path, Path]:
    """A directory under root whose path has a quote, a $, spaces and a ;,
    and the file the shell makes when it runs any of it unquoted: the name
    is the command "touch <file>" among them. The directory is not made."""
    canary = root / "PWNED"
    return root / f"it's $HOME $(touch {canary}) a b; touch {canary}; echo '" / "end", canary


def tt2_install(tt2, consumer: Path, rock: Path) -> tuple[int, str]:
    """Install the rock, or build the rockspec, with tt rocks of tt 2 into
    .rocks of consumer, a new tt 2 environment, and return its exit code and
    what it printed."""
    consumer.mkdir(parents=True)
    env = dict(os.environ, PWD=str(consumer))
    rc, output = run_command_and_get_output([tt2, "init"], cwd=consumer, env=env)
    assert rc == 0, output

    return run_command_and_get_output([tt2, "rocks", "install", str(rock)], cwd=consumer, env=env)


def installed(consumer: Path) -> dict[str, dict[str, bytes]]:
    """The files tt rocks installed into .rocks of consumer, by section of the
    rock: by path in the section, with their bytes."""
    sections = {}
    for section, parts in SECTIONS.items():
        root = consumer.joinpath(".rocks", *parts)
        sections[section] = {
            path.relative_to(root).as_posix(): path.read_bytes()
            for path in sorted(root.rglob("*"))
            if path.is_file() and (section != "lua" or path.relative_to(root).parts[0] != METADATA)
        }
    return sections


def tarantool(cwd: Path, script: str) -> str:
    """Run the Lua script in Tarantool in cwd, whose .rocks it loads the
    modules of, and return what it printed."""
    result = subprocess.run(
        ["tarantool", "-e", script + " os.exit(0)"],
        cwd=cwd,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, result.stderr
    return result.stdout.strip()


def is_bundle(path: Path) -> bool:
    """Whether the file at path is a 64-bit Mach-O bundle."""
    header = path.read_bytes()[:16]
    return header[:4] == MH_MAGIC_64 and int.from_bytes(header[12:16], "little") == MH_BUNDLE


@pytest.mark.parametrize(
    "package",
    [PURE, MIGRATIONS, LUA_C, SCRIPTED, CMAKE],
    ids=["pure", "pure-of-dotted-dirs", "lua-c", "shell-make", "cmake"],
)
def test_luarocks_builds_the_source_rock(tt_cmd, tt2, tmp_path, package):
    """tt rocks of tt 2 installs the source rock of the package, and builds
    its bare rockspec from the git repository of the package, which holds no
    file the build makes, into the files of its binary rock: lua/ of the same
    bytes, lib/ and bin/ of the same paths, since native files are compiled
    again. Tarantool loads the modules from the tree of the source rock."""
    tmp = tmp_path.resolve()
    project = write_package(tmp, package)

    # _version.lua says when the package was built: one pack makes the same in
    # both rocks.
    out = tmp / "out"
    pack(tt_cmd, project, out, "rock", "rock-src")

    trees = {}
    for what, suffix in (("source rock", ".src.rock"), ("bare rockspec", ".rockspec")):
        consumer = tmp / what.replace(" ", "-")
        rc, output = tt2_install(tt2, consumer, rock_of(out, package, suffix))
        assert rc == 0, output
        assert f"{package.name} {VERSION}-1 is now installed" in output
        trees[what] = consumer

        got = installed(consumer)
        assert set(got["lua"]) == package.lua, what
        assert set(got["lib"]) == package.lib, what
        assert set(got["bin"]) == package.bin, what

        for program in package.bin:
            path = consumer / ".rocks" / "bin" / program
            assert os.access(path, os.X_OK), f"{what}: {program} is not executable"

        if DARWIN:
            for module in package.lib:
                path = consumer / ".rocks" / "lib" / "tarantool" / module
                assert is_bundle(path), f"{what}: {module} is no Mach-O bundle"

    assert tarantool(trees["source rock"], package.script) == package.printed

    for program in package.bin:
        path = trees["source rock"] / ".rocks" / "bin" / program
        assert subprocess.run([path], capture_output=True, text=True).stdout == "42\n"

    # tt 3 installs the binary rock, as it names the host from the Go
    # runtime: LuaRocks 3.9.2 of tt 2 up to 2.15.2 takes the platform from
    # the first architecture of /bin/sh, x86_64 on a macOS of arm64, and
    # refuses a binary rock of macosx-aarch64 there (#1501).
    binary = tmp / "binary-rock"
    binary.mkdir()
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", str(rock_of(out, package, ".rock"))],
        cwd=binary,
        env=dict(os.environ, PWD=str(binary)),
    )
    assert rc == 0, output
    want = installed(binary)

    for what, consumer in trees.items():
        got = installed(consumer)
        assert got["lua"] == want["lua"], what
        assert set(got["lib"]) == set(want["lib"]), what
        assert set(got["bin"]) == set(want["bin"]), what


@pytest.mark.parametrize("package", [SCRIPTED, LUA_C], ids=["shell-make", "lua-c"])
def test_luarocks_builds_into_a_tree_of_any_name(tt_cmd, tt2, tmp_path, package):
    """tt rocks of tt 2 installs the source rock of the package, and builds
    its bare rockspec, into trees in a directory whose name has a quote, a $,
    spaces and a ;, which LuaRocks gives the commands of the rockspec as it
    is, LUADIR, LIBDIR and BINDIR of the install: the shell runs nothing of
    it, and every file of the rock is installed. Tarantool loads no module
    from there, as a ; separates the entries of package.path."""
    tmp = tmp_path.resolve()
    project = write_package(tmp, package)
    out = tmp / "out"
    pack(tt_cmd, project, out, "rock-src")

    consumers, canary = hostile_dir(tmp)

    for what, suffix in (("source rock", ".src.rock"), ("bare rockspec", ".rockspec")):
        consumer = consumers / what.replace(" ", "-")
        rc, output = tt2_install(tt2, consumer, rock_of(out, package, suffix))
        assert not canary.exists(), f"{what}: the shell ran the name of the tree: {output}"
        assert rc == 0, output
        assert f"{package.name} {VERSION}-1 is now installed" in output

        got = installed(consumer)
        assert set(got["lua"]) == package.lua, what
        assert set(got["lib"]) == package.lib, what
        assert set(got["bin"]) == package.bin, what


def test_luarocks_fails_on_the_source_rock_of_a_build_of_tt_alone(tt_cmd, tt2, tmp_path):
    """The source rock of a module of lua-c with static = true builds with tt
    only: the pack warns of it, and tt rocks of tt 2 fails on it, saying so,
    and installs nothing."""
    tmp = tmp_path.resolve()
    project = write_package(tmp, STATIC)

    out = tmp / "out"
    output = pack(tt_cmd, project, out, "rock-src")
    assert "the source rock builds with tt only" in output
    assert "components.staticmod.build.static" in output

    consumer = tmp / "consumer"
    rc, output = tt2_install(tt2, consumer, rock_of(out, STATIC, ".src.rock"))
    assert rc != 0, output
    # LuaRocks echoes the command of the build, which prints the message: the
    # line of the message itself begins with it.
    said = re.search(rf"^{STATIC.name} {re.escape(VERSION)} builds with tt only: .*$", output, re.M)
    assert said, output
    assert "components.staticmod.build.static" in said.group(0)
    assert "Failed building" in output
    assert not (consumer / ".rocks" / "share" / "tarantool" / "rocks" / STATIC.name).exists()
    assert not (consumer / ".rocks" / "lib" / "tarantool" / STATIC.name).exists()
