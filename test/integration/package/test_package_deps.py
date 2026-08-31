"""End-to-end coverage for `tt package add`, `remove` and `update`.

Every test here drives the real binary against a real resolution: the fixture
rock repository is served over loopback and the manifest's `registry` key points
one dependency at it, so the closure is resolved for real without a network. The
two commands that need no server at all — removing the last dependency, and any
run that is refused before it resolves — are exercised directly.
"""

import hashlib
import re
from pathlib import Path

import tomllib

MANIFEST_HEAD = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'
"""

MANIFEST_TAIL = """
[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
"""

# A comment the editor must leave alone. It sits above the dependency table, so
# a splice that took one line too many would eat it.
KEEP_COMMENT = "# keep me: the editor must not touch this line."

LOCK_TEMPLATE = """lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0'
manifest_hash = 'sha256:{hash}'

[[lock.products.default.dependencies]]
name = 'stat'
version = '{version}'
source = 'registry'
"""


def local_dependency(table: str, server: str, constraint: str) -> str:
    """Render a long-form dependency pinned to the loopback rock server."""
    return (
        f"\n{KEEP_COMMENT}\n"
        f"[{table}.stat]\n"
        "source = 'registry'\n"
        f"registry = '{server}'\n"
        f"version = '{constraint}'\n"
    )


def write_manifest(root: Path, body: str) -> str:
    """Write a manifest built from head + body + tail and return its text."""
    text = MANIFEST_HEAD + body + MANIFEST_TAIL
    (root / "app.manifest.toml").write_text(text)
    return text


def read_lock(root: Path) -> dict:
    with (root / "app.manifest.lock").open("rb") as handle:
        return tomllib.load(handle)


def locked_versions(root: Path) -> dict[str, str]:
    """The default product's closure as name -> version."""
    lock = read_lock(root)
    closure = lock["lock"]["products"]["default"].get("dependencies", [])
    return {entry["name"]: entry["version"] for entry in closure}


def manifest_hash(root: Path) -> str:
    """The hash the lock must carry: sha256 over the manifest's raw bytes."""
    digest = hashlib.sha256((root / "app.manifest.toml").read_bytes()).hexdigest()
    return f"sha256:{digest}"


def test_add_rewrites_the_manifest_and_the_lock(tree, run_tt, rock_server):
    """`add` on a declared name rewrites its constraint and re-resolves."""
    root = tree.root
    write_manifest(root, local_dependency("dependencies", rock_server, "==0.3.1"))

    result = run_tt("package", "add", "stat", ">=0.3.2")
    assert result.returncode == 0, result.stderr

    source = (root / "app.manifest.toml").read_text()
    assert 'version = ">=0.3.2"' in source
    # Everything else in the declaration, and the comment above it, is untouched.
    assert f"registry = '{rock_server}'" in source
    assert KEEP_COMMENT in source

    assert locked_versions(root) == {"stat": "0.3.2-1"}
    # The lock must record the hash of the manifest as it now stands, not the
    # one it had before the edit; otherwise the very next command sees a stale
    # lock and re-resolves for no reason.
    assert read_lock(root)["manifest_hash"] == manifest_hash(root)

    assert "changed stat in [dependencies]: ==0.3.1 -> >=0.3.2" in result.stdout


def test_add_dev_lands_in_dev_dependencies(tree, run_tt, rock_server):
    """`--dev` targets [dev_dependencies] and leaves [dependencies] alone."""
    root = tree.root
    write_manifest(root, local_dependency("dev_dependencies", rock_server, "==0.3.1"))

    result = run_tt("package", "add", "--dev", "stat", ">=0.3.2")
    assert result.returncode == 0, result.stderr
    assert "[dev_dependencies]" in result.stdout

    with (root / "app.manifest.toml").open("rb") as handle:
        parsed = tomllib.load(handle)

    assert parsed["dev_dependencies"]["stat"]["version"] == ">=0.3.2"
    assert "dependencies" not in parsed


def test_add_of_a_new_name_declares_it_in_the_chosen_table(tree, run_tt):
    """A brand-new entry is spliced into the table the flag names.

    The rock does not exist on any server, so this also pins the documented
    order: the edit the user asked for reaches disk first, and a resolution that
    then fails leaves the lock alone and says so.
    """
    root = tree.root
    write_manifest(root, "")

    result = run_tt("package", "add", "--dev", "tt-9958-no-such-rock", ">=1.0.0")
    assert result.returncode == 1
    assert "the manifest was edited but the lock was not updated" in result.stderr

    source = (root / "app.manifest.toml").read_text()
    assert re.search(r"\[dev_dependencies\]\s*\ntt-9958-no-such-rock = \">=1.0.0\"", source)

    with (root / "app.manifest.toml").open("rb") as handle:
        parsed = tomllib.load(handle)

    assert "tt-9958-no-such-rock" in parsed["dev_dependencies"]
    assert "dependencies" not in parsed
    assert not (root / "app.manifest.lock").exists()


def test_add_failure_does_not_touch_an_existing_lock(tree, run_tt):
    """A failed resolution leaves the lock exactly as it was."""
    root = tree.root
    write_manifest(root, "")
    before = LOCK_TEMPLATE.format(hash="0" * 64, version="0.3.1-1")
    (root / "app.manifest.lock").write_text(before)

    result = run_tt("package", "add", "tt-9958-no-such-rock")
    assert result.returncode == 1
    assert (root / "app.manifest.lock").read_text() == before


def test_remove_clears_both_files(tree, run_tt, rock_server):
    """`remove` drops the declaration and re-resolves the lock without it.

    Nothing is left to resolve afterwards, so this one needs no server at all —
    the fixture only supplies a registry the removed entry pointed at.
    """
    root = tree.root
    write_manifest(root, local_dependency("dependencies", rock_server, ">=0.3.1"))
    (root / "app.manifest.lock").write_text(
        LOCK_TEMPLATE.format(hash="0" * 64, version="0.3.1-1"),
    )

    result = run_tt("package", "remove", "stat")
    assert result.returncode == 0, result.stderr

    with (root / "app.manifest.toml").open("rb") as handle:
        parsed = tomllib.load(handle)

    assert "dependencies" not in parsed
    assert KEEP_COMMENT in (root / "app.manifest.toml").read_text()

    assert locked_versions(root) == {}
    assert read_lock(root)["manifest_hash"] == manifest_hash(root)
    # The rock that left the closure is reported, not silently dropped.
    assert "stat 0.3.1-1 -> (dropped)" in result.stdout


def test_remove_of_an_undeclared_name_fails(tree, run_tt):
    """An unknown name is exit 1, not a no-op — it is almost always a typo."""
    root = tree.root
    text = write_manifest(root, "")

    result = run_tt("package", "remove", "nosuchrock")
    assert result.returncode == 1
    assert "not declared in the manifest" in result.stderr
    # Nothing was written: neither file moved.
    assert (root / "app.manifest.toml").read_text() == text
    assert not (root / "app.manifest.lock").exists()


def test_update_pulls_a_newer_version(tree, run_tt, rock_server):
    """A bare `update` takes the newest version the constraint allows."""
    root = tree.root
    write_manifest(root, local_dependency("dependencies", rock_server, ">=0.3.1"))
    (root / "app.manifest.lock").write_text(
        LOCK_TEMPLATE.format(hash="0" * 64, version="0.3.1-1"),
    )

    text_before = (root / "app.manifest.toml").read_text()

    result = run_tt("package", "update")
    assert result.returncode == 0, result.stderr

    assert locked_versions(root) == {"stat": "0.3.2-1"}
    assert "stat 0.3.1-1 -> 0.3.2-1" in result.stdout
    # update changes no declaration, so the manifest is byte-for-byte the same.
    assert (root / "app.manifest.toml").read_text() == text_before


def test_update_of_one_name_resolves_it(tree, run_tt, rock_server):
    """`update NAME` frees that rock; the rest of the closure is held."""
    root = tree.root
    write_manifest(root, local_dependency("dependencies", rock_server, ">=0.3.1"))
    (root / "app.manifest.lock").write_text(
        LOCK_TEMPLATE.format(hash="0" * 64, version="0.3.1-1"),
    )

    result = run_tt("package", "update", "stat")
    assert result.returncode == 0, result.stderr
    assert locked_versions(root) == {"stat": "0.3.2-1"}


def test_update_of_an_undeclared_name_fails(tree, run_tt):
    """`update NAME` refuses a name the manifest does not declare."""
    root = tree.root
    write_manifest(root, "")

    result = run_tt("package", "update", "nosuchrock")
    assert result.returncode == 1
    assert "not declared in the manifest" in result.stderr
