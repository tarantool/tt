"""Exit codes of the manifest commands, driven through the real binary.

1 is what the user asked for and can fix, 2 is the system the command ran on,
3 is a multi-package operation that partly succeeded. The unit tests pin the
classification; these pin that a real failure still carries its type all the
way up to the process exit, which a wrapper formatting with %v instead of %w
would quietly break.
"""

import os
import socket
import subprocess
from pathlib import Path

import pytest

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
stat = '>=0.3.0'
"""


def run(tt_cmd, cwd: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run([str(tt_cmd), *args], cwd=cwd, capture_output=True, text=True)


def closed_port() -> int:
    """A loopback port nothing listens on: bound once, then released."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


@pytest.fixture()
def project(tmp_path: Path) -> Path:
    (tmp_path / "app.manifest.toml").write_text(MANIFEST)
    return tmp_path


def test_unreachable_registry_exits_2(tt_cmd, project):
    """A rock server that refuses the connection is the network's fault."""
    registry = f"http://127.0.0.1:{closed_port()}/"

    result = run(tt_cmd, project, "package", "resolve", "--registry", registry)
    assert result.returncode == 2, result.stderr
    assert "connection refused" in result.stderr


def test_malformed_registry_exits_1(tt_cmd, project):
    """A registry URL with a scheme nothing speaks is the user's to fix.

    The HTTP client reports it in the same wrapper type as a network failure,
    which is exactly why it has to be told apart.
    """
    result = run(tt_cmd, project, "package", "resolve", "--registry", "ftp://127.0.0.1/")
    assert result.returncode == 1, result.stderr


def test_invalid_manifest_exits_1(tt_cmd, project):
    (project / "app.manifest.toml").write_text(MANIFEST.replace("'my-app'", "'My App'"))

    result = run(tt_cmd, project, "package", "deps")
    assert result.returncode == 1, result.stderr


@pytest.mark.skipif(os.geteuid() == 0, reason="root reads a file whatever its mode")
def test_unreadable_manifest_exits_2(tt_cmd, project):
    """A file the process may not read is the machine refusing, not the input."""
    manifest = project / "app.manifest.toml"
    manifest.chmod(0)
    try:
        result = run(tt_cmd, project, "package", "deps")
    finally:
        manifest.chmod(0o644)

    assert result.returncode == 2, result.stderr
    assert "permission denied" in result.stderr
