"""End-to-end coverage for `tt package build`, `pack` and `install`.

These three had Go tests but no integration suite, so nothing drove the real
binary through the cycle a user actually runs: build a project, pack it, and
install the archive somewhere else. That is what this file does, in one chain,
with each step asserting on what the previous one put on disk.

It stays offline by resolving against the fixture rock repository as a local
directory registry: `--registry` for the commands that take it, and
`TT_REGISTRIES` for `tt package install`, which has no flag of its own. The
archive is `--without-deps` throughout: a with-deps archive bundles `_runtime/`
and would need a Tarantool runtime in the cache, which a test cannot assume.
"""

import os
import subprocess
import tarfile
from pathlib import Path

import zstandard

ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

# stat is the only rock the fixture repository serves; 0.3.2-1 is its newest.
DEPENDENCY = "stat"
DEPENDENCY_VERSION = "0.3.2-1"

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'
include = ['README.md']

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
stat = '>=0.3.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = 'src'
"""


def run(tt_cmd, cwd: Path, *args: str, env: dict | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        [str(tt_cmd), *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        env=env,
    )


def registry_env(registry: Path) -> dict:
    """The environment plus TT_REGISTRIES.

    `tt package install` takes no --registry, so this is the only way to point
    it at the fixture repository. The rest of the environment is inherited
    because resolving a rock runs its build, and that reaches for cmake on
    PATH.
    """
    return {**os.environ, "TT_REGISTRIES": str(registry)}


def archive_names(archive: Path) -> list[str]:
    """List what a .tt archive holds.

    A .tt is tar.zst, which tarfile cannot open before Python 3.14, so the
    stream is decompressed first and read in tarfile's streaming mode, since
    the decompressed stream is not seekable.
    """
    with archive.open("rb") as raw:
        with zstandard.ZstdDecompressor().stream_reader(raw) as stream:
            with tarfile.open(fileobj=stream, mode="r|") as tar:
                return tar.getnames()


def make_project(root: Path) -> Path:
    """Write the smallest project the whole cycle can run on."""
    (root / "src").mkdir(parents=True)
    (root / "src" / "init.lua").write_text("return {}\n")
    (root / "README.md").write_text("# my-app\n")
    (root / "app.manifest.toml").write_text(MANIFEST)
    return root


def build(tt_cmd, project: Path, *args: str) -> subprocess.CompletedProcess:
    return run(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO), *args)


def test_build_materializes_the_tree(tt_cmd, tmp_path):
    """Build lays out the package, its dependency and version.lua, and locks."""
    project = make_project(tmp_path / "project")

    result = build(tt_cmd, project)
    assert result.returncode == 0, result.stderr

    share = project / ".rocks" / "share" / "tarantool"
    assert (share / "my-app" / "init.lua").exists()
    assert (share / "my-app" / "version.lua").exists()
    assert (share / DEPENDENCY).is_dir(), "the declared dependency must be materialized"

    lock = project / "app.manifest.lock"
    assert lock.exists()
    assert DEPENDENCY in lock.read_text()


def test_build_locked_refuses_a_stale_lock(tt_cmd, tmp_path):
    """--locked is the CI gate: a manifest edited after the lock stops it."""
    project = make_project(tmp_path / "project")
    assert build(tt_cmd, project).returncode == 0

    # Any edit changes the hash the lock recorded, a comment included.
    manifest = project / "app.manifest.toml"
    manifest.write_text(manifest.read_text() + "\n# an edit after the lock was written\n")

    stale = build(tt_cmd, project, "--locked")
    assert stale.returncode == 1
    assert "lock" in stale.stderr.lower()

    # Without the flag the same build re-resolves and rewrites the lock, which
    # is what makes the refusal above a property of --locked and not of the
    # edit.
    assert build(tt_cmd, project).returncode == 0
    assert build(tt_cmd, project, "--locked").returncode == 0


def test_fetch_materializes_from_the_lock(tt_cmd, tmp_path):
    """Fetch rebuilds .rocks/ from the lock alone."""
    project = make_project(tmp_path / "project")
    assert build(tt_cmd, project).returncode == 0

    rocks = project / ".rocks"
    assert (rocks / "share" / "tarantool" / DEPENDENCY).is_dir()

    # Take the tree away and put it back with fetch, which never resolves.
    subprocess.run(["rm", "-rf", str(rocks)], check=True)
    result = run(tt_cmd, project, "package", "fetch", "--registry", str(ROCKS_REPO))
    assert result.returncode == 0, result.stderr
    assert (rocks / "share" / "tarantool" / DEPENDENCY).is_dir()


def pack(tt_cmd, project: Path) -> Path:
    """Pack the project without dependencies and return the archive path."""
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


def test_pack_without_deps_builds_a_portable_archive(tt_cmd, tmp_path):
    """The archive carries the package and its metadata, and nothing foreign."""
    project = make_project(tmp_path / "project")

    archive = pack(tt_cmd, project)
    # No native component and no bundled runtime, so the archive fits any
    # platform and says so in its name instead of naming one.
    assert archive.name.startswith("my-app-")
    assert archive.name.endswith("-any.tt")

    names = archive_names(archive)

    assert "app.manifest.toml" in names
    assert "app.manifest.lock" in names
    assert "VERSION" in names
    assert "README.md" in names, "[package].include must reach the archive"
    assert any(name.endswith(".rocks/share/tarantool/my-app/init.lua") for name in names), names

    # --without-deps: no runtime and no foreign rock travels along.
    assert not any(name.startswith("_runtime") for name in names), names
    assert not any(f"/{DEPENDENCY}/" in name for name in names), names

    # The fields pack fills only for a with-deps archive stay empty.
    lock = (project / "app.manifest.lock").read_text()
    assert "bundled_tarantool_version" not in lock


def test_install_refetches_the_closure(tt_cmd, tmp_path):
    """Installing a --without-deps archive pulls its pins from the registry."""
    project = make_project(tmp_path / "project")
    archive = pack(tt_cmd, project)

    target = tmp_path / "target"
    target.mkdir()

    result = run(
        tt_cmd,
        target,
        "package",
        "install",
        str(archive),
        env=registry_env(ROCKS_REPO),
    )
    assert result.returncode == 0, result.stderr

    share = target / ".rocks" / "share" / "tarantool"
    assert (share / "my-app" / "init.lua").exists()
    assert (share / DEPENDENCY).is_dir(), "the lock's pins must be refetched"

    # The metadata install records is what list and uninstall then read.
    meta = target / ".rocks" / "manifests" / "my-app"
    assert (meta / "manifest.toml").exists()
    assert (meta / "lock.toml").exists()
    assert (meta / "VERSION").exists()

    listed = run(tt_cmd, target, "package", "list")
    assert listed.returncode == 0, listed.stderr
    assert "my-app" in listed.stdout
    assert DEPENDENCY_VERSION in listed.stdout


def test_install_refuses_a_collision_without_a_flag(tt_cmd, tmp_path):
    """A second install of the same package stops instead of overwriting."""
    project = make_project(tmp_path / "project")
    archive = pack(tt_cmd, project)

    target = tmp_path / "target"
    target.mkdir()
    env = registry_env(ROCKS_REPO)

    first = run(tt_cmd, target, "package", "install", str(archive), env=env)
    assert first.returncode == 0, first.stderr

    again = run(tt_cmd, target, "package", "install", str(archive), env=env)
    assert again.returncode == 1
    assert "my-app" in again.stderr

    forced = run(tt_cmd, target, "package", "install", str(archive), "--force", env=env)
    assert forced.returncode == 0, forced.stderr
