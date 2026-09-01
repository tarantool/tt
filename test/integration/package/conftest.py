import functools
import http.server
import subprocess
import threading
from pathlib import Path

import pytest

# The rock repository the `tt rocks` suite already ships: a LuaRocks `manifest`
# next to two versions of `stat`. Serving it over loopback is what lets the
# dependency commands resolve for real without a network — the manifest
# pipeline's remote index speaks HTTP and cannot read a directory.
ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

MANIFEST_TEMPLATE = """manifest_version = '0.1'

[package]
name = '{name}'
description = '{description}'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
"""

LOCK_HEADER = """lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0'
manifest_hash = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
"""

LOCK_DEPENDENCY = """
[[lock.products.default.dependencies]]
name = '{name}'
version = '{version}'
source = 'registry'
"""


def render_lock(pins: dict[str, str]) -> str:
    """Render a lock pinning the given rocks under the default product.

    The nesting is `lock.products.<product>.dependencies`, which is what
    cli/manifest writes and parses; a flatter shape parses as an empty closure
    and would silently make every refcounting assertion vacuous.
    """
    body = "".join(
        LOCK_DEPENDENCY.format(name=name, version=version) for name, version in sorted(pins.items())
    )
    return LOCK_HEADER + body


class Tree:
    """A fixture install tree, populated the way `tt package install` would.

    These tests deliberately never run an install: the task under test is pure
    metadata handling, so building the state by hand keeps the suite offline and
    independent of the installer's own correctness.
    """

    def __init__(self, root: Path, *, system: bool = False) -> None:
        """Mirror the layout `state.ResolveLayout` resolves for a scope.

        The project scope nests everything under `.rocks/`; the system scope is
        the tree root itself, so a staging directory for it has no `.rocks/`
        level. Getting this wrong would make a system-scope test pass against
        paths tt never reads.
        """
        self.root = root
        self.rocks = root if system else root / ".rocks"
        self.share = self.rocks / "share" / "tarantool"
        self.lib = self.rocks / "lib" / "tarantool"
        self.manifests = self.rocks / "manifests"

    def install_primary(
        self,
        name: str,
        version: str,
        pins: dict[str, str] | None = None,
        deps: dict[str, str] | None = None,
    ) -> None:
        """Lay the project's own package into the install root."""
        pins = pins or {}
        _write(self.root / "app.manifest.toml", _manifest(name, deps))
        _write(self.root / "app.manifest.lock", render_lock(pins))
        _write(self.root / "VERSION", version + "\n")
        self._install_files(name, version, pins)

    def install_guest(
        self,
        name: str,
        version: str,
        pins: dict[str, str] | None = None,
        deps: dict[str, str] | None = None,
    ) -> None:
        """Lay a guest package's metadata under manifests/<name>/ and its files.

        `pins` is the lock's whole transitive closure; `deps` is what the manifest
        declares, a subset of it. The two differ on purpose: the depth split the
        tree view reports is exactly that difference.
        """
        pins = pins or {}
        meta = self.manifests / name
        _write(meta / "manifest.toml", _manifest(name, deps))
        _write(meta / "lock.toml", render_lock(pins))
        _write(meta / "VERSION", version + "\n")
        self._install_files(name, version, pins)

    def _install_files(self, name: str, version: str, pins: dict[str, str]) -> None:
        self.install_rock(name, version)
        for rock, rock_version in pins.items():
            self.install_rock(rock, rock_version)

    def install_rock(self, name: str, version: str) -> None:
        """Write the three places one rock version occupies in the tree."""
        _write(self.share / name / "init.lua", f"-- {name}\n")
        _write(self.lib / name / f"{name}.so", "binary\n")
        _write(self.share / "rocks" / name / version / "rock_manifest", f"{name} {version}\n")

    def write_tree_manifest(
        self,
        edges: dict[str, list[str]],
        versions: dict[str, str] | None = None,
    ) -> None:
        """Write the LuaRocks tree manifest recording which rock requires which.

        LuaRocks manifests are bare assignments loaded into an environment, not a
        returned table, and every key is bracket-quoted because a rock name may
        contain a hyphen. Writing it by hand keeps the test honest about the real
        on-disk format.
        """
        versions = versions or {}
        body = "commands = {}\nmodules = {}\nrepository = {}\ndependencies = {\n"
        for rock in sorted(edges):
            version = versions.get(rock, "0.0.0-1")
            body += f'   ["{rock}"] = {{\n      ["{version}"] = {{\n'
            for dep in edges[rock]:
                body += f'         {{ name = "{dep}", constraints = {{}} }},\n'
            body += "      }\n   },\n"
        body += "}\n"
        _write(self.share / "rocks" / "manifest", body)

    def rock_exists(self, name: str) -> bool:
        """True if a rock still has files in any of the three locations."""
        return any(
            path.exists()
            for path in (
                self.share / name,
                self.lib / name,
                self.share / "rocks" / name,
            )
        )

    def metadata_exists(self, name: str) -> bool:
        return (self.manifests / name).exists()


def _manifest(name: str, deps: dict[str, str] | None = None) -> str:
    body = MANIFEST_TEMPLATE.format(name=name, description=f"{name} description")
    if deps:
        body += "\n[dependencies]\n" + "".join(
            f"{dep} = '{constraint}'\n" for dep, constraint in sorted(deps.items())
        )
    return body


def _write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)


@pytest.fixture()
def tree(tmp_path: Path) -> Tree:
    return Tree(tmp_path)


@pytest.fixture()
def shared_tree(tree: Tree) -> Tree:
    """The refcounting scenario: `metrics` has one owner, `checks` has two.

    monitoring -> metrics, checks
    alerting   -> checks
    """
    tree.install_primary("my-app", "1.2.3")
    tree.install_guest("monitoring", "2.0.0", {"metrics": "1.0.0", "checks": "3.1.0"})
    tree.install_guest("alerting", "0.5.0", {"checks": "3.1.0"})
    return tree


@pytest.fixture()
def tree_fixture(tree: Tree) -> Tree:
    """Every case the dependency view distinguishes.

    metrics    -- declared by monitoring, held only by it (would go with it)
    shared-lib -- declared by monitoring, but installed in its own right
    checks     -- transitive for monitoring, declared by my-app and alerting
    luasocket  -- transitive for monitoring, held only by it
    """
    tree.install_primary("my-app", "1.2.3", {"checks": "3.1.0"}, {"checks": ">=3.0.0"})
    tree.install_guest(
        "monitoring",
        "2.0.0",
        # The lock's whole closure: two declared rocks and two that came in behind
        # them.
        {"checks": "3.1.0", "metrics": "1.0.0", "shared-lib": "1.0.0", "luasocket": "3.0.4"},
        {"metrics": ">=1.0.0", "shared-lib": ">=1.0.0"},
    )
    tree.install_guest("alerting", "0.5.0", {"checks": "3.1.0"}, {"checks": ">=3.0.0"})
    tree.install_guest("shared-lib", "1.0.0")
    # The edges LuaRocks records: metrics pulls checks, shared-lib pulls
    # luasocket. Those two are exactly monitoring's indirect rocks.
    tree.write_tree_manifest(
        {"metrics": ["checks"], "shared-lib": ["luasocket"]},
        {"metrics": "1.0.0", "shared-lib": "1.0.0"},
    )
    return tree


@pytest.fixture()
def system_staging(tmp_path: Path) -> Tree:
    """A system-layout staging tree: two guests, one dependency shared.

    Built through the same Tree helper the project-scope tests use, so the
    metadata format cannot drift between the two scopes. The caller copies it
    over a container's /usr; nothing here touches the host's.
    """
    staging = Tree(tmp_path / "staging", system=True)
    staging.install_guest("monitoring", "2.0.0", {"metrics": "1.0.0", "checks": "3.1.0"})
    staging.install_guest("alerting", "0.5.0", {"checks": "3.1.0"})
    return staging


@pytest.fixture()
def rock_server():
    """Serve the fixture rock repository over loopback and yield its base URL.

    The dependency commands re-resolve, and resolution talks to a rock server
    over HTTP. Pointing a dependency's `registry` key at this one is what keeps
    the suite offline: no flag routes the whole server list at a local
    directory, and the remote index cannot read one anyway.
    """
    handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(ROCKS_REPO))
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}/"
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


@pytest.fixture()
def run_tt(tt_cmd, tree: Tree):
    """Run `tt` in the fixture tree and return the completed process."""

    def run(
        *args: str,
        stdin: str | None = None,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess:
        """`env` replaces the environment outright rather than adding to it.

        That is what makes it useful here: the only caller uses it to take
        `tarantool` off `PATH`, and an inherited `PATH` would leave it findable.
        """
        return subprocess.run(
            [str(tt_cmd), *args],
            cwd=cwd or tree.root,
            input=stdin,
            capture_output=True,
            text=True,
            env=env,
        )

    return run
