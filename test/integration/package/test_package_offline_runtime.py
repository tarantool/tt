"""The runtime of a project for a machine with no network.

`tt package resolve` pins the exact tarantool and tt a project runs on in its
lock, and `tt package download` puts exactly those into binaries/ of the
mirror. The programs here are fake installations laid as `tt binary install`
lays them, a tarantool with its headers and EmmyLua stubs and a tt, so nothing
is downloaded and nothing runs them: each command is run with every proxy
dead, and a request to any source of prebuilt archives would fail.
"""

import hashlib
import json
import os
import platform
import shutil
import subprocess
import tarfile
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


def sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def tar_names(archive: Path) -> dict[str, bytes]:
    """The files of a gzip-compressed tar, by name."""
    with tarfile.open(archive, "r:gz") as tar:
        return {member.name: tar.extractfile(member).read() for member in tar if member.isfile()}


def download(tt_cmd, project: Path, mirror: Path) -> subprocess.CompletedProcess:
    return run(tt_cmd, project, "package", "download", "--dir", str(mirror))


def read_index(mirror: Path) -> list[dict]:
    index = json.loads((mirror / "binaries" / "tt-binaries-index.json").read_text())
    assert index["format"] == 1
    return index["binaries"]


def host_platform() -> tuple[str, str]:
    goos = {"Linux": "linux", "Darwin": "darwin"}[platform.system()]
    goarch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[
        platform.machine().lower()
    ]
    return goos, goarch


@pytest.fixture()
def mirror(tt_cmd, project: Path, tmp_path: Path) -> Path:
    """The mirror tt package download makes of the resolved project."""
    resolved = run(tt_cmd, project, "package", "resolve")
    assert resolved.returncode == 0, resolved.stderr

    mirror = tmp_path / "mirror"
    downloaded = download(tt_cmd, project, mirror)
    assert downloaded.returncode == 0, downloaded.stderr
    return mirror


def test_download_mirrors_the_pinned_runtime(tt_cmd, project, mirror):
    """The pinned tarantool and tt go into binaries/ of the mirror, repacked
    from the installations of the project, with their sha256 in the index; a
    tarantool with its headers, EmmyLua stubs and license files."""
    goos, goarch = host_platform()
    tarantool = f"tarantool-{TARANTOOL}-{goos}-{goarch}.tar.gz"
    tt = f"tt-{TT}-{goos}-{goarch}.tar.gz"

    entries = read_index(mirror)
    assert [(entry["product"], entry["version"], entry["file"]) for entry in entries] == [
        ("tarantool", TARANTOOL, tarantool),
        ("tt", TT, tt),
    ]
    for entry in entries:
        archive = mirror / "binaries" / entry["file"]
        assert (entry["os"], entry["arch"]) == (goos, goarch)
        assert entry["sha256"] == sha256_of(archive)
        assert entry["size"] == archive.stat().st_size

    files = tar_names(mirror / "binaries" / tarantool)
    assert files["include/tarantool/module.h"] == b"/* module.h */\n"
    assert files["share/tarantool/emmylua/box.lua"] == b"---@meta\n"
    assert files["LICENSE"] == b"the license of tarantool\n"
    assert files["AUTHORS"] == b"the authors of tarantool\n"
    assert "tarantool" in files
    assert set(tar_names(mirror / "binaries" / tt)) == {"tt", "LICENSE"}

    # A second download extends the mirror in place: the same bytes again.
    again = download(tt_cmd, project, mirror)
    assert again.returncode == 0, again.stderr
    assert str(mirror / "binaries" / tarantool) in again.stdout
    assert read_index(mirror) == entries


@pytest.fixture()
def airgap(project: Path, tmp_path: Path) -> Path:
    """The project on another machine: its manifest and lock, with nothing
    installed."""
    root = tmp_path / "airgap"
    (root / "src").mkdir(parents=True)
    (root / "src" / "init.lua").write_text("return {}\n")
    for name in ("app.manifest.toml", "app.manifest.lock"):
        shutil.copy(project / name, root / name)
    return root


def test_binary_install_from_the_mirror(tt_cmd, mirror, airgap):
    """tt binary install with no version installs the pinned tarantool from
    the mirror of --registry, and the pinned tt from that of TT_REGISTRIES,
    with no network: the headers and the EmmyLua stubs of the tarantool come
    along, and a build of the project goes offline."""
    installed = run(tt_cmd, airgap, "binary", "install", "tarantool", "--registry", str(mirror))
    assert installed.returncode == 0, installed.stderr
    assert f"Installing tarantool {TARANTOOL}, the version app.manifest.lock pins" in (
        installed.stdout + installed.stderr
    )

    root = airgap / ".tarantool"
    installation = root / "versions" / "tarantool" / f"tarantool_{TARANTOOL}"
    assert (installation / "tarantool").is_file()
    assert (installation / "include" / "tarantool" / "module.h").is_file()
    assert (installation / "share" / "tarantool" / "emmylua" / "box.lua").is_file()
    assert (installation / "LICENSE").is_file()
    assert (installation / "AUTHORS").is_file()
    assert (root / "bin" / "tarantool").resolve() == (installation / "tarantool").resolve()
    assert (root / "include" / "tarantool").resolve() == (
        installation / "include" / "tarantool"
    ).resolve()

    env = offline_env(TT_REGISTRIES=str(mirror))
    installed = run(tt_cmd, airgap, "binary", "install", "tt", env=env)
    assert installed.returncode == 0, installed.stderr
    assert (root / "versions" / "tt" / f"tt_{TT}" / "tt").is_file()
    assert (root / "bin" / "tt").resolve() == (root / "versions" / "tt" / f"tt_{TT}" / "tt")


def test_binary_install_checks_the_sha256_of_the_mirror(tt_cmd, mirror, airgap):
    """An archive whose bytes are not those the index of the mirror lists is
    refused, and nothing is installed."""
    goos, goarch = host_platform()
    archive = mirror / "binaries" / f"tarantool-{TARANTOOL}-{goos}-{goarch}.tar.gz"
    data = bytearray(archive.read_bytes())
    data[-1] ^= 0xFF
    archive.write_bytes(bytes(data))

    installed = run(tt_cmd, airgap, "binary", "install", "tarantool", "--registry", str(mirror))
    assert installed.returncode != 0
    assert "archive checksum mismatch" in installed.stdout + installed.stderr
    installation = airgap / ".tarantool" / "versions" / "tarantool" / f"tarantool_{TARANTOOL}"
    assert not installation.exists()


def test_download_without_a_pin_fails(tt_cmd, tmp_path):
    """A program the lock pins no version of cannot be mirrored: the download
    says that tt package resolve pins it."""
    root = make_project(tmp_path / "project", tarantool=">=3.99.0")
    resolved = run(tt_cmd, root, "package", "resolve")
    assert resolved.returncode == 0, resolved.stderr

    downloaded = download(tt_cmd, root, tmp_path / "mirror")
    assert downloaded.returncode != 0
    assert "the lock pins no version of tarantool: run tt package resolve" in downloaded.stderr
    assert "--no-binaries" in downloaded.stderr

    # --no-binaries mirrors no programs, so the lock need not pin them.
    rocks = tmp_path / "rocks"
    skipped = run(tt_cmd, root, "package", "download", "--dir", str(rocks), "--no-binaries")
    assert skipped.returncode == 0, skipped.stderr
    assert (rocks / "tt-rocks-index.json").is_file()
    assert not (rocks / "binaries").exists()


def test_resolve_without_a_version_to_pin_warns(tt_cmd, tmp_path):
    """Nothing satisfies the constraint, and no source answers: resolve
    succeeds, warns and leaves the pin out."""
    root = make_project(tmp_path / "project", tarantool=">=3.99.0")

    resolved = run(tt_cmd, root, "package", "resolve")
    assert resolved.returncode == 0, resolved.stderr
    assert "app.manifest.lock pins no tarantool" in resolved.stderr
    assert "tarantool_version" not in read_lock(root)
