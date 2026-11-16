"""The libraries of the native modules of a package, built by the package.

A `lua-c` component links a library another component of the package builds:
the build of that component sends it, with its header, to `to = "link"` of its
`output`, and `link = ["foo"]` of the `lua-c` block links it. The module is
then loaded in Tarantool, from the tree of the project and from a tree it is
installed into.

The tests need cc, ar and a tarantool with its headers, as tt finds them
(TT_CLI_TARANTOOL_PREFIX names the prefix of a tarantool built elsewhere).
"""

import os
import platform
import re
import shutil
import subprocess
from pathlib import Path

import pytest

ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

pytestmark = pytest.mark.skipif(
    shutil.which("cc") is None or shutil.which("ar") is None or shutil.which("tarantool") is None,
    reason="no cc, ar or tarantool",
)

DARWIN = platform.system() == "Darwin"

# The file of the module answer, and the directory of the module itself as
# the loader names it in an rpath.
MODULE = "answer.dylib" if DARWIN else "answer.so"
ORIGIN = "@loader_path" if DARWIN else "$ORIGIN"

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['foo', 'answer']
default = true

[components.foo]
path = 'foo'

[components.foo.build]
{foo_build}
[components.answer]
path = 'answer'
namespace = ''

[components.answer.build]
backend = 'lua-c'
module = 'answer'
sources = ['answer.c']
link = ['foo']
static = {static}
"""

# The build block of foo of a shell component that runs script.
SHELL_BUILD = """backend = 'shell'
command = 'sh'
args = ['-c', '{script}']
output = {output}
"""

FOO_C = '#include "foo.h"\nint foo_answer(void) { return FOO_ANSWER; }\n'

FOO_H = "#define FOO_ANSWER 42\nint foo_answer(void);\n"

ANSWER_C = """#include <lua.h>
#include <foo.h>

static int l_answer(lua_State *L) {
\tlua_pushinteger(L, foo_answer());
\treturn 1;
}

int luaopen_answer(lua_State *L) {
\tlua_pushcfunction(L, l_answer);
\treturn 1;
}
"""

# The static library of foo, built with -fPIC to be linked into a module.
STATIC_FOO = "cc -fPIC -c foo.c -o foo.o && ar rcs libfoo.a foo.o"


def run(tt_cmd, cwd: Path, *args: str, env=None) -> subprocess.CompletedProcess:
    return subprocess.run([str(tt_cmd), *args], cwd=cwd, capture_output=True, text=True, env=env)


def make_project(
    root: Path,
    script: str = "",
    output: str = "",
    static: bool = False,
    foo_build: str = "",
    **extra,
) -> Path:
    """Write the package of foo and answer, foo a shell component that runs
    script and has the output output, or the one of the build block
    foo_build; extra adds lines to the block of answer."""
    (root / "foo").mkdir(parents=True)
    (root / "foo" / "foo.c").write_text(FOO_C)
    (root / "foo" / "foo.h").write_text(FOO_H)
    (root / "answer").mkdir()
    (root / "answer" / "answer.c").write_text(ANSWER_C)
    (root / "VERSION").write_text("1.0.0\n")

    if not foo_build:
        foo_build = SHELL_BUILD.format(script=script, output=output)

    manifest = MANIFEST.format(foo_build=foo_build, static=str(static).lower())
    for key, value in extra.items():
        manifest += f"{key} = {value}\n"

    (root / "app.manifest.toml").write_text(manifest)
    return root


def shared_library(name: str, needs: str = "", own_rpath: bool = False) -> tuple[str, str, str]:
    """How a shell component builds the shared library name of <name>.c, as
    a build of the host makes one to bundle: the command, the file it
    builds and the name of the copy bundle makes of it.

    On Linux the library is lib<name>.so of the SONAME lib<name>.so.1, the
    name of the copy; on macOS lib<name>.dylib of the install name
    @rpath/lib<name>.1.dylib, whose base name the copy takes. The library is
    linked with the library needs of the directory when needs is given, and
    finds what it needs by an rpath of its own directory when own_rpath is
    set.
    """
    if DARWIN:
        file, copy = f"lib{name}.dylib", f"lib{name}.1.dylib"
        command = f"cc -dynamiclib -o {file} {name}.c -Wl,-install_name,@rpath/{copy}"
        rpath = " -Wl,-rpath,@loader_path"
    else:
        file, copy = f"lib{name}.so", f"lib{name}.so.1"
        command = f"cc -shared -fPIC -o {file} {name}.c -Wl,-soname,{copy}"
        # The backslash keeps $ORIGIN from the shell of the script.
        rpath = " -Wl,-rpath,\\$ORIGIN"

    if needs:
        command += f" -L. -l{needs}"

    if own_rpath:
        command += rpath

    return command, file, copy


def build(tt_cmd, project: Path, env=None) -> subprocess.CompletedProcess:
    """Build the package, skipping the test for a tarantool without headers."""
    result = run(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO), env=env)
    if "tarantool development headers not found" in result.stderr:
        pytest.skip("the tarantool tt finds has no headers")

    assert result.returncode == 0, result.stderr
    return result


def pack(tt_cmd, project: Path) -> Path:
    """Pack the package without its dependencies and move the archive out of
    the project, beside it."""
    pack = ("package", "pack", "--without-deps", "--registry", str(ROCKS_REPO))
    result = run(tt_cmd, project, *pack)
    assert result.returncode == 0, result.stderr

    archives = list((project / "_build" / "pack").glob("*.tt"))
    assert len(archives) == 1, archives

    return Path(shutil.move(archives[0], project.parent))


def install(tt_cmd, archive: Path, cwd: Path, *args: str, env=None) -> None:
    """Install the archive from cwd, args the flags of tt package install."""
    env = dict(env or os.environ, TT_REGISTRIES=str(ROCKS_REPO))
    result = run(tt_cmd, cwd, "package", "install", *args, str(archive), env=env)
    assert result.returncode == 0, result.stderr


def tarantool(cwd: Path, script: str) -> str:
    """Run the Lua script in Tarantool in cwd, which loads the modules of
    .rocks of cwd, with no LD_LIBRARY_PATH, and return what it printed."""
    env = {
        key: value
        for key, value in os.environ.items()
        if key not in ("LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH")
    }
    result = subprocess.run(
        ["tarantool", "-e", script + " os.exit(0)"],
        cwd=cwd,
        capture_output=True,
        text=True,
        env=env,
    )
    assert result.returncode == 0, result.stderr
    return result.stdout.strip()


def require_answer(cwd: Path) -> str:
    """Load the module answer from .rocks of cwd in Tarantool and return what
    its function returns."""
    return tarantool(cwd, "print(require('answer')())")


def load_paths(module: Path) -> tuple[list[str], list[str]]:
    """The rpaths of the native file module and the libraries it asks the
    loader for: DT_RUNPATH, DT_RPATH and DT_NEEDED as readelf -d prints them,
    LC_RPATH and LC_LOAD_DYLIB as otool -l does on macOS."""
    if DARWIN:
        printed = subprocess.run(
            ["otool", "-l", str(module)],
            capture_output=True,
            text=True,
            check=True,
        ).stdout
        rpaths, needed, command = [], [], ""
        for line in printed.splitlines():
            words = line.split()
            if words[:1] == ["cmd"]:
                command = words[1]
            elif words[:1] == ["path"] and command == "LC_RPATH":
                rpaths.append(words[1])
            elif words[:1] == ["name"] and command == "LC_LOAD_DYLIB":
                needed.append(words[1])
        return rpaths, needed

    printed = subprocess.run(
        ["readelf", "-d", str(module)],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    rpaths = []
    for paths in re.findall(r"\((?:RUNPATH|RPATH)\)\s+Library r(?:un)?path: \[(.*)\]", printed):
        rpaths += paths.split(":")
    return rpaths, re.findall(r"\(NEEDED\)\s+Shared library: \[(.*)\]", printed)


@pytest.mark.parametrize("static", [False, True], ids=["dynamic", "static"])
def test_link_a_static_library_of_a_component(tt_cmd, tmp_path, static):
    """A lua-c module links the static library a shell component built.

    The library and its header go to _build/link/default/foo/, the header to
    include/ of it, and neither reaches the tree of the project.
    """
    project = make_project(
        tmp_path / "project",
        STATIC_FOO,
        "[{ path = 'libfoo.a', to = 'link' }, { path = 'foo.h', to = 'link' }]",
        static=static,
    )

    build(tt_cmd, project)

    link = project / "_build" / "link" / "default" / "foo"
    assert (link / "libfoo.a").is_file()
    assert (link / "include" / "foo.h").is_file()
    assert not list((project / ".rocks").rglob("libfoo.a"))
    assert not list((project / ".rocks").rglob("foo.h"))

    assert require_answer(project) == "42"


@pytest.mark.parametrize("static", [False, True], ids=["dynamic", "static"])
def test_bundle_a_shared_library_of_a_component(tt_cmd, tmp_path, static):
    """A lua-c module bundles the shared library a shell component built.

    The library goes beside the module under its SONAME, into the tree, the
    archive pack makes and the tree install unpacks it into, elsewhere, where
    the module loads with no LD_LIBRARY_PATH and the sources of the library
    gone. With static = true the build passes the static check: the module
    finds the library beside itself. On macOS the library is a dylib whose
    install name is @rpath/libfoo.1.dylib, and the copy takes its base name.
    """
    command, library, copy = shared_library("foo")

    project = make_project(
        tmp_path / "project",
        command,
        f"[{{ path = '{library}', to = 'link' }}, {{ path = 'foo.h', to = 'link' }}]",
        static=static,
        bundle="['foo']",
    )

    build(tt_cmd, project)

    lib = project / ".rocks" / "lib" / "tarantool"
    assert (lib / MODULE).is_file()
    assert (lib / copy).is_file()
    assert require_answer(project) == "42"

    archive = pack(tt_cmd, project)
    target = tmp_path / "target"
    target.mkdir()
    shutil.rmtree(project)

    install(tt_cmd, archive, target)

    installed = target / ".rocks" / "lib" / "tarantool"
    assert (installed / copy).is_file()
    assert require_answer(target) == "42"


def test_bundle_moves_with_the_module(tt_cmd, tmp_path):
    """A module and the library it bundles move as one.

    The module asks the loader for the copy beside it by the rpath of its own
    directory alone, $ORIGIN, @loader_path on macOS, and by no path of the
    machine it was built on. Packed, installed into the user scope from
    another directory with the project gone, and the root of the scope then
    moved elsewhere as a whole, it loads with no LD_LIBRARY_PATH from the
    version of the application, where it is and where it was moved to.
    """
    command, library, copy = shared_library("foo")

    project = make_project(
        tmp_path / "project",
        command,
        f"[{{ path = '{library}', to = 'link' }}, {{ path = 'foo.h', to = 'link' }}]",
        bundle="['foo']",
    )

    build(tt_cmd, project)

    archive = pack(tt_cmd, project)
    shutil.rmtree(project)

    user_root = tmp_path / "user"
    env = dict(
        os.environ,
        TT_USER_ROOT=str(user_root),
        TT_SYSTEM_ROOT=str(tmp_path / "system"),
    )
    env.pop("TT_SYSTEMD_UNIT", None)
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()

    install(tt_cmd, archive, elsewhere, "--scope", "user", env=env)

    version = Path("versions") / "my-app" / "my-app_1.0.0"
    lib = user_root / version / ".rocks" / "lib" / "tarantool"
    assert (lib / copy).is_file()

    rpaths, needed = load_paths(lib / MODULE)
    assert rpaths == [ORIGIN]
    assert (f"@rpath/{copy}" if DARWIN else copy) in needed, needed

    assert require_answer(user_root / version) == "42"

    moved = tmp_path / "moved"
    user_root.rename(moved)

    assert require_answer(moved / version) == "42"


# foo of FOO_OF_BAZ_C needs the library baz of BAZ_C.
FOO_OF_BAZ_C = (
    '#include "foo.h"\nint baz_answer(void);\nint foo_answer(void) { return baz_answer(); }\n'
)

BAZ_C = '#include "foo.h"\nint baz_answer(void) { return FOO_ANSWER; }\n'


@pytest.mark.parametrize("static", [False, True], ids=["dynamic", "static"])
def test_bundle_a_library_the_bundled_one_needs(tt_cmd, tmp_path, static):
    """libfoo the module links needs libbaz, and bundle lists both.

    The shell component builds both, the module links both by link = ["foo"],
    and the copies of both go beside the module: the module loads from the
    tree of the project and from the tree install unpacks the archive into,
    with no LD_LIBRARY_PATH and the project gone, and tt warns of no library
    that is not bundled.

    The module asks the loader for libbaz itself. The loader of Linux looks
    for a library libfoo needs by the rpath of libfoo, not by that of the
    module, and finds libbaz only because the module has loaded it already.
    The linkers of gcc of Ubuntu and of zig cc drop by --as-needed a library
    whose functions the module calls none of, so the module links with
    -Wl,--no-as-needed on Linux: without it the module does not ask for
    libbaz and does not load, "libbaz.so.1: cannot open shared object file",
    though bundle lists libbaz. dyld of macOS resolves @rpath/libbaz.1.dylib
    of libfoo by the rpaths of the module as well.

    With static = true libfoo finds libbaz by an rpath of its own directory:
    the static check of the core takes a library a file needs as beside it
    by an rpath of that file only.
    """
    baz, baz_file, baz_copy = shared_library("baz")
    foo, foo_file, foo_copy = shared_library("foo", needs="baz", own_rpath=static)

    extra = {"bundle": "['foo', 'baz']"}
    if not DARWIN:
        extra["platforms"] = "{ linux = { ldflags = ['-Wl,--no-as-needed'] } }"

    project = make_project(
        tmp_path / "project",
        f"{baz} && {foo}",
        f"[{{ path = '{foo_file}', to = 'link' }}, {{ path = '{baz_file}', to = 'link' }}, "
        "{ path = 'foo.h', to = 'link' }]",
        static=static,
        **extra,
    )
    (project / "foo" / "foo.c").write_text(FOO_OF_BAZ_C)
    (project / "foo" / "baz.c").write_text(BAZ_C)

    result = build(tt_cmd, project)
    assert "not bundled" not in result.stderr

    lib = project / ".rocks" / "lib" / "tarantool"
    assert (lib / foo_copy).is_file()
    assert (lib / baz_copy).is_file()

    _, needed = load_paths(lib / MODULE)
    for copy in (foo_copy, baz_copy):
        assert (f"@rpath/{copy}" if DARWIN else copy) in needed, needed

    assert require_answer(project) == "42"

    archive = pack(tt_cmd, project)
    target = tmp_path / "target"
    target.mkdir()
    shutil.rmtree(project)

    install(tt_cmd, archive, target)

    installed = target / ".rocks" / "lib" / "tarantool"
    assert (installed / foo_copy).is_file()
    assert (installed / baz_copy).is_file()
    assert require_answer(target) == "42"


# Two shell components, each the static library ver of its own version,
# and a module of each that links it.
CLASH_MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['ver1', 'ver2', 'one', 'two']
default = true
"""

CLASH_LIBRARY = """
[components.ver{n}]
path = 'ver{n}'

[components.ver{n}.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'cc -fPIC -c ver.c -o ver.o && ar rcs libver.a ver.o']
output = [{{ path = 'libver.a', to = 'link' }}, {{ path = 'ver.h', to = 'link' }}]
"""

CLASH_MODULE = """
[components.{module}]
path = '{module}'
namespace = ''

[components.{module}.build]
backend = 'lua-c'
module = '{module}'
sources = ['{module}.c']
link = ['ver{n}']
"""

VER_H = "int ver_value(void);\n"

# ver_value of the version n of the library ver.
VER_C = '#include "ver.h"\nint ver_value(void) {{ return {n}; }}\n'

# A module of the library ver whose function returns ten times what
# ver_value returns, by module_value, which the other module defines as
# well.
CLASH_C = """#include <lua.h>
#include <ver.h>

int module_value(void) {{
\treturn 10 * ver_value();
}}

static int l_value(lua_State *L) {{
\tlua_pushinteger(L, module_value());
\treturn 1;
}}

int luaopen_{module}(lua_State *L) {{
\tlua_pushcfunction(L, l_value);
\treturn 1;
}}
"""


@pytest.mark.parametrize("compiler", ["cc", "zig cc"])
def test_two_modules_of_two_versions_of_a_library(tt_cmd, tmp_path, compiler):
    """Two lua-c modules each link a version of their own of the static
    library ver, whose function ver_value is of one name in both, and each
    defines module_value of its own: required in one Tarantool, in either
    order, each calls its own.

    require opens a module with dlopen(RTLD_LOCAL), so neither module sees
    the functions of the other. A module loaded into the global scope, by
    ffi.load(path, true), is seen by those loaded after it, and the module
    built with cc calls its own functions still: -Bsymbolic-functions binds
    the calls of a module of Linux to the functions it defines at its link,
    and the two-level namespace of macOS does. zig cc refuses
    -Bsymbolic-functions, and tt links its modules without it: the second
    module of zig cc calls the functions of the first loaded so, which is
    not checked here.
    """
    env = None
    if compiler != "cc":
        if shutil.which("zig") is None:
            pytest.skip("zig is not in PATH")
        env = dict(os.environ, CC=compiler)

    project = tmp_path / "project"
    project.mkdir()
    (project / "VERSION").write_text("1.0.0\n")

    manifest = CLASH_MANIFEST
    for n in (1, 2):
        manifest += CLASH_LIBRARY.format(n=n)
        (project / f"ver{n}").mkdir()
        (project / f"ver{n}" / "ver.h").write_text(VER_H)
        (project / f"ver{n}" / "ver.c").write_text(VER_C.format(n=n))

    for n, module in ((1, "one"), (2, "two")):
        manifest += CLASH_MODULE.format(n=n, module=module)
        (project / module).mkdir()
        (project / module / f"{module}.c").write_text(CLASH_C.format(module=module))

    (project / "app.manifest.toml").write_text(manifest)

    build(tt_cmd, project, env=env)

    for first, second in (("one", "two"), ("two", "one")):
        printed = tarantool(
            project,
            f"local {first} = require('{first}') local {second} = require('{second}') "
            "print(one(), two())",
        )
        assert printed.split() == ["10", "20"], f"{first} required first"

    if compiler != "cc":
        return

    lib = project / ".rocks" / "lib" / "tarantool"
    ext = ".dylib" if DARWIN else ".so"
    for first, second, value in (("one", "two", "20"), ("two", "one", "10")):
        printed = tarantool(
            project,
            f"require('ffi').load('{lib / (first + ext)}', true) print(require('{second}')())",
        )
        assert printed == value, f"{first} loaded into the global scope first"


# The CMake project of foo: its static library of position-independent code,
# or its shared library of the SONAME libfoo.so.1 (@rpath/libfoo.1.dylib on
# macOS), and its header in the build tree.
CMAKE_LISTS = """cmake_minimum_required(VERSION 3.15)
project(foo C)
add_library(foo {kind} foo.c)
set_target_properties(foo PROPERTIES POSITION_INDEPENDENT_CODE ON VERSION 1.0 SOVERSION 1)
configure_file(foo.h foo.h COPYONLY)
"""

CMAKE_BUILD = """backend = 'cmake'
output = [{{ path = '{library}', to = 'link' }}, {{ path = 'foo.h', to = 'link' }}]
"""


@pytest.mark.skipif(shutil.which("cmake") is None, reason="cmake is not in PATH")
@pytest.mark.parametrize("kind", ["STATIC", "SHARED"], ids=["static", "shared"])
def test_link_a_library_of_a_cmake_component(tt_cmd, tmp_path, kind):
    """A lua-c module links the library a cmake component built.

    The output of the cmake build, relative to its build tree, sends the
    library and its header to = "link". The module links the static library
    in; the shared one, a symbolic link of the build tree to the library of
    its version, it bundles under the SONAME, beside itself. The module loads
    from the tree of the project.
    """
    if kind == "STATIC":
        library, bundle = "libfoo.a", {}
    else:
        library = "libfoo.dylib" if DARWIN else "libfoo.so"
        bundle = {"bundle": "['foo']"}

    project = make_project(
        tmp_path / "project",
        foo_build=CMAKE_BUILD.format(library=library),
        **bundle,
    )
    (project / "foo" / "CMakeLists.txt").write_text(CMAKE_LISTS.format(kind=kind))

    build(tt_cmd, project)

    link = project / "_build" / "link" / "default" / "foo"
    assert (link / library).is_file()
    assert (link / "include" / "foo.h").is_file()

    lib = project / ".rocks" / "lib" / "tarantool"
    copies = sorted(path.name for path in lib.iterdir() if path.name.startswith("libfoo"))
    if kind == "STATIC":
        assert copies == []
    else:
        assert copies == ["libfoo.1.dylib" if DARWIN else "libfoo.so.1"]

    assert require_answer(project) == "42"


def test_link_a_component_built_after(tt_cmd, tmp_path):
    """The component a lua-c module links has to be built before it."""
    project = make_project(
        tmp_path / "project",
        STATIC_FOO,
        "[{ path = 'libfoo.a', to = 'link' }]",
    )
    manifest = project / "app.manifest.toml"
    manifest.write_text(
        manifest.read_text().replace("['foo', 'answer']", "['answer', 'foo']"),
    )

    result = run(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))
    assert result.returncode == 1
    assert (
        'products.default.components: component "answer" links the libraries of component '
        '"foo", and the product builds it after "answer": list "foo" before "answer"'
    ) in result.stderr
