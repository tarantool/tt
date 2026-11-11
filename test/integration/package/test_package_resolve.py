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
import platform
import re
import subprocess
import tomllib
import zipfile
from pathlib import Path

import pytest
import yaml

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

# The md5 of the source the rockspec tt writes for wasm 0.1.0 names: the
# checksum the lock records next to the artifacts.
WASM_SOURCE_MD5 = "md5:f986e42dd8d430110c06e9fb2963209b"

# The file name of a rock: <name>-<version>-<revision>.<arch>.rock.
ROCK_FILE = re.compile(r"(?P<name>.+?)-(?P<version>[^-]+-\d+)\.(?P<arch>[^.]+)\.rock")


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


def sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def pack_wasm_rocks(tt_cmd, work: Path) -> Path:
    """Pack the wasm rock of 0.1.0 for every platform of the index, as
    `tt wasm rock --from` packs a local build, and return their directory."""
    module = work / "module"
    module.write_bytes(INDEX_MODULE)
    packed = work / "packed"
    for arch in INDEX_ARCHES:
        result = subprocess.run(
            [str(tt_cmd), "wasm", "rock", "--from", str(module), "--arch", arch, "--out", packed],
            cwd=work,
            capture_output=True,
            text=True,
        )
        assert result.returncode == 0, result.stderr
    return packed


def write_rock_index(server: Path, packed: Path, yanked: tuple[str, ...] = ()) -> dict:
    """Lay the rocks in packed out as release pages do, one directory per
    version with its rockspec, and write the tt-rocks-index.json that names
    them by relative URLs. Returns the sha256 of every file by version and arch,
    the rockspec under "rockspec", as the lock is to pin them."""
    repository: dict = {}
    digests: dict = {}
    for rock in sorted(packed.glob("*.rock")):
        match = ROCK_FILE.fullmatch(rock.name)
        name, version, arch = match["name"], match["version"], match["arch"]
        release = Path("releases") / f"{name}-{version}"
        (server / release).mkdir(parents=True, exist_ok=True)
        (server / release / rock.name).write_bytes(rock.read_bytes())

        entry = repository.setdefault(name, {}).setdefault(version, {"artifacts": []})
        entry["artifacts"].append(
            {
                "arch": arch,
                "sha256": sha256_of(rock),
                "size": rock.stat().st_size,
                "url": f"{release.as_posix()}/{rock.name}",
            },
        )
        digests.setdefault(version, {})[arch] = sha256_of(rock)

        spec = f"{name}-{version}.rockspec"
        if "rockspec" not in entry:
            with zipfile.ZipFile(rock) as archive:
                (server / release / spec).write_bytes(archive.read(spec))
            entry["rockspec"] = {
                "sha256": sha256_of(server / release / spec),
                "url": f"{release.as_posix()}/{spec}",
            }
            digests[version]["rockspec"] = entry["rockspec"]["sha256"]
        if f"{name}-{version}" in yanked:
            entry["yanked"] = True

    (server / "tt-rocks-index.json").write_text(
        json.dumps({"format": 1, "repository": repository}, indent=2, sort_keys=True),
    )
    return digests


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
    every platform and of the rockspec, next to the md5 of the source, and a
    fetch installs the rock it pins."""
    host_arch()
    server, digests = rock_index
    root = tree.root
    write_manifest(root, index_dependency(server, "0.1.0"))

    result = run_tt("package", "resolve")
    assert result.returncode == 0, result.stderr

    entry = locked_entry(root, "wasm")
    assert entry["version"] == "0.1.0-1"
    assert entry["checksum"] == WASM_SOURCE_MD5
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
