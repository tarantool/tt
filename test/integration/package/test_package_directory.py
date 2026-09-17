"""End-to-end coverage for -C: work on a project that is not the cwd.

Every case runs the binary from a directory that holds no manifest at all, so a
command that answers correctly cannot have read the working directory, which
is the whole claim -C makes. Nothing here resolves, so no rock server is
needed.
"""

import subprocess
from pathlib import Path

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
stat = '>=0.3.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
"""


def run(tt_cmd, cwd: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        capture_output=True,
        text=True,
    )


def test_deps_reads_the_c_directory(tt_cmd, tmp_path):
    """`tt package deps -C` reports the project at -C, not the one at cwd."""
    project = tmp_path / "project"
    project.mkdir()
    (project / "app.manifest.toml").write_text(MANIFEST)

    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()

    # Without -C there is nothing to report: the guard that the -C run is
    # measured against.
    bare = run(tt_cmd, elsewhere, "package", "deps")
    assert bare.returncode == 1
    assert "app.manifest.toml" in bare.stderr

    flagged = run(tt_cmd, elsewhere, "package", "deps", "-C", str(project))
    assert flagged.returncode == 0, flagged.stderr
    assert "my-app" in flagged.stdout
    assert "stat" in flagged.stdout


def test_c_accepts_a_relative_path(tt_cmd, tmp_path):
    """-C resolves against the working directory, like any other path."""
    project = tmp_path / "project"
    project.mkdir()
    (project / "app.manifest.toml").write_text(MANIFEST)

    result = run(tt_cmd, tmp_path, "package", "deps", "-C", "project")
    assert result.returncode == 0, result.stderr
    assert "my-app" in result.stdout


def test_new_writes_into_the_c_directory(tt_cmd, tmp_path):
    """`tt new -C` creates the manifest at -C and leaves the cwd alone."""
    target = tmp_path / "target"
    target.mkdir()

    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()

    result = run(tt_cmd, elsewhere, "new", "-C", str(target), "-n", "my-app")
    assert result.returncode == 0, result.stderr

    assert (target / "app.manifest.toml").exists()
    assert not (elsewhere / "app.manifest.toml").exists()
    assert 'name = "my-app"' in (target / "app.manifest.toml").read_text()


def test_c_is_rejected_when_it_is_not_a_directory(tt_cmd, tmp_path):
    """A -C that cannot be a project is reported as a -C problem.

    Naming the flag matters: left to the command, the same mistake surfaces as
    a missing manifest, which reads as a problem with the project rather than
    with the path that was typed.
    """
    missing = run(tt_cmd, tmp_path, "package", "deps", "-C", str(tmp_path / "absent"))
    assert missing.returncode == 1
    assert "-C" in missing.stderr

    file_path = tmp_path / "app.manifest.toml"
    file_path.write_text(MANIFEST)

    not_a_dir = run(tt_cmd, tmp_path, "package", "deps", "-C", str(file_path))
    assert not_a_dir.returncode == 1
    assert "not a directory" in not_a_dir.stderr


def test_c_is_accepted_by_every_package_subcommand(tt_cmd, tmp_path):
    """-C is declared once on the group, so no subcommand can miss it.

    The check is that the flag parses, not that the command succeeds: proving
    it reaches the project is the job of the cases above.
    """
    for sub in (
        "build",
        "fetch",
        "pack",
        "add",
        "remove",
        "update",
        "resolve",
        "deps",
        "install",
        "list",
        "uninstall",
        "search",
        "download",
    ):
        result = run(tt_cmd, tmp_path, "package", sub, "--help", "-C", str(tmp_path))
        assert result.returncode == 0, f"{sub}: {result.stderr}"
        assert "-C, --directory" in result.stdout, sub
