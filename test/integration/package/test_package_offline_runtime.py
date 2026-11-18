"""The runtime of a project for a machine with no network.

`tt package resolve` pins the exact tarantool and tt a project runs on in its
lock. The programs here are fake installations laid as `tt binary install`
lays them, a tarantool with its headers and EmmyLua stubs and a tt, so nothing
is downloaded and nothing runs them: each command is run with every proxy
dead, and a request to any source of prebuilt archives would fail.
"""

import os
import shutil
import subprocess
import tomllib
from pathlib import Path

import pytest

pytestmark = pytest.mark.skipif(
    shutil.which("tarantool") is None,
    reason="tt package resolve needs a tarantool",
)

MANIFEST = """manifest_version = '0.1'

[package]
name = 'offline-app'

[platform]
tarantool = '{tarantool}'
tt = '>=3.0.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = 'src'
"""

# A proxy nothing listens on: any download over HTTP(S) fails at once.
DEAD_PROXY = "http://127.0.0.1:9"

# The versions of the fake installations: no real release is either, so a
# pin of one of them was taken from the project, not from a source.
TARANTOOL = "3.99.0"
TT = "3.98.0"


def offline_env(**extra: str) -> dict[str, str]:
    """The environment with every proxy dead."""
    env = dict(os.environ, **extra)
    for proxy in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY"):
        env[proxy] = DEAD_PROXY
    for bypass in ("NO_PROXY", "no_proxy"):
        env.pop(bypass, None)
    return env


def run(tt_cmd, cwd: Path, *args: str, env=None) -> subprocess.CompletedProcess:
    return subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        env=env if env is not None else offline_env(),
    )


def make_project(root: Path, tarantool: str = ">=3.0.0") -> Path:
    (root / "src").mkdir(parents=True)
    (root / "src" / "init.lua").write_text("return {}\n")
    (root / "app.manifest.toml").write_text(MANIFEST.format(tarantool=tarantool))
    return root


def install_fake(project: Path, product: str, version: str) -> Path:
    """Lay an installation of product into the project scope as tt binary
    install does: the executable, the license files and, of tarantool, the
    headers and the EmmyLua stubs of share/tarantool."""
    installation = project / ".tarantool" / "versions" / product / f"{product}_{version}"
    installation.mkdir(parents=True)
    executable = installation / ("tarantool" if product == "tarantool" else "tt")
    executable.write_text(f"#!/bin/sh\necho '{product} {version}'\n")
    executable.chmod(0o755)
    (installation / "LICENSE").write_text(f"the license of {product}\n")
    if product == "tarantool":
        (installation / "AUTHORS").write_text("the authors of tarantool\n")
        headers = installation / "include" / "tarantool"
        headers.mkdir(parents=True)
        (headers / "module.h").write_text("/* module.h */\n")
        stubs = installation / "share" / "tarantool" / "emmylua"
        stubs.mkdir(parents=True)
        (stubs / "box.lua").write_text("---@meta\n")
    return installation


def read_lock(project: Path) -> dict:
    with (project / "app.manifest.lock").open("rb") as handle:
        return tomllib.load(handle)


@pytest.fixture()
def project(tmp_path: Path) -> Path:
    """A project with a tarantool and a tt installed into it."""
    root = make_project(tmp_path / "project")
    install_fake(root, "tarantool", TARANTOOL)
    install_fake(root, "tt", TT)
    return root


def test_resolve_pins_the_installed_runtime(tt_cmd, project):
    """The installations of the project are pinned, with no network; a
    second resolve keeps the pins and finds the lock up to date."""
    resolved = run(tt_cmd, project, "package", "resolve")
    assert resolved.returncode == 0, resolved.stderr
    assert "pins no" not in resolved.stderr

    lock = read_lock(project)
    assert lock["tarantool_version"] == TARANTOOL
    assert lock["tt_version"] == TT
    assert "tcm_version" not in lock
    assert not any(key.startswith("bundled_") for key in lock)

    again = run(tt_cmd, project, "package", "resolve")
    assert again.returncode == 0, again.stderr
    assert again.stdout.strip() == "the lock is up to date"


def test_resolve_keeps_a_pin_and_drops_one_that_no_longer_fits(tt_cmd, project):
    """A pin that still satisfies [platform] is kept over the installation;
    one that does not is pinned anew, from the project."""
    resolved = run(tt_cmd, project, "package", "resolve")
    assert resolved.returncode == 0, resolved.stderr

    install_fake(project, "tarantool", "3.99.5")
    kept = run(tt_cmd, project, "package", "resolve")
    assert kept.returncode == 0, kept.stderr
    assert read_lock(project)["tarantool_version"] == TARANTOOL

    manifest = project / "app.manifest.toml"
    manifest.write_text(manifest.read_text().replace("'>=3.0.0'\ntt", "'>=3.99.1'\ntt"))
    moved = run(tt_cmd, project, "package", "resolve")
    assert moved.returncode == 0, moved.stderr
    assert read_lock(project)["tarantool_version"] == "3.99.5"
    assert 'tarantool_version is "3.99.5", was "3.99.0"' in moved.stdout


def test_resolve_without_a_version_to_pin_warns(tt_cmd, tmp_path):
    """Nothing satisfies the constraint, and no source answers: resolve
    succeeds, warns and leaves the pin out."""
    root = make_project(tmp_path / "project", tarantool=">=3.99.0")

    resolved = run(tt_cmd, root, "package", "resolve")
    assert resolved.returncode == 0, resolved.stderr
    assert "app.manifest.lock pins no tarantool" in resolved.stderr
    assert "tarantool_version" not in read_lock(root)
