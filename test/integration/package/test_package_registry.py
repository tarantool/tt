"""End-to-end coverage for `tt package search` and `tt package download`.

Both talk to a rock server for real; the fixture repository served over loopback
is the only "internet" this suite has.

The download tests are the interesting ones. `download` is a mirror builder, not
a file fetcher: what makes the files it writes useful is the LuaRocks `manifest`
written beside them, which turns the directory into a rock server. The
acceptance test proves exactly that and nothing weaker — it stops the loopback
server before building, so a build that succeeds cannot have reached the
network.
"""

import json
import os
from pathlib import Path

import yaml

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'
{registries}
[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'

[dependencies]
stat = '>=0.3.1'
"""


def write_manifest(root: Path, registries: str = "") -> None:
    """Write a manifest whose one dependency is served by the fixture repo."""
    block = f"registries = ['{registries}']\n" if registries else ""
    (root / "app.manifest.toml").write_text(MANIFEST.format(registries=block))


def rock_files(directory: Path) -> list[str]:
    """Return the rock file names a mirror directory holds."""
    return sorted(path.name for path in directory.glob("*.rock"))


def test_search_reports_every_offered_version(run_tt, rock_server):
    """A search asks every server and reports each version it offers."""
    result = run_tt("package", "search", "stat", "--registry", rock_server, "-o", "json")
    assert result.returncode == 0, result.stderr

    matches = json.loads(result.stdout)
    assert {match["version"] for match in matches} == {"0.3.1-1", "0.3.2-1"}
    assert {match["name"] for match in matches} == {"stat"}


def test_search_without_a_match_succeeds(run_tt, rock_server):
    """A term nothing matches is an answer, not a failure.

    The format is named explicitly: output is captured here, so the default
    would be YAML, and the empty-stdout property belongs to the table.
    """
    result = run_tt(
        "package",
        "search",
        "unpublished",
        "--registry",
        rock_server,
        "-o",
        "table",
    )

    assert result.returncode == 0, result.stderr
    # The table form keeps stdout clean so a redirected listing is the listing.
    assert result.stdout == ""
    assert "unpublished" in result.stderr


def test_search_without_a_match_prints_an_empty_list(run_tt, rock_server):
    """The machine formats answer with an empty document, not an empty file."""
    result = run_tt(
        "package",
        "search",
        "unpublished",
        "--registry",
        rock_server,
        "-o",
        "json",
    )

    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == []


def test_download_writes_a_rock_and_indexes_it(tree, run_tt, rock_server):
    """A named download lands in --dir together with the manifest indexing it."""
    result = run_tt(
        "package",
        "download",
        "stat@0.3.1-1",
        "--dir",
        "./mirror",
        "--registry",
        rock_server,
    )
    assert result.returncode == 0, result.stderr

    mirror = tree.root / "mirror"
    assert rock_files(mirror) == ["stat-0.3.1-1.all.rock"]
    # The index is what makes the directory a server rather than a pile of
    # files; without it nothing downstream can read the mirror.
    assert (mirror / "manifest").is_file()
    # Every written path goes to stdout so the run can be piped.
    assert str(mirror / "stat-0.3.1-1.all.rock") in result.stdout


def test_download_without_a_lock_names_the_command_that_writes_one(tree, run_tt):
    """The no-argument form mirrors a closure, so it needs one to exist."""
    write_manifest(tree.root)

    result = run_tt("package", "download", "--dir", "./mirror")

    assert result.returncode == 1
    assert "tt package resolve" in result.stderr


def test_download_mirrors_the_locked_closure(tree, run_tt, rock_server):
    """With no arguments the lock decides what is mirrored, at its versions."""
    write_manifest(tree.root, registries=rock_server)

    resolved = run_tt("package", "resolve")
    assert resolved.returncode == 0, resolved.stderr

    result = run_tt("package", "download", "--dir", "./mirror")
    assert result.returncode == 0, result.stderr

    # The lock resolves >=0.3.1 to the newest offering, and the mirror carries
    # exactly what the lock pinned - not every version the server has.
    assert rock_files(tree.root / "mirror") == ["stat-0.3.2-1.all.rock"]


def test_download_is_idempotent(tree, run_tt, rock_server):
    """Re-running overwrites and re-indexes instead of failing or duplicating."""
    write_manifest(tree.root, registries=rock_server)
    assert run_tt("package", "resolve").returncode == 0

    first = run_tt("package", "download", "--dir", "./mirror")
    assert first.returncode == 0, first.stderr

    second = run_tt("package", "download", "--dir", "./mirror")
    assert second.returncode == 0, second.stderr

    assert first.stdout == second.stdout
    assert rock_files(tree.root / "mirror") == ["stat-0.3.2-1.all.rock"]


def test_a_mirror_builds_the_project_offline(tree, run_tt, rock_server, stop_rock_server):
    """The point of the whole command: resolve, mirror, then build with no network.

    The loopback server is stopped before the build, so a build that succeeds
    cannot have reached it. What is left is the directory the download wrote,
    reached through TT_REGISTRIES.
    """
    write_manifest(tree.root, registries=rock_server)
    assert run_tt("package", "resolve").returncode == 0

    downloaded = run_tt("package", "download", "--dir", "./mirror")
    assert downloaded.returncode == 0, downloaded.stderr

    stop_rock_server()

    built = run_tt("package", "build", "--locked", env=with_registries("./mirror"))
    assert built.returncode == 0, built.stderr

    # The rock is installed from the mirror, not merely resolved from it.
    assert (tree.root / ".rocks" / "share" / "tarantool" / "stat" / "init.lua").is_file()


def test_a_mirror_works_as_a_registry_flag(tree, run_tt, rock_server, stop_rock_server):
    """The same directory reached through --registry rather than the environment."""
    write_manifest(tree.root, registries=rock_server)
    assert run_tt("package", "resolve").returncode == 0
    assert run_tt("package", "download", "--dir", "./mirror").returncode == 0

    stop_rock_server()

    built = run_tt("package", "build", "--locked", "--registry", "./mirror")
    assert built.returncode == 0, built.stderr
    assert (tree.root / ".rocks" / "share" / "tarantool" / "stat" / "init.lua").is_file()


def test_registry_list_reports_the_layer_a_server_came_from(tree, run_tt, rock_server):
    """The listing has to name the layer, not just the URL."""
    write_manifest(tree.root, registries=rock_server)

    from_manifest = run_tt("registry", "list", "-o", "json")
    assert from_manifest.returncode == 0, from_manifest.stderr
    assert json.loads(from_manifest.stdout) == [
        {"url": rock_server, "source": "manifest"},
    ]

    from_env = run_tt(
        "registry",
        "list",
        "-o",
        "yaml",
        env=with_registries("./mirror"),
    )
    assert from_env.returncode == 0, from_env.stderr
    listed = yaml.safe_load(from_env.stdout)
    assert [entry["source"] for entry in listed] == ["env"]
    # A relative entry is reported as the absolute path it resolves to, which is
    # what the engine is actually handed.
    assert listed[0]["url"] == str(tree.root / "mirror")

    from_flag = run_tt("registry", "list", "-o", "json", "--registry", "./mirror")
    assert from_flag.returncode == 0, from_flag.stderr
    assert json.loads(from_flag.stdout) == [
        {"url": str(tree.root / "mirror"), "source": "flag"},
    ]


def with_registries(value: str) -> dict[str, str]:
    """The environment for a run that sets TT_REGISTRIES.

    `run_tt`'s `env` replaces the environment rather than adding to it, so a run
    that sets one variable still has to carry everything else — the PATH
    tarantool is found on above all.
    """
    return dict(os.environ, TT_REGISTRIES=value)
