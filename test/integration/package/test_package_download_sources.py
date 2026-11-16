"""`tt package download` of a rock its server offers only as a rockspec.

Such a rockspec names its sources by `source.url`, here a local git repository,
which a build from the mirror cannot reach once the network is gone. The
download packs them into a `.src.rock` beside the rockspec, so the mirror
builds with the repository removed, the rock server stopped and every proxy
dead.
"""

import functools
import http.server
import json
import os
import shutil
import subprocess
import threading
from pathlib import Path

import pytest

pytestmark = pytest.mark.skipif(
    shutil.which("git") is None or shutil.which("tarantool") is None,
    reason="needs git and tarantool",
)

ROCKSPEC = """package = "hello"
version = "1.0.0-1"
source = {{
   url = "git+file://{repo}",
   tag = "1.0.0",
}}
description = {{ summary = "A rock served as a rockspec alone" }}
build = {{ type = "builtin", modules = {{ hello = "hello.lua" }} }}
"""

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

registries = ['{server}']

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'

[{table}]
hello = '>=1.0.0'
"""

# A proxy nothing listens on: any download over HTTP(S) fails at once.
DEAD_PROXY = "http://127.0.0.1:9"


def git(repo: Path, *args: str) -> None:
    subprocess.run(
        ["git", "-c", "user.name=t", "-c", "user.email=t@t", "-C", str(repo), *args],
        check=True,
        capture_output=True,
    )


def make_repo(repo: Path) -> None:
    """A repository whose tag 1.0.0 returns 'tagged' and whose later commit
    returns 'head': a mirror of anything but the tag shows."""
    repo.mkdir(parents=True)
    git(repo, "init", "-q")
    (repo / "hello.lua").write_text("return 'tagged'\n")
    git(repo, "add", "hello.lua")
    git(repo, "commit", "-q", "-m", "tagged")
    git(repo, "tag", "1.0.0")
    (repo / "hello.lua").write_text("return 'head'\n")
    git(repo, "commit", "-q", "-am", "head")


@pytest.fixture()
def source_server(tmp_path, tt_cmd):
    """A rock server over loopback that offers hello 1.0.0-1 as a rockspec
    alone, sourced from a local git repository.

    Yields the repository, the server's URL and a callable that stops it.
    """
    repo = tmp_path / "upstream" / "hello"
    make_repo(repo)

    served = tmp_path / "served"
    served.mkdir()
    (served / "hello-1.0.0-1.rockspec").write_text(ROCKSPEC.format(repo=repo))
    indexed = run(tt_cmd, tmp_path, "package", "index", str(served))
    assert indexed.returncode == 0, indexed.stderr

    handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(served))
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    stopped = False

    def stop() -> None:
        nonlocal stopped
        if not stopped:
            stopped = True
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)

    try:
        yield repo, served, f"http://127.0.0.1:{server.server_address[1]}/", stop
    finally:
        stop()


def offline_env(mirror: Path) -> dict[str, str]:
    """The environment of a build against mirror alone, with no network."""
    env = dict(os.environ, TT_REGISTRIES=str(mirror))
    for proxy in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY"):
        env[proxy] = DEAD_PROXY
    for bypass in ("NO_PROXY", "no_proxy"):
        env.pop(bypass, None)
    return env


def run(tt_cmd, cwd: Path, *args: str, env=None) -> subprocess.CompletedProcess:
    return subprocess.run([str(tt_cmd), *args], cwd=cwd, capture_output=True, text=True, env=env)


def download_named(tt_cmd, cwd: Path, server: str) -> subprocess.CompletedProcess:
    """Download hello@1.0.0-1 from server into cwd/mirror."""
    args = ("package", "download", "hello@1.0.0-1", "--registry", server, "--dir", "mirror")
    return run(tt_cmd, cwd, *args)


def assert_mirrored(mirror: Path, stdout: str) -> None:
    """The mirror holds the rockspec and the source rock packed of it, and
    its index lists both."""
    assert (mirror / "hello-1.0.0-1.rockspec").is_file()
    assert (mirror / "hello-1.0.0-1.src.rock").is_file()
    assert str(mirror / "hello-1.0.0-1.src.rock") in stdout

    index = json.loads((mirror / "tt-rocks-index.json").read_text())
    version = index["repository"]["hello"]["1.0.0-1"]
    assert version["rockspec"]["url"] == "hello-1.0.0-1.rockspec"
    assert [a["arch"] for a in version["artifacts"]] == ["src"]
    assert "src" in (mirror / "manifest").read_text()


def assert_requires(project: Path) -> None:
    """Tarantool run in the project requires hello as the tag has it."""
    (project / "check.lua").write_text("print(require('hello'))\nos.exit(0)\n")
    result = subprocess.run(["tarantool", "check.lua"], cwd=project, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == "tagged"


@pytest.mark.parametrize("table", ["dependencies", "dev_dependencies"])
@pytest.mark.parametrize("server_index", [True, False], ids=["index", "manifest-only"])
def test_mirror_of_a_rockspec_builds_offline(tt_cmd, tmp_path, source_server, table, server_index):
    """Resolve, mirror, remove the sources and the server, build --locked.

    With the server's tt-rocks-index.json the lock pins the rockspec's sha256
    and no source rock, which the build accepts because the source rock
    bundles that very rockspec; with manifests alone it pins nothing.
    """
    repo, served, server, stop = source_server
    if not server_index:
        (served / "tt-rocks-index.json").unlink()

    project = tmp_path / "project"
    project.mkdir()
    (project / "app.manifest.toml").write_text(MANIFEST.format(server=server, table=table))

    resolved = run(tt_cmd, project, "package", "resolve")
    assert resolved.returncode == 0, resolved.stderr

    lock = (project / "app.manifest.lock").read_text()
    if server_index:
        assert "rockspec = 'sha256:" in lock
        assert "src = " not in lock
    else:
        assert "artifacts" not in lock

    downloaded = run(tt_cmd, project, "package", "download", "--dir", "../mirror")
    assert downloaded.returncode == 0, downloaded.stderr
    mirror = tmp_path / "mirror"
    assert_mirrored(mirror, downloaded.stdout)

    shutil.rmtree(repo)
    stop()

    built = run(tt_cmd, project, "package", "build", "--locked", env=offline_env(mirror))
    assert built.returncode == 0, built.stderr
    assert_requires(project)


def test_named_download_packs_the_sources(tt_cmd, tmp_path, source_server):
    """NAME@VERSION mirrors the same way, outside a project; a rebuild of the
    mirror packs the same bytes."""
    _, _, server, _ = source_server

    first = download_named(tt_cmd, tmp_path, server)
    assert first.returncode == 0, first.stderr
    mirror = tmp_path / "mirror"
    assert_mirrored(mirror, first.stdout)
    packed = (mirror / "hello-1.0.0-1.src.rock").read_bytes()

    second = download_named(tt_cmd, tmp_path, server)
    assert second.returncode == 0, second.stderr
    assert (mirror / "hello-1.0.0-1.src.rock").read_bytes() == packed


def test_download_fails_when_the_sources_are_gone(tt_cmd, tmp_path, source_server):
    """A rockspec whose sources cannot be fetched fails the download rather
    than leave a mirror that cannot build."""
    repo, _, server, _ = source_server
    shutil.rmtree(repo)

    result = download_named(tt_cmd, tmp_path, server)
    assert result.returncode != 0
    assert "hello@1.0.0-1" in result.stderr
    assert not (tmp_path / "mirror" / "hello-1.0.0-1.src.rock").exists()
