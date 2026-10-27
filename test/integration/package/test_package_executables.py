"""End-to-end coverage of the executable components of a manifest.

A component with kind = "executable" builds the external process of an
application from its sources: tt package build puts the program into
.rocks/bin/, tt package pack carries it in a --without-deps archive, and the
record gw: {binary: gw} of an instance description runs it from .rocks/bin/
of the project or of the installed version. tt instance start builds
nothing, and says so when the program is missing.

The applications here have external processes only: nothing starts a
Tarantool instance or binds a port.
"""

import contextlib
import json
import os
import re
import shutil
import signal
import subprocess
import tarfile
import time
from pathlib import Path

import pytest

try:
    import zstandard
except ImportError:  # Python 3.14 reads zstd itself; no wheel may build for it.
    zstandard = None

ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

APP = "gwapp"

MANIFEST = """manifest_version = '0.1'

[package]
name = 'gwapp'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[components.{name}]
path = '{name}'
kind = 'executable'

[components.{name}.build]
{build}
"""

# The program of the go component: it tells where it runs, then waits for
# SIGTERM.
GO_MAIN = """package main

import (
\t"fmt"
\t"os"
\t"os/signal"
\t"syscall"
)

func main() {
\twd, _ := os.Getwd()
\tfmt.Println("gw started in", wd)
\tstop := make(chan os.Signal, 1)
\tsignal.Notify(stop, syscall.SIGTERM)
\t<-stop
\tfmt.Println("gw got TERM")
}
"""

# The program of the cargo component: SIGTERM ends it as it is.
RUST_MAIN = """use std::{thread, time::Duration};

fn main() {
    println!("agent started");
    loop {
        thread::sleep(Duration::from_secs(1));
    }
}
"""


def run(tt_cmd, cwd: Path, *args: str, env=None) -> subprocess.CompletedProcess:
    p = subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        env=env,
    )
    print(f"$ tt {' '.join(args)}\n{p.stdout}{p.stderr}")
    return p


def make_go_project(root: Path) -> Path:
    """Write a project whose component gw is a Go program, and whose
    default template runs it as the external process gw."""
    (root / "gw").mkdir(parents=True)
    (root / "gw" / "go.mod").write_text("module example.com/gw\n\ngo 1.22\n")
    (root / "gw" / "main.go").write_text(GO_MAIN)
    (root / "app.manifest.toml").write_text(
        MANIFEST.format(name="gw", build="backend = 'go'"),
    )
    return finish_project(root, "gw")


def make_cargo_project(root: Path) -> Path:
    """Write a project whose component agent is a static Rust program, and
    whose default template runs it as the external process agent."""
    (root / "agent" / "src").mkdir(parents=True)
    (root / "agent" / "Cargo.toml").write_text(
        "[package]\nname = 'agent'\nversion = '0.1.0'\nedition = '2021'\n\n[dependencies]\n",
    )
    (root / "agent" / "src" / "main.rs").write_text(RUST_MAIN)
    (root / "app.manifest.toml").write_text(
        MANIFEST.format(name="agent", build="backend = 'cargo'\nprofile = 'dev'\nstatic = true"),
    )
    return finish_project(root, "agent")


def finish_project(root: Path, program: str) -> Path:
    (root / "VERSION").write_text("1.0.0\n")
    template = root / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text(f"{program}:\n  binary: {program}\n")
    return root


def build(tt_cmd, project: Path) -> None:
    result = run(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))
    assert result.returncode == 0, result.stderr


def status(tt_cmd, cwd: Path, *args: str, env=None) -> dict:
    result = run(tt_cmd, cwd, "instance", "status", *args, f"{APP}:*", "-o", "json", env=env)
    assert result.returncode == 0, result.stderr
    return json.loads(result.stdout)


def wait(timeout, condition, interval=0.2):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if condition():
            return True
        time.sleep(interval)
    return condition()


def kill_instances(root: Path):
    """Kill the process groups of the watchdogs and the external processes
    that still run under the scope root root, so that a failed test leaves
    nothing behind."""
    for pid_file in [*root.glob("var/run/*/*/tt.pid"), *root.glob("var/run/*/*/process.pid")]:
        with contextlib.suppress(OSError, ValueError):
            os.killpg(int(pid_file.read_text().strip()), signal.SIGKILL)


def start_status_stop(tt_cmd, cwd: Path, program: str, root: Path, log_line: str, *scope, env=None):
    """Start the external process program of the application, see it run
    and say log_line, and stop it."""
    target = f"{APP}:{program}"
    try:
        result = run(tt_cmd, cwd, "instance", "start", *scope, target, env=env)
        assert result.returncode == 0, result.stderr

        assert wait(
            10,
            lambda: status(tt_cmd, cwd, *scope, env=env)[target]["status"] == "RUNNING",
        )

        log = root / "var" / "log" / APP / program / "tt.log"
        assert wait(10, lambda: log.exists() and log_line in log.read_text())

        result = run(tt_cmd, cwd, "instance", "stop", "-y", *scope, target, env=env)
        assert result.returncode == 0, result.stderr
        assert status(tt_cmd, cwd, *scope, env=env)[target]["status"] == "NOT RUNNING"
    finally:
        kill_instances(root)


def host_token(tt_cmd, cwd: Path) -> str:
    """The platform token of the host, which tt version reports as <os>/<arch>."""
    result = run(tt_cmd, cwd, "version")
    assert result.returncode == 0, result.stderr

    match = re.search(r"\b(linux|darwin)/(\w+)", result.stdout)
    assert match, result.stdout
    return f"{match[1]}-{match[2]}"


def archive_names(archive: Path) -> list[str]:
    """List what a .tt archive (tar.zst) holds."""
    if zstandard is None:
        with tarfile.open(archive, mode="r:zst") as tar:
            return tar.getnames()

    with archive.open("rb") as raw:
        with zstandard.ZstdDecompressor().stream_reader(raw) as stream:
            with tarfile.open(fileobj=stream, mode="r|") as tar:
                return tar.getnames()


def pack_without_deps(tt_cmd, project: Path) -> Path:
    result = run(
        tt_cmd,
        project,
        "package",
        "pack",
        "--without-deps",
        "--registry",
        str(ROCKS_REPO),
    )
    assert result.returncode == 0, result.stderr

    archives = list((project / "_build" / "pack").glob("*.tt"))
    assert len(archives) == 1, archives
    return archives[0]


requires_go = pytest.mark.skipif(shutil.which("go") is None, reason="go is not in PATH")


@requires_go
def test_start_without_build_tells_to_build(tt_cmd, tmp_path):
    """tt instance start builds nothing: a program missing from .rocks/bin
    that a component of the project builds is an error naming both places it
    was looked for and telling to run tt package build. tt instance check
    says the same."""
    project = make_go_project(tmp_path / "project")
    root = project / ".tarantool"

    try:
        for command in ("start", "check"):
            result = run(tt_cmd, project, "instance", command, f"{APP}:gw")
            assert result.returncode != 0, result.stdout
            output = result.stdout + result.stderr
            assert f'"gw" is neither in {project / ".rocks" / "bin"} nor in PATH' in output
            assert 'the executable component "gw" of the project builds it' in output
            assert "run tt package build" in output
    finally:
        kill_instances(root)

    assert not (project / ".rocks" / "bin" / "gw").exists()
    assert not list(root.glob("var/run/*/*/tt.pid")), "nothing has started"


@requires_go
def test_go_component_runs_in_the_project(tt_cmd, tmp_path):
    """tt package build puts the Go program into .rocks/bin, and the record
    gw: {binary: gw} runs it from there."""
    project = make_go_project(tmp_path / "project")

    build(tt_cmd, project)

    program = project / ".rocks" / "bin" / "gw"
    assert program.is_file()
    assert os.access(program, os.X_OK)
    # The component lays nothing out under the package's namespace.
    assert not (project / ".rocks" / "share" / "tarantool" / APP / "main.go").exists()

    result = run(tt_cmd, project, "instance", "check", f"{APP}:gw")
    assert result.returncode == 0, result.stderr
    assert f"its binary {program} is found" in result.stdout + result.stderr

    start_status_stop(tt_cmd, project, "gw", project / ".tarantool", "gw started in")


@requires_go
def test_go_component_installs_and_runs_from_the_user_scope(tt_cmd, tmp_path):
    """pack --without-deps carries the program in .rocks/bin and labels the
    archive for the host; installed into the user scope, the version runs it
    from its own .rocks/bin."""
    project = make_go_project(tmp_path / "project")

    archive = pack_without_deps(tt_cmd, project)
    assert archive.name == f"{APP}-1.0.0-{host_token(tt_cmd, tmp_path)}.tt", archive.name
    names = archive_names(archive)
    assert any(name.endswith(".rocks/bin/gw") for name in names), names

    user_root = tmp_path / "user"
    env = dict(
        os.environ,
        TT_USER_ROOT=str(user_root),
        TT_SYSTEM_ROOT=str(tmp_path / "system"),
    )
    env.pop("TT_SYSTEMD_UNIT", None)
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()

    result = run(tt_cmd, elsewhere, "package", "install", "--scope", "user", str(archive), env=env)
    assert result.returncode == 0, result.stderr

    version = user_root / "versions" / APP / f"{APP}_1.0.0"
    assert os.access(version / ".rocks" / "bin" / "gw", os.X_OK)

    start_status_stop(
        tt_cmd,
        elsewhere,
        "gw",
        user_root,
        "gw started in",
        "--scope",
        "user",
        env=env,
    )


def cargo_with_musl() -> bool:
    """Whether cargo is in PATH with the musl target of the host installed,
    which a static build needs."""
    if shutil.which("cargo") is None or shutil.which("rustc") is None:
        return False
    arch = {"x86_64": "x86_64", "aarch64": "aarch64", "arm64": "aarch64"}.get(os.uname().machine)
    if arch is None or os.uname().sysname != "Linux":
        return False
    sysroot = subprocess.run(
        ["rustc", "--print", "sysroot"],
        capture_output=True,
        text=True,
    ).stdout.strip()
    return (Path(sysroot) / "lib" / "rustlib" / f"{arch}-unknown-linux-musl").is_dir()


@pytest.mark.skipif(not cargo_with_musl(), reason="no cargo with the musl target")
def test_static_cargo_component_runs_in_the_project(tt_cmd, tmp_path):
    """A cargo component with static = true is built for the musl target and
    checked to need no loader or library; it runs like the Go one."""
    project = make_cargo_project(tmp_path / "project")

    build(tt_cmd, project)
    assert os.access(project / ".rocks" / "bin" / "agent", os.X_OK)

    start_status_stop(tt_cmd, project, "agent", project / ".tarantool", "agent started")
