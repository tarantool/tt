"""The spinner a build step shows, driven through the real binary on a pty.

The spinner is progress for a person: it is drawn on stderr, only when stderr
is a terminal, and never on stdout, so a script capturing stdout gets no
frames and carriage returns whatever its stderr is.
"""

import errno
import fcntl
import os
import struct
import subprocess
import termios
from pathlib import Path

import pytest

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.lua]
path = '.'
include = ['*.lua']

[components.native]
path = 'native/'

[components.native.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'sleep 0.5; echo built > fast_hash.so']
output = ['fast_hash.so']

[products.default]
components = ['lua', 'native']
default = true
"""

# ERASE returns to the first column and erases the line: the spinner writes
# it before every frame and once more when it stops.
ERASE = b"\r\x1b[K"
STATUS = b"running sh -c"


@pytest.fixture()
def project(tmp_path: Path) -> Path:
    (tmp_path / "app.manifest.toml").write_text(MANIFEST)
    (tmp_path / "VERSION").write_text("1.2.3\n")
    (tmp_path / "init.lua").write_text("return {}\n")
    (tmp_path / "native").mkdir()
    return tmp_path


def run_on_pty(tt_cmd, cwd: Path, *args: str, pty_stream: str) -> tuple[int, bytes, bytes]:
    """Run tt with one of its streams on a pty and the other on a pipe.

    pty_stream names the stream attached to the pty, "stderr" or "stdout".
    Returns the exit code, what the pty received and what the pipe received.
    """
    master, slave = os.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 100, 0, 0))

    env = dict(os.environ, TERM="xterm")
    env.pop("NO_COLOR", None)

    streams = {"stdout": subprocess.PIPE, "stderr": subprocess.PIPE}
    streams[pty_stream] = slave

    proc = subprocess.Popen(
        [str(tt_cmd), *args],
        cwd=cwd,
        stdin=subprocess.DEVNULL,
        env=env,
        **streams,
    )
    os.close(slave)

    captured = b""
    while True:
        try:
            chunk = os.read(master, 4096)
        except OSError as err:
            if err.errno == errno.EIO:
                break
            raise
        if not chunk:
            break
        captured += chunk
    os.close(master)

    piped = proc.stderr if pty_stream == "stdout" else proc.stdout
    rest = piped.read()
    piped.close()

    return proc.wait(), captured, rest


def test_spinner_on_terminal_stderr(tt_cmd, project):
    """A terminal stderr shows the spinner; the captured stdout stays clean."""
    code, stderr, stdout = run_on_pty(tt_cmd, project, "package", "build", pty_stream="stderr")
    assert code == 0, stderr

    assert ERASE not in stdout
    assert b"\r" not in stdout
    assert STATUS not in stdout

    assert STATUS in stderr
    # The last thing drawn after the last frame is the erase of stop.
    last_frame = stderr.rindex(STATUS)
    assert ERASE in stderr[last_frame:]


def test_no_spinner_on_piped_stderr(tt_cmd, project):
    """A stderr that is not a terminal shows no spinner, even when stdout is."""
    code, stdout, stderr = run_on_pty(tt_cmd, project, "package", "build", pty_stream="stdout")
    assert code == 0, stderr

    for stream in (stdout, stderr):
        assert b"\r" not in stream.replace(b"\r\n", b"\n")
        assert STATUS not in stream


def test_no_spinner_with_json_log(tt_cmd, project):
    """The JSON log format is for machines: no spinner even on a terminal."""
    code, stderr, stdout = run_on_pty(
        tt_cmd,
        project,
        "--log-format",
        "json",
        "package",
        "build",
        pty_stream="stderr",
    )
    assert code == 0, stderr

    for stream in (stdout, stderr):
        assert ERASE not in stream
        assert STATUS not in stream
