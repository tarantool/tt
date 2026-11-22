"""What the rocks catalogue check reads: the manifest of the rock server, the
rock_manifest of an installed rock and test/rocks-catalog.yaml, the file of
its exceptions."""

import os
import re
import time
import urllib.request
from pathlib import Path

import yaml
from rocks_platforms import DISTROS, SYSTEMS, is_platform

LEVELS = ("project", "user", "system", "os")
PACKAGE_MANAGERS = ("apt", "brew", "dnf", "emerge", "pacman")
DEFAULT_REGISTRY = "https://rocks.tarantool.org"
CATALOG_FILE = Path(__file__).resolve().parents[2] / "rocks-catalog.yaml"

_TOKEN = re.compile(
    r"""\s+|--[^\n]*
    |(?P<name>[A-Za-z_][A-Za-z0-9_]*)
    |(?P<number>-?\d+(?:\.\d+)?)
    |"(?P<dq>(?:[^"\\]|\\.)*)"
    |'(?P<sq>(?:[^'\\]|\\.)*)'
    |(?P<punct>[{}\[\]=,;])""",
    re.VERBOSE,
)
_ESCAPE = re.compile(r"\\(.)")


def _tokens(text):
    pos = 0
    while pos < len(text):
        match = _TOKEN.match(text, pos)
        if match is None:
            raise ValueError(f"unexpected {text[pos : pos + 20]!r} at offset {pos}")
        pos = match.end()
        kind = match.lastgroup
        if kind is None:
            continue
        value = match.group(kind)
        if kind in ("dq", "sq"):
            yield "string", _ESCAPE.sub(r"\1", value)
        elif kind == "number":
            yield "number", float(value) if "." in value else int(value)
        else:
            yield kind, value
    yield "end", None


class _Parser:
    """The Lua the manifests of LuaRocks are written in: assignments of tables
    of strings, numbers and booleans. A table is a dict; its positional items
    are keyed 1, 2 and on."""

    def __init__(self, text):
        self.tokens = _tokens(text)
        self.kind, self.value = next(self.tokens)

    def take(self, kind=None, value=None):
        if (kind is not None and self.kind != kind) or (value is not None and self.value != value):
            raise ValueError(f"expected {value or kind}, got {self.value!r}")
        taken = self.value
        self.kind, self.value = next(self.tokens)
        return taken

    def chunk(self):
        result = {}
        while self.kind != "end":
            name = self.take("name")
            self.take("punct", "=")
            result[name] = self.expression()
        return result

    def expression(self):
        if self.kind == "punct" and self.value == "{":
            return self.table()
        if self.kind in ("string", "number"):
            return self.take()
        name = self.take("name")
        if name in ("true", "false"):
            return name == "true"
        if name == "nil":
            return None
        raise ValueError(f"unexpected name {name!r}")

    def table(self):
        self.take("punct", "{")
        result = {}
        position = 1
        while not (self.kind == "punct" and self.value == "}"):
            if self.kind == "punct" and self.value == "[":
                self.take()
                key = self.expression()
                self.take("punct", "]")
                self.take("punct", "=")
                result[key] = self.expression()
            elif self.kind == "name" and self.value not in ("true", "false", "nil"):
                key = self.take()
                self.take("punct", "=")
                result[key] = self.expression()
            else:
                result[position] = self.expression()
                position += 1
            if self.kind == "punct" and self.value in (",", ";"):
                self.take()
        self.take("punct", "}")
        return result


def parse_lua(text):
    """Return the variables a LuaRocks manifest assigns, as dicts."""
    return _Parser(text).chunk()


def registry():
    """The rock server whose manifest lists the rocks to check: a URL or a
    directory. TT_TEST_ROCKS_REGISTRY names another one than
    rocks.tarantool.org; tt installs the rocks from the servers it is
    configured with all the same (TT_REGISTRIES)."""
    return os.getenv("TT_TEST_ROCKS_REGISTRY") or DEFAULT_REGISTRY


def fetch_manifest(server, attempts=3):
    """Return the rock names and versions of the manifest of a server."""
    if "://" not in server:
        text = (Path(server) / "manifest").read_text()
    else:
        url = server.rstrip("/") + "/manifest"
        for attempt in range(attempts):
            try:
                with urllib.request.urlopen(url, timeout=60) as response:
                    text = response.read().decode()
                break
            except OSError:
                if attempt + 1 == attempts:
                    raise
                time.sleep(5 * (attempt + 1))
    repository = parse_lua(text).get("repository")
    if not isinstance(repository, dict) or not repository:
        raise ValueError(f"the manifest of {server} lists no rocks")
    return {name: sorted(versions) for name, versions in repository.items()}


def rock_modules(rock_manifest):
    """Return the modules of a rock to require: of the Lua modules its
    rock_manifest lists, those of the fewest dots. The rock crud also installs
    cartridge.roles.crud-router, which only loads with cartridge, a rock crud
    does not depend on; a rock with no top-level module, such as http, has
    http.server and its siblings.

    The C modules are taken only from a rock with no Lua module: a shared
    library may be one the Lua code of the rock loads with ffi.load, which has
    no luaopen_ function to require it by, as avro_schema_rt_c of avro-schema.
    override/ holds copies of modules, which tt searches before the others.
    """

    def walk(tree, prefix, extension):
        for key, value in tree.items():
            if not isinstance(key, str):
                continue
            if isinstance(value, dict):
                if prefix or key != "override":
                    yield from walk(value, [*prefix, key], extension)
            elif key.endswith(extension):
                parts = [*prefix, key.removesuffix(extension)]
                if extension == ".lua" and parts[-1] == "init" and len(parts) > 1:
                    parts.pop()
                yield ".".join(parts)

    tree = parse_lua(rock_manifest).get("rock_manifest", {})
    found = set(walk(tree.get("lua", {}), [], ".lua"))
    if not found:
        for extension in (".so", ".dylib"):
            found.update(walk(tree.get("lib", {}), [], extension))
    if not found:
        return []
    depth = min(module.count(".") for module in found)
    return sorted(module for module in found if module.count(".") == depth)


def load_catalog(path=CATALOG_FILE):
    """Return the exceptions file, its rocks and packages, and the problems
    of its entries: an unknown key, a skip or a pin with no reason, a value of
    the wrong type."""
    data = yaml.safe_load(path.read_text()) or {}
    problems = []
    unknown = set(data) - {"packages", "rocks"}
    if unknown:
        problems.append(f"unknown top-level keys: {', '.join(sorted(unknown))}")
    packages = data.get("packages") or {}
    if not isinstance(packages, dict) or not all(
        isinstance(names, list) and all(isinstance(name, str) for name in names)
        for names in packages.values()
    ):
        problems.append("packages must map a package manager to a list of package names")
        packages = {}
    unknown = set(packages) - set(PACKAGE_MANAGERS)
    if unknown:
        problems.append(
            f"packages: unknown package managers {', '.join(sorted(unknown))}, "
            f"not of {', '.join(PACKAGE_MANAGERS)}",
        )
    rocks = data.get("rocks") or {}
    if not isinstance(rocks, dict):
        problems.append("rocks must map rock names to their entries")
        rocks = {}
    for name, entry in rocks.items():
        problems.extend(f"{name}: {problem}" for problem in _entry_problems(entry))
    return {"packages": packages, "rocks": rocks}, problems


ENTRY_KEYS = ("skip", "os", "levels", "constraint", "require", "exclude", "reason")
# The keys a reason has to explain.
PINS = ("constraint", "require", "exclude")


def _entry_problems(entry):
    if not isinstance(entry, dict) or not entry:
        return [f"an entry is a mapping of {', '.join(ENTRY_KEYS)}"]
    problems = []
    unknown = set(entry) - set(ENTRY_KEYS)
    if unknown:
        problems.append(f"unknown keys {', '.join(sorted(unknown))}")
    if "skip" in entry and not (isinstance(entry["skip"], str) and entry["skip"].strip()):
        problems.append("skip must say why the rock is skipped")
    if "skip" not in entry:
        problems.extend(
            f"{key} limits a skip, and the entry has none"
            for key in ("os", "levels")
            if key in entry
        )
    if "os" in entry and not _is_list(entry["os"], is_platform):
        problems.append(
            f"os must be a list of {', '.join(SYSTEMS)} and of the distributions "
            f"{', '.join(DISTROS)}, each alone or with its major version, as rocky-8",
        )
    if "levels" in entry and not _is_list(entry["levels"], LEVELS.__contains__):
        problems.append(f"levels must be a list of {', '.join(LEVELS)}")
    if "constraint" in entry and not isinstance(entry["constraint"], str):
        problems.append("constraint must be a string")
    problems.extend(
        f"{key} must be a list of module names"
        for key in ("require", "exclude")
        if key in entry
        and not (
            isinstance(entry[key], list) and all(isinstance(module, str) for module in entry[key])
        )
    )
    if "require" in entry and "exclude" in entry:
        problems.append("exclude takes modules off the derived list, which require replaces")
    pins = [key for key in PINS if key in entry]
    if pins and not (isinstance(entry.get("reason"), str) and entry["reason"].strip()):
        problems.append(f"{' and '.join(pins)} {'needs' if len(pins) == 1 else 'need'} a reason")
    if "reason" in entry and not pins:
        problems.append(f"reason explains {', '.join(PINS)}, and the entry has none of them")
    return problems


def _is_list(values, valid):
    return (
        isinstance(values, list)
        and bool(values)
        and all(isinstance(value, str) and valid(value) for value in values)
    )


def skip_reason(entry, names, level):
    """Return why the entry skips the rock on the system of the names, as
    platforms() gives them, at the level, or None."""
    if "skip" not in entry:
        return None
    if "os" in entry and not set(entry["os"]) & set(names):
        return None
    if level not in entry.get("levels", LEVELS):
        return None
    return entry["skip"]
