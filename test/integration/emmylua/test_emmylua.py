"""tt emmylua without the programs of EmmyLua: what tt says and writes itself.

config writes the configuration EmmyLua runs with and needs no program, so the
tests read what it writes: what git ignores and the directories of tt left out
of the code, the stub of luatest read in place of its entry module. A fake
tarantool without stubs stands for an old Tarantool.
"""

import json
import os
import subprocess
from pathlib import Path

import pytest

FAKE_TARANTOOL = """#!/bin/sh
echo "Tarantool 3.8.1-0-ge643f88a5e0c"
echo "Target: Linux-x86_64-RelWithDebInfo"
"""


def run_tt(tt_cmd: Path, cwd: Path, *args: str, env=None) -> subprocess.CompletedProcess:
    return subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        env=env,
        capture_output=True,
        text=True,
        timeout=120,
    )


def git(cwd: Path, *args: str) -> None:
    subprocess.run(
        ["git", "-c", "init.defaultBranch=master", *args],
        cwd=cwd,
        check=True,
        capture_output=True,
    )


@pytest.fixture()
def tt_env(tmp_path: Path) -> dict:
    """An environment with a user cache of its own and no git config of the user."""
    env = os.environ.copy()
    env["XDG_CACHE_HOME"] = str(tmp_path / "cache")
    env["GIT_CONFIG_GLOBAL"] = os.devnull
    env["GIT_CONFIG_NOSYSTEM"] = "1"
    env["GIT_CEILING_DIRECTORIES"] = str(tmp_path)
    return env


@pytest.fixture()
def project(tt_cmd: Path, tmp_path: Path, tt_env: dict) -> Path:
    """A project in a git work tree, with tracked, untracked and ignored Lua."""
    root = tmp_path / "proj"
    root.mkdir()
    result = run_tt(tt_cmd, root, "new", env=tt_env)
    assert result.returncode == 0, result.stderr
    git(root, "init", "-q")
    (root / ".gitignore").write_text("scratch/\n*.tmp.lua\n")
    for name in ["app/init.lua", "app/draft.tmp.lua", "scratch/try.lua", "_build/rock/x.lua"]:
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("return {}\n")
    return root


def workspace(config: Path) -> dict:
    return json.loads(config.read_text())["workspace"]


def test_config_leaves_out_what_git_ignores(tt_cmd, project, tt_env):
    result = run_tt(tt_cmd, project, "emmylua", "config", env=tt_env)
    assert result.returncode == 0, result.stderr

    config = Path(result.stdout.strip())
    assert config == project / ".tarantool" / "emmylua" / "emmyrc.json"
    assert workspace(config)["ignoreDir"] == [
        str(project / ".rocks"),
        str(project / ".tarantool"),
        str(project / "_build"),
        str(project / "app" / "draft.tmp.lua"),
        str(project / "scratch"),
    ]


def test_config_reads_the_stub_of_luatest(tt_cmd, project, tt_env):
    rocks = project / ".rocks" / "share" / "tarantool"
    (rocks / "luatest").mkdir(parents=True)
    (rocks / "luatest" / "init.lua").write_text("return {}\n")
    (rocks / "luatest" / "assertions.lua").write_text("return {}\n")

    result = run_tt(tt_cmd, project, "emmylua", "config", env=tt_env)
    assert result.returncode == 0, result.stderr

    config = Path(result.stdout.strip())
    stubs = config.parent / "stubs"
    assert workspace(config)["library"][0] == str(stubs)
    assert str(rocks) in workspace(config)["library"]
    assert str(rocks / "luatest" / "init.lua") in workspace(config)["ignoreDir"]
    stub = (stubs / "luatest" / "init.lua").read_text()
    assert "local luatest = require('luatest.assertions')" in stub


def test_no_stubs_is_said_once(tt_cmd, project, tt_env, tmp_path):
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    tarantool = bin_dir / "tarantool"
    tarantool.write_text(FAKE_TARANTOOL)
    tarantool.chmod(0o755)
    tt_env["PATH"] = f"{bin_dir}{os.pathsep}{tt_env['PATH']}"

    no_stubs = "Tarantool 3.8.1 has no EmmyLua stubs"

    first = run_tt(tt_cmd, project, "emmylua", "config", env=tt_env)
    assert first.returncode == 0, first.stderr
    assert no_stubs in first.stderr
    assert "said once for this Tarantool" in first.stderr

    second = run_tt(tt_cmd, project, "emmylua", "config", env=tt_env)
    assert second.returncode == 0, second.stderr
    assert no_stubs not in second.stderr

    verbose = run_tt(tt_cmd, project, "-V", "emmylua", "config", env=tt_env)
    assert verbose.returncode == 0, verbose.stderr
    assert no_stubs in verbose.stderr
    assert "said once" not in verbose.stderr


def test_verbose_names_no_empty_tcm(tt_cmd, project, tt_env):
    result = run_tt(tt_cmd, project, "-V", "emmylua", "config", env=tt_env)
    assert result.returncode == 0, result.stderr
    assert 'tcm executable found: ""' not in result.stderr


def test_rock_of_a_release_without_builds(tt_cmd, tmp_path, tt_env):
    result = run_tt(
        tt_cmd,
        tmp_path,
        "emmylua",
        "rock",
        "--version",
        "0.30.0pre1",
        "--out",
        str(tmp_path / "rocks"),
        env=tt_env,
    )
    assert result.returncode != 0
    assert "no builds to download for EmmyLua 0.30.0pre1" in result.stderr
    assert "unsupported platform" not in result.stderr
