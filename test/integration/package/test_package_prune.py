"""tt package build and fetch bring .rocks/ in step with the lock.

A rock the lock no longer holds - removed with tt package remove, or left
behind by a lock that moved a rock to another version - used to stay in the
project's tree, and require could load code the lock does not have. The
materializing commands now remove it, through its rock_manifest, and say so.
What the lock of the project never held is not theirs to remove: the guest
packages tt package install put into the project, and what their locks pin,
stay.

Everything resolves against local registries - the fixture repository the
other suites use, and one each test lays out itself - so no test needs a
network.
"""

import hashlib
import io
import os
import subprocess
import zipfile
from pathlib import Path

ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

# stat is the only rock the fixture repository serves; 0.3.2-1 is its newest.
STAT_VERSION = "0.3.2-1"

MANIFEST = """manifest_version = '0.1'

[package]
name = '{name}'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'
{dependencies}
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


def make_project(root: Path, dependencies: str, name: str = "my-app") -> Path:
    """Write a project of one Lua component and the dependency tables given."""
    (root / "src").mkdir(parents=True)
    (root / "src" / "init.lua").write_text("return {}\n")
    (root / "app.manifest.toml").write_text(
        MANIFEST.format(name=name, dependencies=dependencies),
    )
    return root


def tt_ok(tt_cmd, cwd: Path, *args: str, env: dict | None = None) -> subprocess.CompletedProcess:
    result = run(tt_cmd, cwd, *args, env=env)
    assert result.returncode == 0, result.stderr
    return result


def share(project: Path) -> Path:
    return project / ".rocks" / "share" / "tarantool"


def rock_files(project: Path, name: str) -> list[str]:
    """Every path of the tree that belongs to the rock name by its path."""
    rocks = project / ".rocks"
    found = []
    for path in rocks.rglob("*"):
        rel = path.relative_to(rocks).as_posix()
        if f"/{name}/" in f"/{rel}/" or Path(rel).name.startswith(f"{name}."):
            found.append(rel)
    return sorted(found)


def tree_manifest(project: Path) -> str:
    return (share(project) / "rocks" / "manifest").read_text()


def test_build_prunes_a_removed_dependency(tt_cmd, tmp_path):
    """remove, then build: the rock leaves the tree and its manifest."""
    project = make_project(tmp_path / "project", "\n[dependencies]\nstat = '>=0.3.0'\n")

    tt_ok(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))
    assert (share(project) / "stat" / "init.lua").exists()

    tt_ok(tt_cmd, project, "package", "remove", "stat", "--registry", str(ROCKS_REPO))
    result = tt_ok(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))

    assert f"removed unused dependency stat {STAT_VERSION}" in result.stdout
    assert rock_files(project, "stat") == []
    assert '"stat"' not in tree_manifest(project) and "stat =" not in tree_manifest(project)
    # The build's own rock is the project's, not the lock's.
    assert (share(project) / "my-app" / "init.lua").exists()
    assert (share(project) / "my-app" / "version.lua").exists()

    # Once pruned, there is nothing left to report.
    again = tt_ok(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))
    assert "removed" not in again.stdout


def test_fetch_prunes_a_removed_dependency(tt_cmd, tmp_path):
    """remove, then fetch: fetch materializes from the lock and prunes too."""
    project = make_project(tmp_path / "project", "\n[dependencies]\nstat = '>=0.3.0'\n")

    tt_ok(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))
    tt_ok(tt_cmd, project, "package", "remove", "stat", "--registry", str(ROCKS_REPO))

    result = tt_ok(tt_cmd, project, "package", "fetch", "--registry", str(ROCKS_REPO))

    assert f"removed unused dependency stat {STAT_VERSION}" in result.stdout
    assert rock_files(project, "stat") == []
    assert (share(project) / "my-app" / "init.lua").exists()


def md5(data: bytes) -> str:
    return hashlib.md5(data).hexdigest()


def write_rock(registry: Path, name: str, version: str, modules: dict[str, str]) -> None:
    """Write a binary rock of name holding the Lua modules given, as files of
    lua/<name>/, with the rockspec and the rock_manifest LuaRocks packs."""
    spec = (
        f"package = '{name}'\nversion = '{version}'\n"
        "source = { url = 'file://.' }\n"
        "build = { type = 'builtin', modules = {} }\n"
    ).encode()
    spec_name = f"{name}-{version}.rockspec"

    entries = "".join(
        f'         ["{module}"] = "{md5(body.encode())}",\n'
        for module, body in sorted(modules.items())
    )
    rock_manifest = (
        "rock_manifest = {\n"
        f"   lua = {{\n      {name} = {{\n{entries}      }}\n   }},\n"
        f'   ["{spec_name}"] = "{md5(spec)}"\n'
        "}\n"
    )

    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w") as rock:
        rock.writestr(spec_name, spec)
        rock.writestr("rock_manifest", rock_manifest)
        for module, body in modules.items():
            rock.writestr(f"lua/{name}/{module}", body)

    (registry / f"{name}-{version}.all.rock").write_bytes(buffer.getvalue())


def write_registry_manifest(registry: Path, name: str, versions: list[str]) -> None:
    listed = "".join(f'      ["{v}"] = {{ {{ arch = "all" }} }},\n' for v in versions)
    (registry / "manifest").write_text(
        f"commands = {{}}\nmodules = {{}}\nrepository = {{\n   {name} = {{\n{listed}   }}\n}}\n",
    )


def shrinking_registry(root: Path) -> Path:
    """A registry of shrink: 1.0.0 ships init.lua and extra.lua, 2.0.0 only
    init.lua."""
    root.mkdir(parents=True)
    write_rock(
        root,
        "shrink",
        "1.0.0-1",
        {"init.lua": "return 'one'\n", "extra.lua": "return 'extra'\n"},
    )
    write_rock(root, "shrink", "2.0.0-1", {"init.lua": "return 'two'\n"})
    write_registry_manifest(root, "shrink", ["1.0.0-1", "2.0.0-1"])
    return root


def test_a_version_change_leaves_no_stale_files(tt_cmd, tmp_path):
    """A lock that moves a rock to a version of fewer files leaves none of
    the old version's files behind, in either direction."""
    registry = shrinking_registry(tmp_path / "registry")
    project = make_project(tmp_path / "project", "\n[dependencies]\nshrink = '==1.0.0'\n")
    module = share(project) / "shrink"

    tt_ok(tt_cmd, project, "package", "build", "--registry", str(registry))
    assert (module / "extra.lua").read_text() == "return 'extra'\n"

    tt_ok(tt_cmd, project, "package", "add", "shrink", "==2.0.0", "--registry", str(registry))
    result = tt_ok(tt_cmd, project, "package", "build", "--registry", str(registry))

    assert "removed shrink 1.0.0-1" in result.stdout
    assert (module / "init.lua").read_text() == "return 'two'\n"
    assert rock_files(project, "shrink") == [
        "share/tarantool/rocks/shrink",
        "share/tarantool/rocks/shrink/2.0.0-1",
        "share/tarantool/rocks/shrink/2.0.0-1/rock_manifest",
        "share/tarantool/rocks/shrink/2.0.0-1/shrink-2.0.0-1.rockspec",
        "share/tarantool/shrink",
        "share/tarantool/shrink/init.lua",
    ]

    # Back to the version of more files: they all come back, at its contents.
    tt_ok(tt_cmd, project, "package", "add", "shrink", "==1.0.0", "--registry", str(registry))
    tt_ok(tt_cmd, project, "package", "build", "--registry", str(registry))

    assert (module / "init.lua").read_text() == "return 'one'\n"
    assert (module / "extra.lua").read_text() == "return 'extra'\n"
    assert not (share(project) / "rocks" / "shrink" / "2.0.0-1").exists()


def test_a_dev_dependency_is_kept_by_build(tt_cmd, tmp_path):
    """Build materializes the dev closure too, so it does not prune it - also
    not when a rock moves from [dependencies] to [dev_dependencies]."""
    project = make_project(tmp_path / "project", "\n[dependencies]\nstat = '>=0.3.0'\n")

    tt_ok(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))
    tt_ok(tt_cmd, project, "package", "remove", "stat", "--registry", str(ROCKS_REPO))
    add = ("package", "add", "--dev", "stat", ">=0.3.0")
    tt_ok(tt_cmd, project, *add, "--registry", str(ROCKS_REPO))

    for _ in range(2):
        result = tt_ok(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO))
        assert "removed" not in result.stdout
        assert (share(project) / "stat" / "init.lua").exists()


def test_a_guest_dependency_survives_a_build_of_the_host(tt_cmd, tmp_path):
    """A guest's dependency is the guest's: the host dropping it from its own
    lock does not remove it, and neither the guest itself."""
    guest = make_project(
        tmp_path / "guest",
        "\n[dependencies]\nstat = '>=0.3.0'\n",
        name="monitoring",
    )
    tt_ok(tt_cmd, guest, "package", "pack", "--without-deps", "--registry", str(ROCKS_REPO))
    archives = list((guest / "_build" / "pack").glob("*.tt"))
    assert len(archives) == 1, archives

    host = make_project(tmp_path / "host", "\n[dependencies]\nstat = '>=0.3.0'\n")
    tt_ok(tt_cmd, host, "package", "build", "--registry", str(ROCKS_REPO))
    tt_ok(
        tt_cmd,
        host,
        "package",
        "install",
        str(archives[0]),
        env={**os.environ, "TT_REGISTRIES": str(ROCKS_REPO)},
    )
    assert (share(host) / "monitoring" / "init.lua").exists()

    tt_ok(tt_cmd, host, "package", "remove", "stat", "--registry", str(ROCKS_REPO))
    result = tt_ok(tt_cmd, host, "package", "build", "--registry", str(ROCKS_REPO))

    assert "removed" not in result.stdout
    assert (share(host) / "stat" / "init.lua").exists()
    assert (share(host) / "monitoring" / "init.lua").exists()
    assert (share(host) / "my-app" / "init.lua").exists()

    # Its dependency goes with the guest, as uninstall decides.
    uninstalled = tt_ok(tt_cmd, host, "package", "uninstall", "monitoring", "--yes")
    assert "removed unused dependency stat" in uninstalled.stdout
    assert rock_files(host, "stat") == []
