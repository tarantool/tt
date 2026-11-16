"""tt package uninstall removes the programs a guest put into .rocks/bin/.

A guest package installed into a project carries the programs of its
executable components in .rocks/bin/. Uninstall removes exactly those, and no
other file of .rocks/bin/: the project's own programs and the files of bin/ no
component of the guest makes stay. A guest an older tt installed laid its
files out by hand and recorded none of its programs; they are told by its
manifest, among the files of bin/ no rock owns.

Everything is local: the components copy a program of the host, and nothing
is resolved from a rock server.
"""

import os
import shutil
import subprocess
from pathlib import Path

ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

# A small program of the host the components copy: a build is to put a
# program of the build platform into bin/.
HOST_PROGRAM = os.path.realpath(shutil.which("true") or "/usr/bin/true")

MANIFEST = """manifest_version = '0.1'

[package]
name = '{name}'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua', 'tool']
default = true

[components.lua]
path = 'src'

[components.tool]
path = 'tool'

[components.tool.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'mkdir -p build && cp {program} build/{tool}']
output = [{{ path = 'build/{tool}', to = 'bin' }}]
"""


def run(tt_cmd, cwd: Path, *args: str, env: dict | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        env=env,
    )


def tt_ok(tt_cmd, cwd: Path, *args: str) -> subprocess.CompletedProcess:
    result = run(tt_cmd, cwd, *args)
    assert result.returncode == 0, result.stderr
    return result


def manifest(name: str, tool: str) -> str:
    return MANIFEST.format(name=name, tool=tool, program=HOST_PROGRAM)


def make_project(root: Path, name: str, tool: str) -> Path:
    """Write a project of a Lua component and the component tool, whose
    build makes the program tool."""
    (root / "src").mkdir(parents=True)
    (root / "src" / "init.lua").write_text("return {}\n")
    (root / "tool").mkdir()
    (root / "tool" / "README").write_text("the sources of the tool\n")
    (root / "app.manifest.toml").write_text(manifest(name, tool))
    return root


def bin_names(project: Path) -> list[str]:
    bin_dir = project / ".rocks" / "bin"
    return sorted(path.name for path in bin_dir.iterdir()) if bin_dir.exists() else []


def test_uninstall_removes_the_programs_of_the_guest(tt_cmd, tmp_path):
    """The guest's program goes; the host's program and a file of bin/ no
    package installed stay."""
    guest = make_project(tmp_path / "guest", "monitoring", "mon-tool")
    tt_ok(tt_cmd, guest, "package", "pack", "--without-deps", "--registry", str(ROCKS_REPO))
    archives = list((guest / "_build" / "pack").glob("*.tt"))
    assert len(archives) == 1, archives

    host = make_project(tmp_path / "host", "my-app", "app-tool")
    tt_ok(tt_cmd, host, "package", "build", "--registry", str(ROCKS_REPO))
    (host / ".rocks" / "bin" / "stranger").write_text("laid by hand\n")

    tt_ok(tt_cmd, host, "package", "install", str(archives[0]))
    assert bin_names(host) == ["app-tool", "mon-tool", "stranger"]

    tt_ok(tt_cmd, host, "package", "uninstall", "monitoring", "--yes")

    assert bin_names(host) == ["app-tool", "stranger"]
    assert not (host / ".rocks" / "share" / "tarantool" / "monitoring").exists()
    assert (host / ".rocks" / "share" / "tarantool" / "my-app" / "init.lua").exists()


def test_uninstall_removes_the_programs_of_a_guest_an_older_tt_installed(tt_cmd, tmp_path):
    """A guest laid out by hand, as an older tt installed it: its program in
    bin/ is no rock's, and its manifest tells it."""
    host = make_project(tmp_path / "host", "my-app", "app-tool")
    tt_ok(tt_cmd, host, "package", "build", "--registry", str(ROCKS_REPO))

    rocks = host / ".rocks"
    meta = rocks / "manifests" / "monitoring"
    meta.mkdir(parents=True)
    (meta / "manifest.toml").write_text(manifest("monitoring", "mon-tool"))
    (meta / "VERSION").write_text("1.0.0\n")
    (rocks / "share" / "tarantool" / "monitoring").mkdir(parents=True)
    (rocks / "share" / "tarantool" / "monitoring" / "init.lua").write_text("return {}\n")
    shutil.copy(HOST_PROGRAM, rocks / "bin" / "mon-tool")
    (rocks / "bin" / "stranger").write_text("laid by hand\n")

    tt_ok(tt_cmd, host, "package", "uninstall", "monitoring", "--yes")

    assert bin_names(host) == ["app-tool", "stranger"]
    assert not (rocks / "share" / "tarantool" / "monitoring").exists()
    assert not meta.exists()
