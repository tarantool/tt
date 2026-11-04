"""A dependency declared with a registry of its own comes from that registry.

The registry key of a dependency used to steer the resolution only: tt package
build installed the rock from the configured registries, which need not have it,
and a directory in the key was taken relative to the directory tt ran in. Every
test here configures an empty registry with TT_REGISTRIES, so a rock that is
installed can only have come from the one its declaration names.
"""

import os
import shutil
import subprocess
from pathlib import Path

ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

# What the fixture repository's stat holds, unlike the public one.
STAT_STUB = 'return "Hello, world!"\n'

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.lua]
path = '.'

[dependencies]
stat = {{ version = '>=0.3.1', registry = '{registry}' }}
"""


def empty_registry(root: Path) -> Path:
    """A rock server that serves nothing."""
    root.mkdir(parents=True)
    (root / "manifest").write_text("commands = {}\nmodules = {}\nrepository = {}\n")
    return root


def make_project(root: Path, registry: str) -> Path:
    """A package whose one dependency is stat from registry."""
    root.mkdir(parents=True)
    (root / "app.manifest.toml").write_text(MANIFEST.format(registry=registry))
    (root / "init.lua").write_text("return {}\n")
    return root


def run(tt_cmd: Path, cwd: Path, *args: str, empty: Path) -> subprocess.CompletedProcess:
    """Run tt with no registry configured but the empty one."""
    return subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        env={**os.environ, "TT_REGISTRIES": str(empty)},
    )


def installed_stat(project: Path) -> str:
    return (project / ".rocks" / "share" / "tarantool" / "stat" / "init.lua").read_text()


def test_build_installs_from_the_declared_server(tt_cmd, tmp_path, rock_server):
    project = make_project(tmp_path / "project", rock_server)
    empty = empty_registry(tmp_path / "empty")

    result = run(tt_cmd, project, "package", "build", empty=empty)

    assert result.returncode == 0, result.stderr
    assert installed_stat(project) == STAT_STUB


def test_fetch_installs_from_the_declared_server(tt_cmd, tmp_path, rock_server):
    project = make_project(tmp_path / "project", rock_server)
    empty = empty_registry(tmp_path / "empty")

    resolved = run(tt_cmd, project, "package", "resolve", empty=empty)
    assert resolved.returncode == 0, resolved.stderr

    result = run(tt_cmd, project, "package", "fetch", empty=empty)

    assert result.returncode == 0, result.stderr
    assert installed_stat(project) == STAT_STUB


def test_a_registry_directory_is_relative_to_the_manifest(tt_cmd, tmp_path):
    """-C from elsewhere finds the directory next to the manifest."""
    project = make_project(tmp_path / "project", "vendor-rocks")
    shutil.copytree(ROCKS_REPO, project / "vendor-rocks")
    empty = empty_registry(tmp_path / "empty")
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()

    result = run(tt_cmd, elsewhere, "package", "build", "-C", str(project), empty=empty)

    assert result.returncode == 0, result.stderr
    assert installed_stat(project) == STAT_STUB
