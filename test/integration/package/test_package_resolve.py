"""End-to-end coverage for `tt package resolve` and `tt package deps`.

`resolve` drives a real resolution against the fixture rock repository served
over loopback, exactly like the add/remove/update suite next to it: the
manifest's `registry` key points the one dependency at that server, so the
closure is resolved for real without a network.

`deps` resolves nothing at all — it reads the manifest and the lock — so its
tests build the two files by hand and never start a server. That is as much the
property under test as the output is: the command has to answer from disk.
"""

import hashlib
import json
from pathlib import Path

import yaml

try:
    import tomllib
except ModuleNotFoundError:  # Python < 3.11, which is what CI runs.
    import tomli as tomllib

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

# A comment the resolve path must leave alone: it changes no declaration, so the
# manifest has to come back byte for byte.
KEEP_COMMENT = "# keep me: resolve must not rewrite the manifest."

# The hash placeholder every lock fixture below carries; `current_lock` swaps it
# for the real one when the lock is meant to look current.
NO_HASH = "0" * 64

LOCK_HEAD = f"""lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0'
manifest_hash = 'sha256:{NO_HASH}'
"""

LOCK_ENTRY = """
[[lock.products.default.dependencies]]
name = '{name}'
version = '{version}'
source = 'registry'
"""


def local_dependency(server: str, constraint: str) -> str:
    """Render a long-form dependency pinned to the loopback rock server."""
    return (
        f"\n{KEEP_COMMENT}\n"
        "[dependencies.stat]\n"
        "source = 'registry'\n"
        f"registry = '{server}'\n"
        f"version = '{constraint}'\n"
    )


def write_manifest(root: Path, body: str, tail: str = MANIFEST_TAIL) -> str:
    """Write a manifest built from head + body + tail and return its text."""
    text = MANIFEST_HEAD + body + tail
    (root / "app.manifest.toml").write_text(text)
    return text


def render_lock(pins: dict[str, str]) -> str:
    return LOCK_HEAD + "".join(
        LOCK_ENTRY.format(name=name, version=version) for name, version in sorted(pins.items())
    )


def write_lock(root: Path, pins: dict[str, str], *, current: bool) -> None:
    """Write a lock over the given pins.

    With `current=True` it carries the hash of the manifest as it stands on
    disk, which is what makes tt read it as up to date; otherwise it keeps the
    all-zero placeholder, which no manifest can hash to, and tt reads it as
    stale.
    """
    text = render_lock(pins)
    if current:
        digest = hashlib.sha256((root / "app.manifest.toml").read_bytes()).hexdigest()
        text = text.replace(NO_HASH, digest)
    (root / "app.manifest.lock").write_text(text)


def read_lock(root: Path) -> dict:
    with (root / "app.manifest.lock").open("rb") as handle:
        return tomllib.load(handle)


def locked_versions(root: Path, product: str = "default") -> dict[str, str]:
    """One product's closure as name -> version."""
    closure = read_lock(root)["lock"]["products"][product].get("dependencies", [])
    return {entry["name"]: entry["version"] for entry in closure}


def manifest_hash(root: Path) -> str:
    digest = hashlib.sha256((root / "app.manifest.toml").read_bytes()).hexdigest()
    return f"sha256:{digest}"


def test_resolve_rewrites_the_lock_without_building(tree, run_tt, rock_server):
    """`resolve` brings a hand-edited manifest's lock back in step.

    Nothing is materialized into `.rocks/` and no backend runs: the point of the
    command is that it costs a resolution and nothing more.
    """
    root = tree.root
    write_manifest(root, local_dependency(rock_server, ">=0.3.1"))

    result = run_tt("package", "resolve")
    assert result.returncode == 0, result.stderr

    assert locked_versions(root) == {"stat": "0.3.2-1"}
    # The lock records the manifest as it stands, so the next command does not
    # find it stale.
    assert read_lock(root)["manifest_hash"] == manifest_hash(root)
    # And the manifest is byte for byte what the user wrote.
    assert KEEP_COMMENT in (root / "app.manifest.toml").read_text()


def test_resolve_holds_the_versions_the_lock_already_has(tree, run_tt, rock_server):
    """A resolve is not an update: a locked version that still fits is kept.

    `stat` is served at 0.3.1 and 0.3.2 and the manifest allows both. An
    unpinned re-resolution would take 0.3.2; holding what the lock has is what
    keeps a hand edit to one dependency from moving the others.
    """
    root = tree.root
    write_manifest(root, local_dependency(rock_server, ">=0.3.1"))
    write_lock(root, {"stat": "0.3.1-1"}, current=True)

    result = run_tt("package", "resolve")
    assert result.returncode == 0, result.stderr

    assert locked_versions(root) == {"stat": "0.3.1-1"}
    assert "the lock is up to date" in result.stdout


def test_resolve_holds_pins_from_a_stale_lock(tree, run_tt, rock_server):
    """The pins hold even when the lock is the stale one that prompted the run.

    This is the case the command exists for — a manifest edited by hand, so its
    hash no longer matches — and it is where holding the pins matters most: the
    edit is allowed to move the closure, the rest of it is not. `stat` is served
    at 0.3.2 as well, and an unpinned resolution would take it.
    """
    root = tree.root
    write_manifest(root, local_dependency(rock_server, ">=0.3.1"))
    write_lock(root, {"stat": "0.3.1-1"}, current=False)

    result = run_tt("package", "resolve")
    assert result.returncode == 0, result.stderr

    assert locked_versions(root) == {"stat": "0.3.1-1"}
    # Stale no more: the rewritten lock carries the current manifest's hash.
    assert read_lock(root)["manifest_hash"] == manifest_hash(root)


def test_resolve_satisfies_a_locked_build(tree, run_tt, rock_server):
    """The lock `resolve` writes is one a `--locked` build accepts as it is.

    A `--locked` build refuses a stale lock and re-resolves nothing, so it
    passing over the freshly resolved lock — and leaving its pins alone — is the
    assertion that `resolve` produces what a build would have.

    The project has never resolved, so nothing is pinned and the resolution is
    the same unpinned one a first build would run.
    """
    root = tree.root
    write_manifest(root, local_dependency(rock_server, ">=0.3.1"))

    resolved = run_tt("package", "resolve")
    assert resolved.returncode == 0, resolved.stderr
    after_resolve = read_lock(root)

    build = run_tt("package", "build", "--locked")
    assert build.returncode == 0, build.stderr

    assert read_lock(root)["lock"] == after_resolve["lock"]
    assert locked_versions(root) == {"stat": "0.3.2-1"}


def test_resolve_without_a_manifest_fails(run_tt):
    """A directory holding no package is an error, not an empty success."""
    result = run_tt("package", "resolve")

    assert result.returncode == 1
    assert "app.manifest.toml" in result.stderr


def test_deps_reports_declared_and_locked_versions(tree, run_tt):
    """`deps` answers from the manifest and the lock, with no server at all."""
    root = tree.root
    write_manifest(root, "\n[dependencies]\nstat = '>=0.3.1'\n")
    write_lock(root, {"stat": "0.3.2-1", "luasocket": "3.0.0-1"}, current=True)

    result = run_tt("package", "deps")
    assert result.returncode == 0, result.stderr

    # Piped output defaults to YAML, so this is parseable without a flag.
    report = yaml.safe_load(result.stdout)
    assert report["package"] == "my-app"
    assert report["lock"] == "current"

    entries = {entry["name"]: entry for entry in report["products"][0]["dependencies"]}
    assert entries["stat"]["constraint"] == ">=0.3.1"
    assert entries["stat"]["version"] == "0.3.2-1"
    assert entries["stat"]["direct"] is True
    # Nothing declares luasocket: it is in the closure behind stat.
    assert entries["luasocket"]["version"] == "3.0.0-1"
    assert entries["luasocket"]["direct"] is False


def test_deps_json_is_valid(tree, run_tt):
    """`-o json` is the machine-readable contract; it has to parse."""
    root = tree.root
    write_manifest(
        root,
        "\n[dependencies]\nstat = '>=0.3.1'\n\n[dev_dependencies]\nluatest = '*'\n",
    )

    result = run_tt("package", "deps", "-o", "json")
    assert result.returncode == 0, result.stderr

    report = json.loads(result.stdout)
    assert report["package"] == "my-app"
    # Never resolved: the declarations are known, the versions are not.
    assert report["lock"] == "missing"

    declared = report["products"][0]["dependencies"][0]
    assert declared["name"] == "stat"
    assert "version" not in declared
    assert report["dev_dependencies"][0]["name"] == "luatest"


def test_deps_reports_a_stale_lock_rather_than_re_resolving(tree, run_tt):
    """A lock the manifest moved away from is labelled, not silently refreshed.

    The command contacts no registry, so presenting its versions as current
    would be a lie — and re-resolving behind the user's back would make a
    read-only command write files.
    """
    root = tree.root
    write_manifest(root, "\n[dependencies]\nstat = '>=0.3.1'\n")
    write_lock(root, {"stat": "0.3.1-1"}, current=False)
    before = (root / "app.manifest.lock").read_text()

    result = run_tt("package", "deps")
    assert result.returncode == 0, result.stderr

    report = yaml.safe_load(result.stdout)
    assert report["lock"] == "stale"
    assert "manifest changed" in report["lock_reason"]
    # Read-only: the lock on disk is untouched.
    assert (root / "app.manifest.lock").read_text() == before


def test_deps_reports_each_product_separately(tree, run_tt):
    """Products hold independent closures, so each is reported on its own."""
    root = tree.root
    write_manifest(
        root,
        "\n[dependencies]\nstat = '>=0.3.1'\n",
        tail="""
[products.default]
components = ['lua']
default = true

[products.extra]
components = ['native']

[components.lua]
path = '.'

[components.native]
path = 'native'

[components.native.dependencies]
luasocket = '>=3.0.0'
""",
    )

    result = run_tt("package", "deps", "-o", "json")
    assert result.returncode == 0, result.stderr

    products = {product["name"]: product for product in json.loads(result.stdout)["products"]}
    assert set(products) == {"default", "extra"}

    def names(product: str) -> set[str]:
        return {entry["name"] for entry in products[product]["dependencies"]}

    # The global table applies to both; the component's table only to the
    # product built from that component.
    assert names("default") == {"stat"}
    assert names("extra") == {"stat", "luasocket"}


def test_deps_without_a_manifest_fails(run_tt):
    """Same as resolve: no package here is an error, not an empty report."""
    result = run_tt("package", "deps")

    assert result.returncode == 1
    assert "app.manifest.toml" in result.stderr


def test_deps_needs_no_tarantool(tree, run_tt):
    """The report is a read of two files, so no tarantool has to be around.

    `PATH` is emptied, which is what a machine that never installed Tarantool
    looks like to the command. Every re-resolving command fails there; this one
    must not, because it resolves nothing.
    """
    root = tree.root
    write_manifest(root, "\n[dependencies]\nstat = '>=0.3.1'\n")

    result = run_tt("package", "deps", "-o", "json", env={"PATH": "/nonexistent"})
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout)["package"] == "my-app"
