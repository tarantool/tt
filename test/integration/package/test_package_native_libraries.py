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
import shutil
import subprocess
from pathlib import Path

import pytest

ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

pytestmark = pytest.mark.skipif(
    shutil.which("cc") is None or shutil.which("ar") is None or shutil.which("tarantool") is None,
    reason="no cc, ar or tarantool",
)

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
backend = 'shell'
command = 'sh'
args = ['-c', '{script}']
output = {output}

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


def run(tt_cmd, cwd: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run([str(tt_cmd), *args], cwd=cwd, capture_output=True, text=True)


def make_project(root: Path, script: str, output: str, static: bool = False, **extra) -> Path:
    """Write the package of foo and answer; extra adds lines to the block of
    answer."""
    (root / "foo").mkdir(parents=True)
    (root / "foo" / "foo.c").write_text(FOO_C)
    (root / "foo" / "foo.h").write_text(FOO_H)
    (root / "answer").mkdir()
    (root / "answer" / "answer.c").write_text(ANSWER_C)
    (root / "VERSION").write_text("1.0.0\n")

    manifest = MANIFEST.format(script=script, output=output, static=str(static).lower())
    for key, value in extra.items():
        manifest += f"{key} = {value}\n"

    (root / "app.manifest.toml").write_text(manifest)
    return root


def build(tt_cmd, project: Path) -> None:
    """Build the package, skipping the test for a tarantool without headers."""
    result = run(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))
    if "tarantool development headers not found" in result.stderr:
        pytest.skip("the tarantool tt finds has no headers")

    assert result.returncode == 0, result.stderr


def require_answer(cwd: Path) -> str:
    """Load the module answer from .rocks of cwd in Tarantool, with no
    LD_LIBRARY_PATH, and return what its function returns."""
    env = {
        key: value
        for key, value in os.environ.items()
        if key not in ("LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH")
    }
    result = subprocess.run(
        ["tarantool", "-e", "print(require('answer')()) os.exit(0)"],
        cwd=cwd,
        capture_output=True,
        text=True,
        env=env,
    )
    assert result.returncode == 0, result.stderr
    return result.stdout.strip()


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
    finds the library beside itself.
    """
    project = make_project(
        tmp_path / "project",
        "cc -shared -fPIC -o libfoo.so foo.c -Wl,-soname,libfoo.so.1",
        "[{ path = 'libfoo.so', to = 'link' }, { path = 'foo.h', to = 'link' }]",
        static=static,
        bundle="['foo']",
    )

    build(tt_cmd, project)

    lib = project / ".rocks" / "lib" / "tarantool"
    assert (lib / "answer.so").is_file()
    assert (lib / "libfoo.so.1").is_file()
    assert require_answer(project) == "42"

    pack = ("package", "pack", "--without-deps", "--registry", str(ROCKS_REPO))
    result = run(tt_cmd, project, *pack)
    assert result.returncode == 0, result.stderr

    archives = list((project / "_build" / "pack").glob("*.tt"))
    assert len(archives) == 1, archives

    archive = shutil.move(archives[0], tmp_path)
    target = tmp_path / "target"
    target.mkdir()
    shutil.rmtree(project)

    result = subprocess.run(
        [str(tt_cmd), "package", "install", str(archive)],
        cwd=target,
        capture_output=True,
        text=True,
        env={**os.environ, "TT_REGISTRIES": str(ROCKS_REPO)},
    )
    assert result.returncode == 0, result.stderr

    installed = target / ".rocks" / "lib" / "tarantool"
    assert (installed / "libfoo.so.1").is_file()
    assert require_answer(target) == "42"


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
