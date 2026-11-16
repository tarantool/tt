"""`tt run`: the project's Tarantool, with the arguments passed straight on."""

import os
import shutil
import subprocess
import sys
from pathlib import Path

import pytest


def test_run_needs_a_manifest(run_tt, tmp_path_factory: pytest.TempPathFactory):
    """A directory with no package is an error, not an empty success.

    The manifest is the only thing the command requires, so this is also the
    assertion that it requires nothing else. The directory is outside the
    project of the fixture: inside it, the run would find that project.
    """
    elsewhere = tmp_path_factory.mktemp("elsewhere")

    result = run_tt("run", "-e", "print(1)", cwd=elsewhere)

    assert result.returncode == 1
    assert "app.manifest.toml" in result.stderr


def test_run_needs_no_tt_environment(run_tt):
    """The project fixture writes no tt.yaml, and that is the point."""
    result = run_tt("run", "-e", "print('ran')")

    assert result.returncode == 0, result.stderr
    assert "ran" in result.stdout
    assert "tt.yaml" not in result.stderr


def test_run_passes_arguments_through(run_tt, project: Path):
    (project / "script.lua").write_text("print(table.concat(arg, ','))\n")

    result = run_tt("run", "script.lua", "a", "b", "c")

    assert result.returncode == 0, result.stderr
    assert "a,b,c" in result.stdout


def test_run_passes_the_double_dash_through(run_tt, project: Path):
    """'--' is Tarantool's to interpret, so tt must not eat it.

    Cobra leaves it in place under DisableFlagParsing; this pins that it still
    arrives, because a version that swallowed it would silently change how every
    script sees its own arguments.
    """
    (project / "script.lua").write_text("print(table.concat(arg, ','))\n")

    result = run_tt("run", "script.lua", "--", "-x")

    assert result.returncode == 0, result.stderr
    assert "--,-x" in result.stdout


def test_run_reads_stdin(run_tt):
    result = run_tt("run", "-", stdin="print(42)\n")

    assert result.returncode == 0, result.stderr
    assert "42" in result.stdout


def test_run_returns_tarantools_exit_code(run_tt):
    """tt replaces itself with Tarantool, so the code is Tarantool's own."""
    result = run_tt("run", "-e", "os.exit(7)")

    assert result.returncode == 7


def test_run_prefers_the_project_tarantool(run_tt, project_tarantool):
    """The tarantool of the project is what the project chose to run under.

    The fake prints a marker, so this fails loudly if the host's interpreter is
    chosen instead — which is the whole priority rule.
    """
    project_tarantool()

    result = run_tt("run", "hello")

    assert result.returncode == 0, result.stderr
    assert "PROJECT" in result.stdout
    assert "arg:hello" in result.stdout


def test_use_system_tarantool_inverts_the_priority(run_tt, project_tarantool):
    project_tarantool()

    result = run_tt("run", "-e", "print('SYSTEM')", env={"TT_USE_SYSTEM_TARANTOOL": "1"})

    assert result.returncode == 0, result.stderr
    assert "SYSTEM" in result.stdout
    assert "PROJECT" not in result.stdout


def test_run_works_in_the_project_root(run_tt, project: Path):
    """Started in the root, Tarantool runs in the root.

    Asserting on the working directory Tarantool reports is the only way to see
    this from outside.
    """
    (project / "where.lua").write_text("print(require('fio').cwd())\n")

    result = run_tt("run", "where.lua")

    assert result.returncode == 0, result.stderr
    # macOS resolves /tmp through a symlink, so compare resolved paths.
    assert Path(result.stdout.strip()).resolve() == project.resolve()


def test_run_works_in_the_directory_it_is_started_in(run_tt, project: Path):
    """A run started in a subdirectory runs the project there, not in its root.

    The project is found as the instance commands find theirs, and the command
    says which one it is, since the package that runs is not the directory the
    shell is in. Tarantool itself runs where the shell is, as `tarantool`
    started there by hand would.
    """
    subdirectory = project / "sub"
    subdirectory.mkdir()

    result = run_tt("run", "-e", "print(require('fio').cwd()) os.exit()", cwd=subdirectory)

    assert result.returncode == 0, result.stderr
    assert Path(result.stdout.strip()).resolve() == subdirectory.resolve()
    _, said, named = result.stderr.partition("working on the project in ")
    assert said, result.stderr
    assert Path(named.splitlines()[0].strip()).resolve() == project.resolve()


def test_run_takes_a_script_path_relative_to_the_working_directory(run_tt, project: Path):
    """`tt run ../script.lua` in a subdirectory is the script of its parent.

    A run that moved into the root first could not open it: the path would
    then point above the project.
    """
    (project / "script.lua").write_text("print('parent script', arg[1])\n")
    (project / "sub").mkdir()
    (project / "sub" / "script.lua").write_text("print('sub script')\n")

    result = run_tt("run", "../script.lua", "x", cwd=project / "sub")

    assert result.returncode == 0, result.stderr
    assert "parent script\tx" in result.stdout

    result = run_tt("run", "script.lua", cwd=project / "sub")

    assert result.returncode == 0, result.stderr
    assert "sub script" in result.stdout


MODULES_SCRIPT = """
for _, name in ipairs({'dep', 'my-app.mod', 'cmod', 'kv.foo'}) do
    local ok, value = pcall(require, name)
    print(name .. '=' .. (ok and tostring(value) or 'FAILED'))
end
os.exit(0)
"""

CMOD_SOURCE = """
typedef struct lua_State lua_State;
extern void lua_pushstring(lua_State *L, const char *s);
int luaopen_cmod(lua_State *L) { lua_pushstring(L, "cmod"); return 1; }
"""


@pytest.fixture()
def project_modules(project: Path) -> Path:
    """The project's modules of every kind, as a build lays them out.

    A dependency and the package itself as built, in .rocks/; a C module in
    .rocks/; and a source module of the root, which only the root itself
    holds. The C module is compiled against no headers: its two symbols are
    declared in place, and Tarantool resolves lua_pushstring when it loads it.
    """
    share = project / ".rocks" / "share" / "tarantool"
    (share / "my-app").mkdir(parents=True)
    (share / "dep.lua").write_text("return 'dep'\n")
    (share / "my-app" / "mod.lua").write_text("return 'pkgmod'\n")
    (project / "kv").mkdir()
    (project / "kv" / "foo.lua").write_text("return 'rootmod'\n")

    compiler = shutil.which("cc") or shutil.which("gcc") or shutil.which("clang")
    if compiler is None:
        pytest.skip("no C compiler")

    lib = project / ".rocks" / "lib" / "tarantool"
    lib.mkdir(parents=True)
    source = project / "cmod.c"
    source.write_text(CMOD_SOURCE)
    ext = ".dylib" if sys.platform == "darwin" else ".so"
    flags = ["-shared", "-fPIC"]
    if sys.platform == "darwin":
        flags += ["-undefined", "dynamic_lookup"]
    subprocess.run([compiler, *flags, str(source), "-o", str(lib / f"cmod{ext}")], check=True)
    source.unlink()

    return project


EXPECTED_MODULES = ["dep=dep", "my-app.mod=pkgmod", "cmod=cmod", "kv.foo=rootmod"]


@pytest.mark.parametrize("depth", ["", "sub", "sub/deep"])
@pytest.mark.parametrize("how", ["-e", "stdin", "script here", "script of the root"])
def test_run_resolves_the_project_modules_from_any_directory(
    run_tt,
    project_modules: Path,
    depth: str,
    how: str,
):
    """Every kind of module of the project resolves wherever tt run is started.

    Tarantool finds .rocks/ upward from its working directory by itself; the
    sources of the root are found below it through the LUA_PATH tt composes.
    """
    cwd = project_modules / depth
    cwd.mkdir(parents=True, exist_ok=True)

    if how == "-e":
        result = run_tt("run", "-e", MODULES_SCRIPT, cwd=cwd, stdin="")
    elif how == "stdin":
        result = run_tt("run", "-", cwd=cwd, stdin=MODULES_SCRIPT)
    elif how == "script here":
        (cwd / "here.lua").write_text(MODULES_SCRIPT)
        result = run_tt("run", "here.lua", cwd=cwd)
    else:
        (project_modules / "modules.lua").write_text(MODULES_SCRIPT)
        result = run_tt("run", str(project_modules / "modules.lua"), cwd=cwd)

    assert result.returncode == 0, result.stderr
    assert result.stdout.split() == EXPECTED_MODULES


def test_run_keeps_the_environment_in_the_root(run_tt, project: Path):
    """In the root nothing is composed: the run is `tarantool` there."""
    script = "print(os.getenv('LUA_PATH') or 'unset') os.exit()"

    result = run_tt("run", "-e", script, stdin="")
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == (os.environ.get("LUA_PATH") or "unset")

    (project / "sub").mkdir()
    result = run_tt("run", "-e", script, cwd=project / "sub", stdin="")
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip().startswith(str(project / "?.lua") + ";")


def test_run_help(run_tt):
    """-h and --help alone are tt's: they print the help of tt run."""
    for flag in ("-h", "--help"):
        result = run_tt("run", flag)

        assert result.returncode == 0, result.stderr
        assert "tt run [TARANTOOL-OPTIONS...] [SCRIPT [ARGS...]]" in result.stdout


def test_run_hands_help_after_the_script_to_the_script(run_tt, project: Path):
    """#1213: an -h of the script's is the script's, '--' or not."""
    (project / "script.lua").write_text("print(table.concat(arg, ','))\n")

    for args, printed in (
        (["script.lua", "-h"], "-h"),
        (["script.lua", "--help", "x"], "--help,x"),
        (["script.lua", "--", "-h"], "--,-h"),
    ):
        result = run_tt("run", *args)

        assert result.returncode == 0, result.stderr
        assert result.stdout.strip() == printed
        assert "TARANTOOL-OPTIONS" not in result.stdout


def test_run_drops_a_double_dash_before_the_script(run_tt, project: Path):
    """'--' before the script ends tt's options and is not passed on."""
    (project / "script.lua").write_text("print(table.concat(arg, ','))\n")

    result = run_tt("run", "--", "script.lua", "-h")

    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == "-h"


def test_run_hands_help_after_a_double_dash_to_tarantool(run_tt, project_tarantool):
    """`tt run -- -h` asks Tarantool, not tt, for its help."""
    project_tarantool()

    result = run_tt("run", "--", "-h")

    assert result.returncode == 0, result.stderr
    assert result.stdout.split() == ["PROJECT", "arg:-h"]


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_run_without_any_tarantool_names_both_places(run_tt):
    """Which of the two places was empty is the whole content of the answer."""
    result = run_tt("run", "--version")

    assert result.returncode == 1
    assert ".tarantool/bin/tarantool" in result.stderr
    assert "PATH" in result.stderr
