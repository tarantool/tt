"""The rocks of the LuaRocks corpus build with tt rocks of tt 2.

test/luarocks-corpus.yaml lists real rocks, each given an app.manifest.toml in
manifests/, and rocks of this repository in fixtures/. For each rock the test:

1. fetches its pinned commit alone into a git repository, or copies its
   fixture into one;
2. drops in its manifest, whose repository it points to the repository,
   commits it and tags the commit; the lock of manifests/, if there is one,
   goes beside it, which git ignores, as it does what tt writes there;
3. packs the binary rock and the source rock with tt 3, in one pack;
4. installs with tt rocks of tt 2, LuaRocks 3.9.2, the source rock into tree
   A, and builds the bare rockspec, whose sources it fetches from the
   repository at the tag, into tree C;
5. installs the binary rock into tree B with tt rocks of tt 3: LuaRocks 3.9.2
   of tt 2 takes the platform from the first architecture of /bin/sh, which
   on a macOS of arm64 may be x86_64, and refuses a binary rock of
   macosx-aarch64 then;
6. compares lua/, lib/ and bin/ of the three trees: the same files, the same
   bytes of each but an ELF or a Mach-O file, which a build compiles anew,
   and the same executable bit in bin/;
7. requires the main module in Tarantool from tree A, its dependencies from
   .rocks of the repository, which the pack installed them into.

A tree is installed with --deps-mode=none: it has the files of the rock
alone.

The environment:

- TT_TEST_TT2: the tt 2 executable; the rocks are skipped without it.
- The tarantool on PATH, which tt 2 runs LuaRocks for and the main module
  loads in, and its headers, which tt 2 and tt 3 build against: those of
  TT_CLI_TARANTOOL_PREFIX, or include/tarantool beside bin/ of the tarantool
  on PATH, which the test gives both as TT_CLI_TARANTOOL_PREFIX. The rocks are
  skipped without them.
- TT_TEST_CORPUS_GIT_SSH: 1 makes every git of the test, and those tt 2 runs,
  fetch git@github.com: for https://github.com/ (url.<base>.insteadOf), for a
  machine that reaches GitHub over SSH only.
- TT_TEST_CORPUS_LOGS: optional, the directory the log of each rock goes to.

The rocks fetch from the network: the commits, the submodules, the
dependencies from their registries, crates and the packages of zig.
.github/workflows/rocks.yml runs them, and ci/luarocks-corpus-tools.sh sets up
what they need beyond the toolchains of the rocks catalogue check.
"""

import json
import os
import re
import shlex
import shutil
import subprocess
import time
import tomllib
from pathlib import Path

import pytest
import yaml
from rocks_platforms import DISTROS, SYSTEMS, is_platform, platforms

pytestmark = pytest.mark.luarocks_corpus

HERE = Path(__file__).resolve().parent
CORPUS_FILE = HERE.parents[1] / "luarocks-corpus.yaml"
MANIFESTS = HERE / "manifests"
FIXTURES = HERE / "fixtures"

TT2 = os.environ.get("TT_TEST_TT2", "")
GIT_SSH = os.environ.get("TT_TEST_CORPUS_GIT_SSH", "") not in ("", "0")
LOGS = Path(os.environ["TT_TEST_CORPUS_LOGS"]) if os.getenv("TT_TEST_CORPUS_LOGS") else None
# linux or darwin, and on Linux the distribution: what an os of a skip names.
PLATFORMS = platforms()

ENTRY_KEYS = (
    "url",
    "commit",
    "fixture",
    "tag",
    "main",
    "backends",
    "tools",
    "libraries",
    "submodules",
    "own_manifest",
    "skip",
    "os",
    "workaround",
)
BACKENDS = ("c", "cargo", "cmake", "go", "lua-c", "make", "shell", "zig")
_COMMIT = re.compile(r"[0-9a-f]{40}")
_TAG = re.compile(r"v\d+\.\d+\.\d+")
_MODULE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*")

# The sections of a rock and where tt rocks installs each, under .rocks/.
# share/tarantool/rocks/ is the metadata of LuaRocks, the rock's own.
SECTIONS = {"lua": ("share", "tarantool"), "lib": ("lib", "tarantool"), "bin": ("bin",)}
METADATA = "rocks"
# The magics of an ELF file and of a Mach-O file, thin or fat, of either
# byte order.
NATIVE_MAGICS = (
    b"\x7fELF",
    b"\xcf\xfa\xed\xfe",
    b"\xce\xfa\xed\xfe",
    b"\xfe\xed\xfa\xcf",
    b"\xfe\xed\xfa\xce",
    b"\xca\xfe\xba\xbe",
)
# What git ignores in the repository of a rock: what tt writes there. The
# lock is one of them, which the pack resolves anew: its manifest_hash is of
# the manifest of another repository.
EXCLUDE = "/_build/\n/.rocks/\n/.tt-rock/\n/app.manifest.lock\n"
GIT = [
    "git",
    "-c",
    "init.defaultBranch=main",
    "-c",
    "advice.detachedHead=false",
    "-c",
    "user.name=tt",
    "-c",
    "user.email=tt@localhost",
]
TAIL_LINES = 60
RESULT_MARK = "LUAROCKS-CORPUS-LOADED "
# Requires the module from the tree, the dependencies from the other tree,
# and fails unless the module is found in the tree.
LOAD = """
local args = require('json').decode([==[%s]==])
local function fail(message)
    io.stdout:write(message, '\\n')
    io.stdout:flush()
    os.exit(1)
end
local ext = jit.os == 'OSX' and 'dylib' or 'so'
local function lua_paths(root)
    return root .. '/share/tarantool/?.lua;' .. root .. '/share/tarantool/?/init.lua'
end
package.path = lua_paths(args.tree) .. ';' .. lua_paths(args.deps)
package.cpath = args.tree .. '/lib/tarantool/?.' .. ext .. ';' ..
    args.deps .. '/lib/tarantool/?.' .. ext
local file = package.searchpath(args.module, package.path) or
    package.searchpath(args.module, package.cpath)
if file == nil then
    fail(('module %%s is found nowhere'):format(args.module))
end
if file:sub(1, #args.tree + 1) ~= args.tree .. '/' then
    fail(('module %%s is found in %%s, not in the tree %%s'):format(args.module, file, args.tree))
end
local ok, err = pcall(require, args.module)
if not ok then
    fail(('require(%%q): %%s'):format(args.module, tostring(err)))
end
io.stdout:write('%s', file, '\\n')
io.stdout:flush()
os.exit(0)
"""


def load_corpus(path=CORPUS_FILE):
    """Return the rocks of the corpus file and the problems of its entries,
    of manifests/ and of fixtures/."""
    data = yaml.safe_load(path.read_text()) or {}
    problems = []
    unknown = set(data) - {"rocks"}
    if unknown:
        problems.append(f"unknown top-level keys: {', '.join(sorted(unknown))}")
    rocks = data.get("rocks") or {}
    if not isinstance(rocks, dict) or not rocks:
        return {}, [*problems, "rocks must map rock names to their entries"]
    for name, entry in rocks.items():
        problems.extend(f"{name}: {problem}" for problem in entry_problems(name, entry))
    own = {f"{name}.toml" for name in rocks} | {f"{name}.lock" for name in rocks}
    problems.extend(
        f"manifests/{file.name}: no rock of the corpus"
        for file in sorted(MANIFESTS.iterdir())
        if file.name not in own
    )
    fixtures = {entry.get("fixture") for entry in rocks.values() if isinstance(entry, dict)}
    problems.extend(
        f"fixtures/{fixture.name}: no rock of the corpus"
        for fixture in sorted(FIXTURES.iterdir())
        if fixture.name not in fixtures
    )
    return rocks, problems


def entry_problems(name, entry):
    if not isinstance(entry, dict) or not entry:
        return [f"an entry is a mapping of {', '.join(ENTRY_KEYS)}"]
    problems = []
    unknown = set(entry) - set(ENTRY_KEYS)
    if unknown:
        problems.append(f"unknown keys {', '.join(sorted(unknown))}")
    if "fixture" in entry:
        if "url" in entry or "commit" in entry:
            problems.append("a fixture has no url and no commit")
        if not (isinstance(entry["fixture"], str) and (FIXTURES / entry["fixture"]).is_dir()):
            problems.append("fixture must name a directory of fixtures/")
        if entry.get("own_manifest"):
            problems.append("the manifest of a fixture is one of manifests/")
    else:
        if not (isinstance(entry.get("url"), str) and entry["url"].startswith("https://")):
            problems.append("url must be the https URL of the git repository")
        if not (isinstance(entry.get("commit"), str) and _COMMIT.fullmatch(entry["commit"])):
            problems.append("commit must be a full sha")
    if not (isinstance(entry.get("tag"), str) and _TAG.fullmatch(entry["tag"])):
        problems.append("tag must be v and a version, as v1.2.3")
    if not (isinstance(entry.get("main"), str) and _MODULE.fullmatch(entry["main"])):
        problems.append("main must be the name of a module")
    backends = entry.get("backends")
    if not (isinstance(backends, list) and all(backend in BACKENDS for backend in backends)):
        problems.append(f"backends must be a list of {', '.join(BACKENDS)}")
    problems.extend(
        f"{key} must be a list of {what}"
        for key, what in (("tools", "programs"), ("libraries", "packages"))
        if not (
            isinstance(entry.get(key, []), list)
            and all(isinstance(item, str) and item for item in entry.get(key, []))
        )
    )
    problems.extend(
        f"{key} must be true or false"
        for key in ("submodules", "own_manifest")
        if key in entry and not isinstance(entry[key], bool)
    )
    problems.extend(
        f"{key} must say why"
        for key in ("skip", "workaround")
        if key in entry and not (isinstance(entry[key], str) and entry[key].strip())
    )
    if "os" in entry:
        if "skip" not in entry:
            problems.append("os limits a skip, and the entry has none")
        if not (
            isinstance(entry["os"], list)
            and entry["os"]
            and all(isinstance(value, str) and is_platform(value) for value in entry["os"])
        ):
            problems.append(
                f"os must be a list of {', '.join(SYSTEMS)} and of the distributions "
                f"{', '.join(DISTROS)}, each alone or with its major version, as rocky-8",
            )
    manifest = MANIFESTS / f"{name}.toml"
    if entry.get("own_manifest"):
        if manifest.exists():
            problems.append(f"own_manifest takes the manifest of the commit, not {manifest.name}")
    elif not manifest.is_file():
        problems.append(f"no manifests/{manifest.name}")
    elif isinstance(backends, list):
        declared = manifest_backends(tomllib.loads(manifest.read_text()))
        if declared != sorted(set(backends)):
            problems.append(f"backends {backends}, the manifest builds with {declared}")
    return problems


def manifest_backends(manifest):
    """The backends the builds and hooks of a manifest name, sorted."""
    blocks = [
        component.get("build") or {} for component in (manifest.get("components") or {}).values()
    ]
    blocks.extend((manifest.get("hooks") or {}).values())
    return sorted({block["backend"] for block in blocks if "backend" in block})


CORPUS, CORPUS_PROBLEMS = load_corpus()


def skip_reason(entry, names):
    """Why the entry skips the rock on the system of the names, as platforms()
    gives them, or None."""
    if "skip" not in entry:
        return None
    if "os" in entry and not set(entry["os"]) & set(names):
        return None
    return entry["skip"]


def version_of(text):
    """The first X.Y.Z of the text, as a tuple of numbers, or None."""
    found = re.search(r"(\d+)\.(\d+)\.(\d+)", text)
    return tuple(int(part) for part in found.groups()) if found else None


def needs_newer(constraint, version):
    """The clause >=X.Y.Z of a constraint of [platform] the version is older
    than, or None. The other clauses are left to tt."""
    for clause in constraint.split(","):
        found = re.fullmatch(r"\s*>=\s*(\d+\.\d+\.\d+)\s*", clause)
        if found and version < version_of(found.group(1)):
            return clause.strip()
    return None


def tarantool_prefix(tarantool):
    """The prefix of the headers of Tarantool the rocks build against:
    TT_CLI_TARANTOOL_PREFIX, or the directory of bin/ of the tarantool on
    PATH, as found or with its links resolved, whose include/tarantool has
    module.h: /usr of the packages, the root of a scope of tt binary install,
    the prefix of Homebrew."""
    given = os.environ.get("TT_CLI_TARANTOOL_PREFIX", "")
    if given:
        return Path(given)
    for path in (Path(tarantool), Path(os.path.realpath(tarantool))):
        prefix = path.parent.parent
        if (prefix / "include" / "tarantool" / "module.h").is_file():
            return prefix
    return None


@pytest.fixture(scope="module")
def tt2():
    """The tt 2 of TT_TEST_TT2, and the environment of the commands: the
    headers of the tarantool on PATH in TT_CLI_TARANTOOL_PREFIX, and the git
    configuration of TT_TEST_CORPUS_GIT_SSH."""
    if not TT2:
        pytest.skip("TT_TEST_TT2 names no tt 2, whose tt rocks is the plain LuaRocks 3.9.2")
    tarantool = shutil.which("tarantool")
    if tarantool is None:
        pytest.skip("no tarantool in PATH, which tt 2 runs LuaRocks for")
    prefix = tarantool_prefix(tarantool)
    headers = prefix / "include" / "tarantool" if prefix else None
    if headers is None or not (headers / "module.h").is_file():
        pytest.skip(f"no module.h of {tarantool}, in TT_CLI_TARANTOOL_PREFIX or beside it")

    output = subprocess.run([TT2, "version"], capture_output=True, text=True, check=False).stdout
    assert re.search(r"Tarantool CLI version 2\.", output), f"TT_TEST_TT2 is no tt 2: {output}"
    output = subprocess.run(
        [tarantool, "--version"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    version = version_of(output)
    assert version, f"no version in the output of tarantool --version: {output}"

    env = {**os.environ, "TT_CLI_TARANTOOL_PREFIX": str(prefix), "GIT_TERMINAL_PROMPT": "0"}
    if GIT_SSH:
        count = int(env.get("GIT_CONFIG_COUNT", "0"))
        env[f"GIT_CONFIG_KEY_{count}"] = "url.git@github.com:.insteadOf"
        env[f"GIT_CONFIG_VALUE_{count}"] = "https://github.com/"
        env["GIT_CONFIG_COUNT"] = str(count + 1)
    return {"tt2": TT2, "tarantool": tarantool, "version": version, "env": env}


class Rock:
    """The work of the test on one rock: runs its commands and keeps the log
    of each, in the temporary directory and in TT_TEST_CORPUS_LOGS."""

    def __init__(self, name, tmp, env):
        self.name = name
        self.env = env
        self.logs = [tmp / "commands.log"]
        if LOGS is not None:
            LOGS.mkdir(parents=True, exist_ok=True)
            self.logs.append(LOGS / f"{name}.log")
        self.write(f"=== {name}: {' '.join(sorted(PLATFORMS))}, {time.ctime()}, in {tmp}\n")

    def write(self, text):
        for log in self.logs:
            with log.open("a") as file:
                file.write(text)

    def run(self, cmd, cwd, timeout=600):
        """Run the command in cwd and return its output; fail with the tail of
        the output when it fails."""
        cmd = [str(word) for word in cmd]
        shown = shlex.join(cmd)
        start = time.monotonic()
        try:
            process = subprocess.run(
                cmd,
                cwd=cwd,
                env={**self.env, "PWD": str(cwd)},
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                errors="replace",
                timeout=timeout,
                check=False,
            )
            output, status = process.stdout, f"exit {process.returncode}"
        except subprocess.TimeoutExpired as error:
            process = None
            output = error.stdout or ""
            if isinstance(output, bytes):
                output = output.decode(errors="replace")
            status = f"killed after the timeout of {timeout} s"
        elapsed = time.monotonic() - start
        self.write(f"$ (cd {shlex.quote(str(cwd))} && {shown})  # {status}, {elapsed:.1f} s\n")
        self.write(output + "\n")
        if process is None or process.returncode != 0:
            self.fail(f"{shown} failed ({status}, {elapsed:.1f} s)", output)
        return output

    def git(self, *args, cwd, timeout=600):
        return self.run([*GIT, *args], cwd, timeout)

    def fail(self, message, output=""):
        lines = output.rstrip().splitlines()
        tail = "\n".join(lines[-TAIL_LINES:])
        where = f" (the whole log is {self.logs[-1]})"
        cut = f"the last {TAIL_LINES} lines" if len(lines) > TAIL_LINES else "the output"
        pytest.fail(f"{message}{where}; {cut}:\n{tail}" if tail else message + where, pytrace=False)


def prepare(rock, entry, repo):
    """Make the git repository of the rock at repo, its manifest committed and
    tagged, and return the manifest."""
    if "fixture" in entry:
        shutil.copytree(FIXTURES / entry["fixture"], repo)
        rock.git("init", "-q", cwd=repo)
    else:
        repo.mkdir()
        rock.git("init", "-q", cwd=repo)
        rock.git("fetch", "-q", "--depth", "1", entry["url"], entry["commit"], cwd=repo)
        rock.git("checkout", "-q", "-b", "corpus", "FETCH_HEAD", cwd=repo)
        if entry.get("submodules"):
            rock.git("submodule", "update", "-q", "--init", "--recursive", "--depth", "1", cwd=repo)

    path = repo / "app.manifest.toml"
    source = path if entry.get("own_manifest") else MANIFESTS / f"{rock.name}.toml"
    text = source.read_text()
    text, count = re.subn(
        r"^repository\s*=.*$",
        lambda _: f'repository = "git+file://{repo}"',
        text,
        flags=re.M,
    )
    if count != 1:
        rock.fail(f"the manifest of {rock.name} has {count} repository lines, not one")
    path.write_text(text)
    lock = MANIFESTS / f"{rock.name}.lock"
    if lock.exists():
        shutil.copyfile(lock, repo / "app.manifest.lock")
    with (repo / ".git" / "info" / "exclude").open("a") as exclude:
        exclude.write(EXCLUDE)

    rock.git("add", "-A", cwd=repo)
    rock.git("commit", "-q", "-m", "tt: app.manifest.toml", cwd=repo)
    rock.git("tag", entry["tag"], cwd=repo)
    return tomllib.loads(text)


def installed(tree):
    """The files tt rocks installed into .rocks of tree, by section of the
    rock: by path in the section, their bytes and whether they are
    executable."""
    sections = {}
    for section, parts in SECTIONS.items():
        root = tree.joinpath(".rocks", *parts)
        sections[section] = {
            path.relative_to(root).as_posix(): (path.read_bytes(), os.access(path, os.X_OK))
            for path in sorted(root.rglob("*"))
            if path.is_file() and (section != "lua" or path.relative_to(root).parts[0] != METADATA)
        }
    return sections


def differences(got, want, what):
    """How the files of a tree, got, differ from those of tree B, want."""
    found = []
    for section in SECTIONS:
        mine, theirs = got[section], want[section]
        paths, others = mine.keys(), theirs.keys()
        found.extend(f"{what} has {section}/{path}, B has not" for path in sorted(paths - others))
        found.extend(f"B has {section}/{path}, {what} has not" for path in sorted(others - paths))
        for path in sorted(paths & others):
            (data, executable), (other, other_executable) = mine[path], theirs[path]
            native = data.startswith(NATIVE_MAGICS) or other.startswith(NATIVE_MAGICS)
            if not native and data != other:
                found.append(f"{section}/{path}: the bytes in {what} differ from those in B")
            if section == "bin" and executable != other_executable:
                found.append(f"{section}/{path}: executable in one of {what} and B only")
    return found


def test_corpus_entries():
    """Every entry of test/luarocks-corpus.yaml says where its rock comes
    from and what it needs, has its manifest, and says why it skips."""
    assert not CORPUS_PROBLEMS, "test/luarocks-corpus.yaml:\n" + "\n".join(CORPUS_PROBLEMS)


@pytest.mark.slow
@pytest.mark.parametrize("name", sorted(CORPUS))
def test_rock(tt_cmd, tt2, tmp_path, name):
    entry = CORPUS[name]
    problems = entry_problems(name, entry)
    if problems:
        pytest.fail(f"test/luarocks-corpus.yaml: {name}: " + "; ".join(problems), pytrace=False)
    reason = skip_reason(entry, PLATFORMS)
    if reason:
        pytest.skip(reason)
    missing = [tool for tool in ("git", *entry.get("tools", [])) if shutil.which(tool) is None]
    if missing:
        pytest.skip(f"no {', '.join(missing)} in PATH, which {name} builds with")

    tmp = tmp_path.resolve()
    rock = Rock(name, tmp, tt2["env"])
    repo = tmp / "repo"
    manifest = prepare(rock, entry, repo)
    constraint = (manifest.get("platform") or {}).get("tarantool", "")
    newer = needs_newer(constraint, tt2["version"])
    if newer:
        pytest.skip(
            f"{name} needs Tarantool {newer}, and the tarantool in PATH is "
            + ".".join(map(str, tt2["version"])),
        )
    if entry.get("own_manifest"):
        declared = manifest_backends(manifest)
        assert declared == sorted(set(entry["backends"])), f"{name} builds with {declared}"

    out = tmp / "out"
    rock.run(
        [tt_cmd, "package", "pack", "--format=rock", "--format=rock-src", "--out", out],
        repo,
        timeout=900,
    )
    stem = f"{manifest['package']['name']}-{entry['tag'].removeprefix('v')}-1"
    rockspec, source = out / f"{stem}.rockspec", out / f"{stem}.src.rock"
    binary = [path for path in out.glob(f"{stem}.*.rock") if path != source]
    if not (rockspec.is_file() and source.is_file() and len(binary) == 1):
        rock.fail(f"the pack made no {stem}.rockspec, .src.rock and one binary rock: {binary}")

    trees = {"A": source, "C": rockspec}
    for tree, file in trees.items():
        (tmp / tree).mkdir()
        rock.run([tt2["tt2"], "init"], tmp / tree, timeout=60)
        rock.run([tt2["tt2"], "rocks", "install", file, "--deps-mode=none"], tmp / tree, 900)
    (tmp / "B").mkdir()
    rock.run([tt_cmd, "rocks", "install", binary[0], "--deps-mode=none"], tmp / "B", timeout=300)

    want = installed(tmp / "B")
    assert any(want.values()), f"the binary rock {binary[0].name} installed no file"
    found = []
    for tree in trees:
        found.extend(differences(installed(tmp / tree), want, tree))
    assert not found, (
        "the trees of the source rock (A) and the rockspec (C) differ from that of the "
        "binary rock (B):\n" + "\n".join(found)
    )

    (tmp / "run").mkdir()
    main = entry["main"]
    args = {"tree": str(tmp / "A" / ".rocks"), "deps": str(repo / ".rocks"), "module": main}
    output = rock.run(
        [tt2["tarantool"], "-e", LOAD % (json.dumps(args), RESULT_MARK)],
        tmp / "run",
        timeout=120,
    )
    assert RESULT_MARK in output, output
