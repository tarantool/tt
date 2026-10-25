"""System-scope coverage for `tt package list` and `tt package uninstall`.

The system scope lays an application out the way `tt binary` lays out a
program (decision 8): every installed version in
`<root>/versions/<name>/<name>_<version>/` and the active one selected by the
link `<root>/apps/<name>`. `TT_SYSTEM_ROOT` moves the root into the test's
directory, so these run on the host, without the `/opt/tarantool` no test may
touch.

The applications are packed and installed for real, `--without-deps`, which
needs no network and no runtime: list and uninstall read what install wrote.
The run of their instances is in `integration/scopes`.
"""

import json
import os
import subprocess
from pathlib import Path

import pytest

MANIFEST = """manifest_version = '0.1'

[package]
name = '{name}'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = 'src'
"""


def run(tt_cmd, cwd: Path, env: dict, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        env=env,
        capture_output=True,
        text=True,
    )


def pack(tt_cmd, path: Path, name: str, version: str) -> Path:
    """Pack the application name of version, written into path, and return
    the archive."""
    (path / "src").mkdir(parents=True)
    (path / "src" / "init.lua").write_text("return {}\n")
    (path / "VERSION").write_text(version + "\n")
    (path / "app.manifest.toml").write_text(MANIFEST.format(name=name))

    result = run(tt_cmd, path, None, "package", "pack", "--without-deps")
    assert result.returncode == 0, result.stderr
    archives = list((path / "_build" / "pack").glob("*.tt"))
    assert len(archives) == 1, archives
    return archives[0]


class SystemScope:
    """The system scope of a root in the test's directory, with two
    applications installed: monitoring 1.0.0 and 2.0.0, and alerting 0.5.0."""

    def __init__(self, tt_cmd, tmp_path: Path):
        self.tt_cmd = tt_cmd
        self.root = tmp_path / "root"
        self.cwd = tmp_path / "cwd"
        self.cwd.mkdir()
        self.env = dict(
            os.environ,
            TT_SYSTEM_ROOT=str(self.root),
            TT_USER_ROOT=str(tmp_path / "user"),
        )

        for name, version in [
            ("monitoring", "1.0.0"),
            ("monitoring", "2.0.0"),
            ("alerting", "0.5.0"),
        ]:
            archive = pack(tt_cmd, tmp_path / "src" / f"{name}-{version}", name, version)
            result = self.tt("package", "install", "--scope", "system", str(archive))
            assert result.returncode == 0, result.stderr

    def tt(self, *args: str) -> subprocess.CompletedProcess:
        return run(self.tt_cmd, self.cwd, self.env, *args)

    def version(self, name: str, version: str) -> Path:
        return self.root / "versions" / name / f"{name}_{version}"


@pytest.fixture
def system_scope(tt_cmd, tmp_path) -> SystemScope:
    return SystemScope(tt_cmd, tmp_path)


def test_system_scope_list(system_scope):
    """`--scope system` lists the applications of the root, not the working
    directory's .rocks/: every installed version and the active one."""
    result = system_scope.tt("package", "list", "--scope", "system", "-o", "json")
    assert result.returncode == 0, result.stderr

    payload = json.loads(result.stdout)
    assert payload["scope"] == "system"
    assert payload["root"] == str(system_scope.root)
    assert payload["packages"] == [
        {"name": "alerting", "version": "0.5.0", "versions": ["0.5.0"], "primary": False},
        {
            "name": "monitoring",
            "version": "2.0.0",
            "versions": ["1.0.0", "2.0.0"],
            "primary": False,
        },
    ]

    # The versions share no rocks, so there is no tree to show.
    result = system_scope.tt("package", "list", "--scope", "system", "--tree")
    assert result.returncode == 1
    assert "--tree applies to the project scope only" in result.stderr


def test_system_scope_uninstall(system_scope):
    """Removal in the system scope takes every version of the application and
    its link, and leaves the other application, the data and the host files
    alone."""
    root = system_scope.root
    for kept in [
        root / "var" / "lib" / "monitoring" / "storage" / "00000000000000000000.snap",
        root / "etc" / "monitoring" / "instances.yaml",
    ]:
        kept.parent.mkdir(parents=True)
        kept.write_text("kept\n")

    result = system_scope.tt("package", "uninstall", "monitoring", "--scope", "system", "--yes")
    assert result.returncode == 0, result.stderr
    assert "removed monitoring from system scope: versions 1.0.0, 2.0.0" in result.stdout

    assert not (root / "versions" / "monitoring").exists()
    assert not os.path.lexists(root / "apps" / "monitoring")
    assert system_scope.version("alerting", "0.5.0").is_dir()
    assert os.readlink(root / "apps" / "alerting") == os.path.join(
        "..",
        "versions",
        "alerting",
        "alerting_0.5.0",
    )
    assert (root / "var" / "lib" / "monitoring" / "storage").is_dir()
    assert (root / "etc" / "monitoring" / "instances.yaml").read_text() == "kept\n"


def test_system_scope_uninstall_unknown_package(system_scope):
    """A miss in the system scope is exit 1, and touches nothing."""
    result = system_scope.tt("package", "uninstall", "nosuch", "--scope", "system", "--yes")
    assert result.returncode == 1
    assert 'package is not installed: "nosuch" in system scope' in result.stderr

    assert system_scope.version("monitoring", "1.0.0").is_dir()
    assert system_scope.version("monitoring", "2.0.0").is_dir()
    assert system_scope.version("alerting", "0.5.0").is_dir()
