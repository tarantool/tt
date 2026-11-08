"""tt wasm against a release of its own, offline.

TT_WASM_RELEASES replaces the releases tt knows with a table whose assets a
loopback server serves: the native module is a few bytes nothing loads, which is
enough to follow it from the download, through the rock, into the rocks tree a
build lays out. tarawasm is left out (--no-tarawasm): installing it takes PyPI.
"""

import functools
import hashlib
import http.server
import json
import os
import platform
import subprocess
import threading
from pathlib import Path

import pytest

MODULE = b"\x7fELF not a module: tt packs and installs it, and nothing loads it\n"

# The builds of the release, as goos, goarch, rock arch and module: the two
# tt knows and linux/arm64, which tarantool-wasm has no build for, so that the
# tests run on an arm64 runner too.
PLATFORMS = [
    ("linux", "amd64", "linux-x86_64", "wasm.so"),
    ("linux", "arm64", "linux-aarch64", "wasm.so"),
    ("darwin", "arm64", "macosx-aarch64", "wasm.dylib"),
]


def host_platform() -> tuple[str, str, str, str]:
    """The build of the host, or a skip on a host the release has none for."""
    system, machine = platform.system(), platform.machine().lower()
    if system == "Linux" and machine in ("x86_64", "amd64"):
        return PLATFORMS[0]
    if system == "Linux" and machine in ("aarch64", "arm64"):
        return PLATFORMS[1]
    if system == "Darwin" and machine in ("arm64", "aarch64"):
        return PLATFORMS[2]
    pytest.skip(f"tarantool-wasm has no build for {system} {machine}")


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, format, *args):
        pass


def serve(handler) -> tuple[http.server.ThreadingHTTPServer, str]:
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, f"http://127.0.0.1:{server.server_address[1]}"


@pytest.fixture()
def assets(tmp_path):
    """A loopback server of the directory its URL serves."""
    root = tmp_path / "assets"
    root.mkdir()
    server, url = serve(functools.partial(QuietHandler, directory=str(root)))
    yield root, url
    server.shutdown()


def write_release(path: Path, url: str, assets: Path, sha256: str | None = None) -> None:
    """Write a release table of 0.1.0 whose builds the assets server serves."""
    version = assets / "v0.1.0"
    version.mkdir(parents=True, exist_ok=True)
    builds = []
    for goos, goarch, arch, module in PLATFORMS:
        asset = f"tarantool-wasm-0.1.0-{arch}-{module}"
        (version / asset).write_bytes(MODULE)
        builds.append(
            {
                "goos": goos,
                "goarch": goarch,
                "asset": asset,
                "sha256": sha256 or hashlib.sha256(MODULE).hexdigest(),
                "rock_arch": arch,
                "module": module,
            },
        )
    path.write_text(
        json.dumps(
            {
                "0.1.0": {
                    "version": "0.1.0",
                    "rock_revision": 1,
                    "sdk_asset": "tarantool-wasm-0.1.0-sdk.tar.gz",
                    "sdk_md5": "0" * 32,
                    "base_url": url,
                    "platforms": builds,
                },
            },
        ),
    )


@pytest.fixture()
def env(tmp_path, assets):
    """The environment of tt: the fake release, no registry but an empty one,
    and a cache of the test's own."""
    root, url = assets
    table = tmp_path / "releases.json"
    write_release(table, url, root)
    empty = tmp_path / "empty-registry"
    empty.mkdir()
    (empty / "manifest").write_text("commands = {}\nmodules = {}\nrepository = {}\n")
    return {
        **os.environ,
        "TT_WASM_RELEASES": str(table),
        "TT_REGISTRIES": str(empty),
        "XDG_CACHE_HOME": str(tmp_path / "cache"),
    }


def tt(tt_cmd, cwd: Path, *args: str, env: dict) -> subprocess.CompletedProcess:
    return subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        env=env,
    )


@pytest.fixture()
def project(tt_cmd, tmp_path, env) -> Path:
    """An empty package."""
    root = tmp_path / "app"
    root.mkdir()
    result = tt(tt_cmd, root, "new", env=env)
    assert result.returncode == 0, result.stderr
    return root


def installed_module(project: Path) -> bytes:
    module = host_platform()[3]
    return (project / ".rocks" / "lib" / "tarantool" / module).read_bytes()


def test_prepare_packs_the_rock_and_a_build_installs_it(tt_cmd, project, env):
    arch = host_platform()[2]

    result = tt(tt_cmd, project, "wasm", "prepare", "--no-tarawasm", env=env)

    assert result.returncode == 0, result.stderr
    assert "prepared for tarantool-wasm 0.1.0" in result.stdout
    assert (project / ".tarantool" / "registry" / f"wasm-0.1.0-1.{arch}.rock").is_file()
    assert (
        'wasm = { version = "0.1.0", registry = ".tarantool/registry" }'
        in (project / "app.manifest.toml").read_text()
    )
    lock = (project / "app.manifest.lock").read_text()
    assert "name = 'wasm'" in lock
    assert "version = '0.1.0-1'" in lock

    build = tt(tt_cmd, project, "package", "build", env=env)

    assert build.returncode == 0, build.stderr
    assert installed_module(project) == MODULE

    status = tt(tt_cmd, project, "wasm", "status", env=env)
    assert status.returncode == 0, status.stdout + status.stderr
    assert "ready" in status.stdout


def test_prepare_refuses_a_tampered_asset(tt_cmd, project, env, assets):
    root, url = assets
    write_release(Path(env["TT_WASM_RELEASES"]), url, root, sha256="0" * 64)
    before = (project / "app.manifest.toml").read_text()

    result = tt(tt_cmd, project, "wasm", "prepare", "--no-tarawasm", env=env)

    assert result.returncode != 0
    assert "checksum mismatch" in result.stderr
    assert (project / "app.manifest.toml").read_text() == before
    assert not (project / ".tarantool" / "registry").exists()


def test_prepare_refuses_an_unknown_release(tt_cmd, project, env):
    result = tt(tt_cmd, project, "wasm", "prepare", "--no-tarawasm", "--version", "9.9.9", env=env)

    assert result.returncode != 0
    assert "unknown release: tarantool-wasm 9.9.9; known: 0.1.0" in result.stderr


def test_status_of_an_unprepared_project_fails(tt_cmd, project, env):
    result = tt(tt_cmd, project, "wasm", "status", env=env)

    assert result.returncode != 0
    assert "the package declares no exact wasm version" in result.stderr


class Redirect(http.server.BaseHTTPRequestHandler):
    """Sends every request on to target, as GitHub does a release asset."""

    target = ""

    def do_GET(self):
        self.send_response(302)
        self.send_header("Location", self.target + self.path)
        self.end_headers()

    def log_message(self, format, *args):
        pass


def test_rocks_of_a_release_serve_every_machine(tt_cmd, tmp_path, project, env):
    """tt wasm rock packs the rock of every build; a project declares the wasm
    rock from where they are served, through a redirect, and builds."""
    rocks = tmp_path / "server" / "v0.1.0"

    packed = tt(tt_cmd, tmp_path, "wasm", "rock", "--out", str(rocks), env=env)

    assert packed.returncode == 0, packed.stderr
    # The manifests luarocks-admin make_manifest writes, one per Lua version
    # and one for all.
    assert sorted(path.name for path in rocks.iterdir()) == [
        "manifest",
        "manifest-5.1",
        "manifest-5.2",
        "manifest-5.3",
        "manifest-5.4",
        "wasm-0.1.0-1.linux-aarch64.rock",
        "wasm-0.1.0-1.linux-x86_64.rock",
        "wasm-0.1.0-1.macosx-aarch64.rock",
    ]

    server, url = serve(functools.partial(QuietHandler, directory=str(rocks.parent)))
    redirect = type("ToServer", (Redirect,), {"target": url})
    front, front_url = serve(redirect)
    try:
        registry = f"{front_url}/v0.1.0/"
        result = tt(
            tt_cmd,
            project,
            "wasm",
            "prepare",
            "--no-tarawasm",
            "--rock-server",
            registry,
            env=env,
        )
        assert result.returncode == 0, result.stderr
        assert f"rock:       from {registry}" in result.stdout
        assert not (project / ".tarantool" / "registry").exists()

        build = tt(tt_cmd, project, "package", "build", env=env)
        assert build.returncode == 0, build.stderr
        assert installed_module(project) == MODULE
    finally:
        front.shutdown()
        server.shutdown()
