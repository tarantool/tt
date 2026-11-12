"""End-to-end coverage for `tt package resolve` and `tt package deps`.

`resolve` drives a real resolution against the fixture rock repository served
over loopback, exactly like the add/remove/update suite next to it: the
manifest's `registry` key points the one dependency at that server, so the
closure is resolved for real without a network.

`deps` resolves nothing at all — it reads the manifest and the lock — so its
tests build the two files by hand and never start a server. That is as much the
property under test as the output is: the command has to answer from disk.

A rock server that publishes a `tt-rocks-index.json` is a directory here: the
rocks are real ones, packed by `tt wasm rock --from` from a module of a few
bytes, laid out as on release pages and named by relative URLs.
"""

import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import tomllib
from pathlib import Path

import pytest
import yaml

from utils import manifest_hash as manifest_hash_of
from utils import sha256_of, write_rock_index

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

# The hash placeholder every lock fixture below carries, of the scheme tt
# writes, which no manifest hashes to; `write_lock` swaps it for the real one
# when the lock is meant to look current.
NO_HASH = "toml1-sha256:" + "0" * 64

LOCK_HEAD = f"""lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0'
manifest_hash = '{NO_HASH}'
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
        text = text.replace(NO_HASH, manifest_hash(root))
    (root / "app.manifest.lock").write_text(text)


def read_lock(root: Path) -> dict:
    with (root / "app.manifest.lock").open("rb") as handle:
        return tomllib.load(handle)


def locked_versions(root: Path, product: str = "default") -> dict[str, str]:
    """One product's closure as name -> version."""
    closure = read_lock(root)["lock"]["products"][product].get("dependencies", [])
    return {entry["name"]: entry["version"] for entry in closure}


def manifest_hash(root: Path) -> str:
    return manifest_hash_of(root / "app.manifest.toml")


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
    # The lock by hand is not the one tt writes - its generated_by names
    # another tt, for one - so the run says what it rewrote, not that the
    # lock is up to date; a second run finds it so.
    assert "no version moved, but the lock changed:" in result.stdout
    assert "up to date" not in result.stdout

    again = run_tt("package", "resolve")
    assert again.returncode == 0, again.stderr
    assert again.stdout.strip() == "the lock is up to date"


def test_a_comment_keeps_the_lock_current(tree, run_tt, rock_server):
    """manifest_hash is taken over what the manifest says, not over its bytes.

    A comment, another layout and another quoting change nothing a lock pins,
    so a `--locked` build takes the lock as it is and a resolve rewrites
    nothing; an edit of a value that pins nothing is a change all the same,
    and the resolve that follows writes the hash anew.
    """
    root = tree.root
    text = write_manifest(root, local_dependency(rock_server, ">=0.3.1"))

    resolved = run_tt("package", "resolve")
    assert resolved.returncode == 0, resolved.stderr
    lock = (root / "app.manifest.lock").read_text()

    reworded = "# My app.\n" + text.replace("name = 'my-app'", 'name = "my-app" # the rock')
    (root / "app.manifest.toml").write_text(reworded + "\n# The end.\n")

    build = run_tt("package", "build", "--locked")
    assert build.returncode == 0, build.stderr

    again = run_tt("package", "resolve")
    assert again.returncode == 0, again.stderr
    assert again.stdout.strip() == "the lock is up to date"
    assert (root / "app.manifest.lock").read_text() == lock

    described = reworded.replace("[package]\n", "[package]\ndescription = 'a memcached'\n")
    (root / "app.manifest.toml").write_text(described)

    stale = run_tt("package", "build", "--locked")
    assert stale.returncode == 1
    assert "manifest changed since the lock was written" in stale.stderr

    refreshed = run_tt("package", "resolve")
    assert refreshed.returncode == 0, refreshed.stderr
    assert refreshed.stdout.splitlines() == [
        "no version moved, but the lock changed:",
        "  manifest hash refreshed",
    ]
    assert read_lock(root)["manifest_hash"] == manifest_hash(root)


def test_resolve_names_a_path_dependency_it_repins(tree, run_tt):
    """A path dependency of files only has no version to move: the first lock
    names it, and a resolve after its files changed says it pinned it anew,
    rather than that the lock is up to date."""
    root = tree.root
    write_manifest(
        root,
        "\n[dependencies]\nhelper = { source = 'path', path = 'vendor/helper' }\n",
    )
    helper = root / "vendor" / "helper" / "init.lua"
    helper.parent.mkdir(parents=True)
    helper.write_text("return {}\n")

    first = run_tt("package", "resolve")
    assert first.returncode == 0, first.stderr
    assert first.stdout.splitlines() == [
        "no version moved, but the lock changed:",
        "  lock written; there was none",
        '  "helper" joined product "default"',
    ]

    again = run_tt("package", "resolve")
    assert again.returncode == 0, again.stderr
    assert again.stdout.strip() == "the lock is up to date"

    helper.write_text("return { changed = true }\n")

    repinned = run_tt("package", "resolve")
    assert repinned.returncode == 0, repinned.stderr
    assert repinned.stdout.splitlines() == [
        "no version moved, but the lock changed:",
        '  path dependency "helper" re-pinned: its files changed',
    ]


def test_a_lock_of_an_older_hash_scheme_is_stale(tree, run_tt, rock_server):
    """A lock whose manifest_hash an older tt took over the bytes of the file
    is out of date even for those very bytes: tt says it hashes the manifest
    differently now, and a resolve writes the scheme it uses."""
    root = tree.root
    write_manifest(root, local_dependency(rock_server, ">=0.3.1"))

    resolved = run_tt("package", "resolve")
    assert resolved.returncode == 0, resolved.stderr

    lock = root / "app.manifest.lock"
    digest = hashlib.sha256((root / "app.manifest.toml").read_bytes()).hexdigest()
    lock.write_text(lock.read_text().replace(manifest_hash(root), f"sha256:{digest}"))

    stale = run_tt("package", "build", "--locked")
    assert stale.returncode == 1
    assert "tt hashes the manifest differently now" in stale.stderr
    assert "run tt package resolve" in stale.stderr

    refreshed = run_tt("package", "resolve")
    assert refreshed.returncode == 0, refreshed.stderr
    assert "  manifest hash refreshed" in refreshed.stdout.splitlines()
    assert read_lock(root)["manifest_hash"] == manifest_hash(root)


def test_resolve_names_the_checksums_it_drops(tree, run_tt, rock_server):
    """A lock an older tt wrote carries the md5 of the source of a rock under
    checksum, which tt locks no more: a resolve drops the keys and names the
    rocks they were of, rather than that the lock is up to date or only that
    it was rewritten."""
    root = tree.root
    write_manifest(root, local_dependency(rock_server, ">=0.3.1"))

    resolved = run_tt("package", "resolve")
    assert resolved.returncode == 0, resolved.stderr

    lock = root / "app.manifest.lock"
    current = lock.read_text()
    lock.write_text(current.replace("name = 'stat'\n", "name = 'stat'\nchecksum = 'md5:0123'\n"))
    assert "checksum" in lock.read_text()

    dropped = run_tt("package", "resolve")
    assert dropped.returncode == 0, dropped.stderr
    assert dropped.stdout.splitlines() == [
        "no version moved, but the lock changed:",
        '  checksum of "stat" dropped: tt no longer locks the md5 of the source of a rock',
    ]
    assert lock.read_text() == current

    again = run_tt("package", "resolve")
    assert again.returncode == 0, again.stderr
    assert again.stdout.strip() == "the lock is up to date"


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

    result = run_tt("package", "deps", "-o", "yaml")
    assert result.returncode == 0, result.stderr

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

    result = run_tt("package", "deps", "-o", "yaml")
    assert result.returncode == 0, result.stderr

    report = yaml.safe_load(result.stdout)
    assert report["lock"] == "stale"
    assert "manifest changed" in report["lock_reason"]
    # Read-only: the lock on disk is untouched.
    assert (root / "app.manifest.lock").read_text() == before


def test_deps_notes_a_stale_lock_on_stderr(tree, run_tt):
    """The table says what its versions are worth in a log line, not in itself.

    stdout carries the table and nothing else, so a redirected table is the
    table; the warning goes to stderr, where the machine formats never put it
    because the lock state is in their document.
    """
    root = tree.root
    write_manifest(root, "\n[dependencies]\nstat = '>=0.3.1'\n")
    write_lock(root, {"stat": "0.3.1-1"}, current=False)

    result = run_tt("package", "deps")
    assert result.returncode == 0, result.stderr

    lines = result.stdout.splitlines()
    assert lines[0].split() == ["PRODUCT", "NAME", "CONSTRAINT", "VERSION", "SOURCE", "ORIGIN"]
    assert lines[1].split()[:2] == ["default", "stat"]
    assert "stale" not in result.stdout

    assert "lock is stale (manifest changed" in result.stderr
    assert "run tt package resolve" in result.stderr

    machine = run_tt("package", "deps", "-o", "json")
    assert machine.returncode == 0, machine.stderr
    assert "lock is stale" not in machine.stderr


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


# The module the wasm rocks of the index carry: a few bytes tt packs and
# installs, and nothing loads.
INDEX_MODULE = b"\x7fELF not a module: tt packs and installs it, and nothing loads it\n"

# The platforms the index serves the wasm rock for.
INDEX_ARCHES = ("linux-aarch64", "linux-x86_64", "macosx-aarch64")

# The md5 of the source the rockspec tt writes for wasm 0.1.0 names, which the
# lock does not record.
WASM_SOURCE_MD5 = "md5:f986e42dd8d430110c06e9fb2963209b"


def host_arch() -> str:
    """The LuaRocks arch of the host, or a skip on one the index has no rock for."""
    system, machine = platform.system(), platform.machine().lower()
    arch = {
        ("Linux", "x86_64"): "linux-x86_64",
        ("Linux", "amd64"): "linux-x86_64",
        ("Linux", "aarch64"): "linux-aarch64",
        ("Linux", "arm64"): "linux-aarch64",
        ("Darwin", "arm64"): "macosx-aarch64",
    }.get((system, machine))
    if arch is None:
        pytest.skip(f"the index has no rock for {system} {machine}")
    return arch


def pack_wasm_rocks(tt_cmd, work: Path, versions: tuple[str, ...] = ("0.1.0",)) -> Path:
    """Pack the wasm rock of each version for every platform of the index, as
    `tt wasm rock --from` packs a local build, and return their directory.

    A table of releases (TT_WASM_RELEASES) gives tt the versions it does not
    know; 0.1.0 is described as tt describes it."""
    module = work / "module"
    module.write_bytes(INDEX_MODULE)
    releases = work / "releases.json"
    releases.write_text(
        json.dumps(
            {
                version: {
                    "version": version,
                    "rock_revision": 1,
                    "sdk_asset": f"tarantool-wasm-{version}-sdk.tar.gz",
                    "sdk_md5": WASM_SOURCE_MD5.removeprefix("md5:"),
                    "platforms": [],
                }
                for version in versions
            },
        ),
    )
    packed = work / "packed"
    for version in versions:
        for arch in INDEX_ARCHES:
            pack = [str(tt_cmd), "wasm", "rock", "--from", str(module), "--arch", arch]
            result = subprocess.run(
                [*pack, "--version", version, "--out", str(packed)],
                cwd=work,
                capture_output=True,
                text=True,
                env={**os.environ, "TT_WASM_RELEASES": str(releases)},
            )
            assert result.returncode == 0, result.stderr
    return packed


@pytest.fixture()
def rock_index(tt_cmd, tmp_path_factory) -> tuple[Path, dict]:
    """A directory rock server with nothing but a tt-rocks-index.json and the
    files it names: the wasm rock of 0.1.0 for every platform of the index.
    Yields the directory and the digests write_rock_index returns."""
    work = tmp_path_factory.mktemp("rock-index")
    server = work / "server"
    digests = write_rock_index(server, pack_wasm_rocks(tt_cmd, work))
    assert not (server / "manifest").exists()
    return server, digests


def index_dependency(server: Path, constraint: str) -> str:
    """Render the wasm dependency declared from the index server."""
    return (
        "\n[dependencies.wasm]\n"
        "source = 'registry'\n"
        f"registry = '{server}'\n"
        f"version = '{constraint}'\n"
    )


def locked_entry(root: Path, name: str) -> dict:
    closure = read_lock(root)["lock"]["products"]["default"]["dependencies"]
    return next(entry for entry in closure if entry["name"] == name)


def installed_wasm(root: Path) -> Path:
    module = "wasm.dylib" if platform.system() == "Darwin" else "wasm.so"
    return root / ".rocks" / "lib" / "tarantool" / module


def test_resolve_pins_the_files_a_rock_index_lists(tree, run_tt, rock_index):
    """The lock of a rock from an index server pins the sha256 of the file of
    every platform and of the rockspec, and nothing of the md5 of the source,
    and a fetch installs the rock it pins."""
    host_arch()
    server, digests = rock_index
    root = tree.root
    write_manifest(root, index_dependency(server, "0.1.0"))

    result = run_tt("package", "resolve")
    assert result.returncode == 0, result.stderr

    entry = locked_entry(root, "wasm")
    assert entry["version"] == "0.1.0-1"
    assert "checksum" not in entry
    assert entry["artifacts"] == {
        arch: f"sha256:{digest}" for arch, digest in digests["0.1.0-1"].items()
    }
    assert sorted(entry["artifacts"]) == [*INDEX_ARCHES, "rockspec"]
    # One inline table per dependency.
    assert "artifacts = {linux-aarch64 = 'sha256:" in (root / "app.manifest.lock").read_text()

    fetch = run_tt("package", "fetch")
    assert fetch.returncode == 0, fetch.stderr
    assert installed_wasm(root).read_bytes() == INDEX_MODULE


def test_fetch_refuses_a_rock_that_is_not_the_locked_one(tree, run_tt, rock_index):
    """A rock the server serves with other bytes than the lock pins is not
    installed, whether the index still lists the old digest or was rewritten
    to the new one; the error names the file and how to pin it anew."""
    arch = host_arch()
    server, _ = rock_index
    root = tree.root
    write_manifest(root, index_dependency(server, "0.1.0"))
    assert run_tt("package", "resolve").returncode == 0

    rock = server / "releases" / "wasm-0.1.0-1" / f"wasm-0.1.0-1.{arch}.rock"
    rock.write_bytes(rock.read_bytes() + b"tampered")

    fetch = run_tt("package", "fetch")
    assert fetch.returncode == 1, fetch.stderr
    assert "checksum mismatch" in fetch.stderr
    assert f"wasm-0.1.0-1.{arch}.rock" in fetch.stderr
    assert "tt package resolve" in fetch.stderr
    assert not installed_wasm(root).exists()

    # The server lists the digest of the new file now: it disagrees with the
    # lock before anything is downloaded.
    index = json.loads((server / "tt-rocks-index.json").read_text())
    for artifact in index["repository"]["wasm"]["0.1.0-1"]["artifacts"]:
        if artifact["arch"] == arch:
            artifact["sha256"], artifact["size"] = sha256_of(rock), rock.stat().st_size
    (server / "tt-rocks-index.json").write_text(json.dumps(index))

    fetch = run_tt("package", "fetch")
    assert fetch.returncode == 1, fetch.stderr
    assert "checksum mismatch" in fetch.stderr
    assert f"wasm-0.1.0-1.{arch}.rock" in fetch.stderr
    assert not installed_wasm(root).exists()


def test_fetch_refuses_a_platform_the_lock_does_not_pin(tree, run_tt, rock_index):
    """A lock resolved where the file of this platform was not listed fails to
    install it, saying to resolve again; a resolve pins it, and the rock
    installs."""
    arch = host_arch()
    server, _ = rock_index
    root = tree.root
    write_manifest(root, index_dependency(server, "0.1.0"))
    assert run_tt("package", "resolve").returncode == 0

    lock = root / "app.manifest.lock"
    lock.write_text(re.sub(rf"{arch} = 'sha256:[0-9a-f]+', ", "", lock.read_text()))
    assert arch not in locked_entry(root, "wasm")["artifacts"]

    fetch = run_tt("package", "fetch")
    assert fetch.returncode == 1, fetch.stderr
    assert "artifact not pinned" in fetch.stderr
    assert "run tt package resolve" in fetch.stderr
    assert not installed_wasm(root).exists()

    assert run_tt("package", "resolve").returncode == 0
    assert arch in locked_entry(root, "wasm")["artifacts"]

    fetch = run_tt("package", "fetch")
    assert fetch.returncode == 0, fetch.stderr
    assert installed_wasm(root).read_bytes() == INDEX_MODULE


def test_a_yanked_version_installs_from_the_lock_only(tt_cmd, tree, run_tt, tmp_path_factory):
    """A version the server yanks after a lock recorded it still installs from
    that lock, and resolving the lock again keeps it; a fresh resolution - an
    update - passes it over for the newest version not yanked."""
    host_arch()
    work = tmp_path_factory.mktemp("rock-index")
    server = work / "server"
    packed = pack_wasm_rocks(tt_cmd, work, ("0.1.0", "0.2.0"))
    write_rock_index(server, packed)
    root = tree.root
    write_manifest(root, index_dependency(server, ">=0.1.0"))

    assert run_tt("package", "resolve").returncode == 0
    assert locked_entry(root, "wasm")["version"] == "0.2.0-1"

    digests = write_rock_index(server, packed, yanked=("wasm-0.2.0-1",))
    index = json.loads((server / "tt-rocks-index.json").read_text())
    assert index["repository"]["wasm"]["0.2.0-1"]["yanked"]

    fetch = run_tt("package", "fetch")
    assert fetch.returncode == 0, fetch.stderr
    assert installed_wasm(root).read_bytes() == INDEX_MODULE
    assert (root / ".rocks" / "share" / "tarantool" / "rocks" / "wasm" / "0.2.0-1").is_dir()

    resolve = run_tt("package", "resolve")
    assert resolve.returncode == 0, resolve.stderr
    assert locked_entry(root, "wasm")["version"] == "0.2.0-1"

    update = run_tt("package", "update")
    assert update.returncode == 0, update.stderr
    assert "wasm 0.2.0-1 -> 0.1.0-1" in update.stdout
    entry = locked_entry(root, "wasm")
    assert entry["version"] == "0.1.0-1"
    assert entry["artifacts"]["rockspec"] == f"sha256:{digests['0.1.0-1']['rockspec']}"

    # Nothing but the yanked version allows it: the error says why.
    write_manifest(root, index_dependency(server, ">=0.2.0"))
    update = run_tt("package", "update")
    assert update.returncode == 1, update.stderr
    assert "yanked; pin one with ==" in update.stderr


def test_resolve_names_the_digests_it_drops_and_pins(tt_cmd, tree, run_tt, tmp_path_factory):
    """A rock that leaves a rock index server for a plain one, which lists no
    digests, keeps its version and loses the pins of its files: the resolve
    says it dropped them and why, not that it pinned them anew; back on the
    index server it says it pinned them where the lock had none."""
    work = tmp_path_factory.mktemp("rock-servers")
    packed = pack_wasm_rocks(tt_cmd, work)
    # tt wasm rock writes the manifests and the index of a server beside the
    # rocks; without its index the directory is a plain server.
    assert (packed / "manifest").exists()
    (packed / "tt-rocks-index.json").unlink(missing_ok=True)
    index = work / "index"
    write_rock_index(index, packed)
    root = tree.root
    write_manifest(root, index_dependency(index, "0.1.0"))

    assert run_tt("package", "resolve").returncode == 0
    assert "artifacts" in locked_entry(root, "wasm")

    write_manifest(root, index_dependency(packed, "0.1.0"))
    dropped = run_tt("package", "resolve")
    assert dropped.returncode == 0, dropped.stderr
    assert dropped.stdout.splitlines() == [
        "no version moved, but the lock changed:",
        "  manifest hash refreshed",
        '  file digests of "wasm" dropped: its server lists none',
    ]
    assert "artifacts" not in locked_entry(root, "wasm")

    write_manifest(root, index_dependency(index, "0.1.0"))
    pinned = run_tt("package", "resolve")
    assert pinned.returncode == 0, pinned.stderr
    assert pinned.stdout.splitlines() == [
        "no version moved, but the lock changed:",
        "  manifest hash refreshed",
        '  file digests of "wasm" pinned; the lock had none',
    ]
    assert "artifacts" in locked_entry(root, "wasm")


def test_an_index_tt_writes_pins_and_installs(tt_cmd, tree, run_tt, tmp_path_factory):
    """tt package index lists the rocks of each release in an index kept
    apart from them, by URLs relative to it: a project resolves from it with
    the digest of every file pinned, and installs the rock checked against
    them. A version yanked in the index stays yanked when the index is
    written again."""
    host_arch()
    work = tmp_path_factory.mktemp("rock-index")
    packed = pack_wasm_rocks(tt_cmd, work, ("0.1.0", "0.2.0"))
    server = work / "server"
    index_path = server / "tt-rocks-index.json"

    def index(version: str) -> None:
        result = run_tt("package", "index", str(server / f"v{version}"), "--index", str(index_path))
        assert result.returncode == 0, result.stderr
        assert result.stdout == f"{index_path}\n"

    for version in ("0.1.0", "0.2.0"):
        release = server / f"v{version}"
        release.mkdir(parents=True)
        for rock in packed.glob(f"wasm-{version}-1.*.rock"):
            shutil.copy(rock, release)
        index(version)

    # The index is all the server publishes: no manifest is written for it.
    assert sorted(path.name for path in server.iterdir()) == [
        "tt-rocks-index.json",
        "v0.1.0",
        "v0.2.0",
    ]
    listed = json.loads(index_path.read_text())["repository"]["wasm"]
    assert sorted(listed) == ["0.1.0-1", "0.2.0-1"]
    assert [artifact["url"] for artifact in listed["0.2.0-1"]["artifacts"]] == [
        f"v0.2.0/wasm-0.2.0-1.{arch}.rock" for arch in INDEX_ARCHES
    ]

    root = tree.root
    write_manifest(root, index_dependency(server, ">=0.1.0"))
    resolve = run_tt("package", "resolve")
    assert resolve.returncode == 0, resolve.stderr

    entry = locked_entry(root, "wasm")
    assert entry["version"] == "0.2.0-1"
    assert entry["artifacts"] == {
        arch: "sha256:" + sha256_of(server / "v0.2.0" / f"wasm-0.2.0-1.{arch}.rock")
        for arch in INDEX_ARCHES
    }

    fetch = run_tt("package", "fetch")
    assert fetch.returncode == 0, fetch.stderr
    assert installed_wasm(root).read_bytes() == INDEX_MODULE

    whole = json.loads(index_path.read_text())
    whole["repository"]["wasm"]["0.2.0-1"]["yanked"] = True
    index_path.write_text(json.dumps(whole))
    index("0.2.0")
    assert json.loads(index_path.read_text())["repository"]["wasm"]["0.2.0-1"]["yanked"]

    update = run_tt("package", "update")
    assert update.returncode == 0, update.stderr
    assert "wasm 0.2.0-1 -> 0.1.0-1" in update.stdout
