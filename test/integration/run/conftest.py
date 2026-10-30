"""Fixtures shared by the `tt run` and `tt test` suites.

Both commands are the same machinery: find the project, choose an interpreter,
replace the process with it. `tt test` adds a build and a runner script on top,
so it needs the same project and the same fake interpreter to be driven
offline.
"""

import os
import stat
import subprocess
from pathlib import Path

import pytest

MANIFEST = """manifest_version = '0.1'

[package]
name = '{name}'
description = '{name} description'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
"""


def write_executable(path: Path, body: str) -> None:
    """Write a script and make it executable.

    The interpreter selection reads the executable bit, so a fake that is not
    executable is skipped rather than chosen — which would make every priority
    assertion pass for the wrong reason.
    """
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(body)
    path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


@pytest.fixture()
def project(tmp_path: Path) -> Path:
    """A package directory: a manifest and nothing else.

    Deliberately without a tt.yaml. Both commands are supposed to work outside a
    tt environment, and a config in the directory would hide a regression that
    reintroduced the dependency on one.
    """
    (tmp_path / "app.manifest.toml").write_text(MANIFEST.format(name="my-app"))
    return tmp_path


@pytest.fixture()
def project_tarantool(project: Path):
    """Install a fake interpreter as the project's tarantool.

    tt binary install makes .tarantool/bin/tarantool a link into
    .tarantool/versions/, and so does a with-deps tt package install for the
    runtime it bundles; an executable put there by hand is chosen the same
    way, only its version is unknown.

    It prints a marker and its arguments, so which interpreter ran and what it
    was handed are both readable from stdout. Asked for --version it answers
    as a release build does, since tt asks the tarantool it runs for its
    version and install prefix before building the package.
    """

    def install(marker: str = "PROJECT") -> Path:
        path = project / ".tarantool" / "bin" / "tarantool"
        write_executable(
            path,
            "#!/bin/sh\n"
            'if [ "$1" = "--version" ]; then\n'
            "  echo 'Tarantool 3.8.1-0-ge643f88a5e0c'\n"
            "  echo 'Target: Linux-x86_64-RelWithDebInfo'\n"
            "  echo 'Build options: cmake . -DCMAKE_INSTALL_PREFIX=/usr -DENABLE_BACKTRACE=TRUE'\n"
            "  exit 0\n"
            "fi\n"
            f'echo "{marker}"\nfor a in "$@"; do echo "arg:$a"; done\n',
        )
        return path

    return install


@pytest.fixture()
def fake_luatest(project: Path):
    """Install a fake luatest into the project's rocks tree.

    This is what keeps the `tt test` suite offline: with a runner already in the
    tree, `tt test` neither resolves nor fetches one, so the whole command runs
    without a registry. The script is real Lua, run by the real Tarantool the
    command selected, so the invocation being asserted is the actual one.

    It exits 1 when handed the sentinel argument, which is how the exit-code
    assertion gets a failure without a failing test.
    """

    def install(version: str = "1.0.0-1") -> Path:
        path = (
            project
            / ".rocks"
            / "share"
            / "tarantool"
            / "rocks"
            / "luatest"
            / version
            / "bin"
            / "luatest"
        )
        write_executable(
            path,
            "#!/usr/bin/env tarantool\n"
            "print('FAKE-LUATEST')\n"
            "for _, value in ipairs(arg) do print('arg:' .. value) end\n"
            "for _, value in ipairs(arg) do\n"
            "    if value == '--fail' then os.exit(3) end\n"
            "end\n"
            "os.exit(0)\n",
        )
        return path

    return install


@pytest.fixture()
def run_tt(tt_cmd, project: Path):
    """Run `tt` in the project and return the completed process."""

    def run(
        *args: str,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        stdin: str | None = None,
    ) -> subprocess.CompletedProcess:
        """`env` is merged into the inherited environment, not a replacement.

        The commands under test are supposed to inherit the host environment
        unchanged, so replacing it outright would test a situation tt never
        creates.
        """
        environment = os.environ.copy()
        if env:
            environment.update(env)

        return subprocess.run(
            [str(tt_cmd), *args],
            cwd=cwd or project,
            input=stdin,
            capture_output=True,
            text=True,
            env=environment,
        )

    return run
