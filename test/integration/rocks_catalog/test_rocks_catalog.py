"""Every rock of rocks.tarantool.org builds against the Tarantool of a level
and loads in it.

A level is where the Tarantool comes from; .github/workflows/rocks.yml
installs it and runs the check on a pull request labelled ci-rocks. The
environment says what to check against:

- TT_TEST_ROCKS_LEVEL: project, user, system or os. At project, the project of
  each rock has its own .tarantool, a copy of that of TT_TEST_ROCKS_TEMPLATE;
  at user and system, <root>/bin of the scope is on PATH; at os, the tarantool
  on PATH comes from the packages of the system, or from Homebrew.
- TT_TEST_ROCKS_TARANTOOL: the tarantool tt must run, at every level but
  project, where it is the one of the project.
- TT_TEST_ROCKS_TEMPLATE: at project, the directory tt binary install
  --scope project installed the Tarantool in.
- TT_TEST_ROCKS_LOGS: optional, the directory the log of each rock goes to.
- TT_TEST_ROCKS_REGISTRY: optional, the rock server whose manifest lists the
  rocks, rocks.tarantool.org by default.

Without TT_TEST_ROCKS_LEVEL the module is skipped, and nothing is fetched.

Each rock is built in a project of its own, so the rocks build in parallel
(test/ci.py run --jobs): tt package add NAME at the constraint of
test/rocks-catalog.yaml, if any, tt package build, then tt run requires the
modules the rock installed. The project is not named after the rock: a
package of the name of a rock it depends on is refused.
"""

import json
import os
import platform
import re
import shlex
import shutil
import subprocess
import time
from pathlib import Path

import pytest
from rocks_catalog_helpers import (
    LEVELS,
    fetch_manifest,
    load_catalog,
    registry,
    rock_modules,
    skip_reason,
)

pytestmark = pytest.mark.rocks_catalog

LEVEL = os.getenv("TT_TEST_ROCKS_LEVEL", "")
if not LEVEL:
    pytest.skip(
        "TT_TEST_ROCKS_LEVEL is not set: the rocks catalogue is checked by rocks.yml",
        allow_module_level=True,
    )
if LEVEL not in LEVELS:
    pytest.fail(f"TT_TEST_ROCKS_LEVEL={LEVEL}: not one of {', '.join(LEVELS)}", pytrace=False)

SYSTEM = platform.system().lower()
TEMPLATE = None
TEMPLATE_VERSION = None
EXPECTED_TARANTOOL = None
if LEVEL == "project":
    TEMPLATE = Path(os.getenv("TT_TEST_ROCKS_TEMPLATE", "")).resolve() / ".tarantool"
    if not os.getenv("TT_TEST_ROCKS_TEMPLATE") or not (TEMPLATE / "bin" / "tarantool").exists():
        pytest.fail(
            "TT_TEST_ROCKS_TEMPLATE must name a directory with .tarantool/bin/tarantool",
            pytrace=False,
        )
    # versions/tarantool/tarantool_<version>/... of the active link.
    _active = Path(os.path.realpath(TEMPLATE / "bin" / "tarantool"))
    _versions = Path(os.path.realpath(TEMPLATE / "versions" / "tarantool"))
    TEMPLATE_VERSION = _active.relative_to(_versions).parts[0].removeprefix("tarantool_")
else:
    _tarantool = os.getenv("TT_TEST_ROCKS_TARANTOOL", "")
    if not _tarantool or not os.access(_tarantool, os.X_OK):
        pytest.fail(
            f"TT_TEST_ROCKS_TARANTOOL={_tarantool!r}: the tarantool of the {LEVEL} level "
            "must be given",
            pytrace=False,
        )
    EXPECTED_TARANTOOL = os.path.realpath(_tarantool)

LOGS = Path(os.environ["TT_TEST_ROCKS_LOGS"]) if os.getenv("TT_TEST_ROCKS_LOGS") else None
MANIFEST = fetch_manifest(registry())
CATALOG, CATALOG_PROBLEMS = load_catalog()

# The warning of tt about headers of another version than the tarantool
# (cli/tarantool/prefix.go): a module built against them may not load.
HEADERS_WARNING = re.compile(r"the headers of Tarantool in .* are of version .*")
RESULT_MARK = "ROCKS-CATALOG-RESULT "
# Requires the modules one by one and prints what became of each. A module
# that required one of the others under pcall and failed left it half loaded
# ("loop or previous error"): the module is required anew for its own error.
PROBE = """
local json = require('json')
local result = {interpreter = arg[-1], modules = {}}
for _, name in ipairs(json.decode([==[%s]==])) do
    local ok, err = pcall(require, name)
    if not ok and tostring(err):find('loop or previous error', 1, true) then
        package.loaded[name] = nil
        ok, err = pcall(require, name)
    end
    table.insert(result.modules, {name = name, ok = ok, error = ok and '' or tostring(err)})
end
io.stdout:write('\\n%s' .. json.encode(result) .. '\\n')
io.stdout:flush()
os.exit(0)
"""
TAIL_LINES = 60
ERROR_LINES = 6


class Probe:
    """The project of one rock: runs tt in it and keeps the log of each
    command."""

    def __init__(self, tt_cmd, project, rock):
        self.tt_cmd = tt_cmd
        self.project = project
        # The check is of what tt finds by itself at the level.
        self.env = dict(os.environ)
        for name in ("TT_CLI_TARANTOOL_PREFIX", "TT_USE_SYSTEM_TARANTOOL"):
            self.env.pop(name, None)
        self.log = None
        if LOGS is not None:
            LOGS.mkdir(parents=True, exist_ok=True)
            self.log = LOGS / f"{rock}.log"
            with self.log.open("a") as log:
                log.write(f"=== {rock}: level {LEVEL}, {SYSTEM}, {time.ctime()}, in {project}\n")

    def tt(self, *args, timeout=600, shown=None):
        """Run tt in the project and return its output; fail with the tail of
        the output when it fails. shown is the command as the log and the
        failure show it, the command itself by default."""
        cmd = [str(self.tt_cmd), *args]
        shown = shown or shlex.join(["tt", *args])
        start = time.monotonic()
        try:
            process = subprocess.run(
                cmd,
                cwd=self.project,
                env=self.env,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                errors="replace",
                timeout=timeout,
            )
            output, status = process.stdout, f"exit {process.returncode}"
        except subprocess.TimeoutExpired as error:
            process = None
            output = error.stdout or ""
            if isinstance(output, bytes):
                output = output.decode(errors="replace")
            status = f"killed after the timeout of {timeout} s"
        elapsed = time.monotonic() - start
        if self.log is not None:
            with self.log.open("a") as log:
                log.write(f"$ {shown}  # {status}, {elapsed:.1f} s\n{output}\n")
        if process is None or process.returncode != 0:
            self.fail(f"{shown} failed ({status}, {elapsed:.1f} s)", output)
        return output

    def fail(self, message, output=""):
        lines = output.rstrip().splitlines()
        tail = "\n".join(lines[-TAIL_LINES:])
        where = f" (in {self.project}; the whole log is {self.log})" if self.log else ""
        cut = f"the last {TAIL_LINES} lines" if len(lines) > TAIL_LINES else "the output"
        pytest.fail(f"{message}{where}; {cut}:\n{tail}" if tail else message + where, pytrace=False)


def copy_template(project):
    """Give the project the Tarantool of the template.

    One tt binary install for the template rather than one per rock: the
    rocks check the headers lookup of a project, not the install. The files of
    .tarantool are linked rather than copied where the file system allows,
    which saves a copy of the tarantool binary per rock: nothing writes to
    them. The links bin/tarantool and include/tarantool are absolute, so the
    copies still point into the template: tt binary switch points them to the
    copy of the project.
    """
    target = project / ".tarantool"

    def link_or_copy(source, destination):
        try:
            os.link(source, destination)
        except OSError:
            shutil.copy2(source, destination)

    shutil.copytree(TEMPLATE, target, symlinks=True, copy_function=link_or_copy)


@pytest.mark.parametrize("rock", sorted(MANIFEST))
def test_rock(tt_cmd, tmp_path, rock):
    entry = CATALOG["rocks"].get(rock) or {}
    reason = skip_reason(entry, SYSTEM, LEVEL)
    if reason:
        pytest.skip(reason)

    project = tmp_path / "probe"
    project.mkdir()
    probe = Probe(tt_cmd, project, rock)
    probe.tt("new", timeout=60)
    if LEVEL == "project":
        copy_template(project)
        probe.tt("binary", "switch", "tarantool", TEMPLATE_VERSION, timeout=60)
        expected = os.path.realpath(project / ".tarantool" / "bin" / "tarantool")
        own = os.path.realpath(project / ".tarantool")
        if not expected.startswith(own + os.sep):
            probe.fail(f"the tarantool of the project is {expected}, outside of {own}")
    else:
        expected = EXPECTED_TARANTOOL

    constraint = [entry["constraint"]] if "constraint" in entry else []
    probe.tt("package", "add", rock, *constraint, timeout=300)
    output = probe.tt("package", "build", timeout=900)
    warning = HEADERS_WARNING.search(output)
    if warning:
        probe.fail(f"the build took the headers of another Tarantool: {warning.group(0)}", output)

    installed = sorted((project / ".rocks/share/tarantool/rocks" / rock).glob("*/rock_manifest"))
    if len(installed) != 1:
        probe.fail(f"tt package build installed {len(installed)} versions of {rock}", output)
    modules = entry.get("require")
    if modules is None:
        excluded = set(entry.get("exclude", []))
        modules = [m for m in rock_modules(installed[0].read_text()) if m not in excluded]

    output = probe.tt(
        "run",
        "-e",
        PROBE % (json.dumps(modules), RESULT_MARK),
        timeout=120,
        shown=f"tt run -e <the probe, requiring {', '.join(modules) or 'nothing'}>",
    )
    lines = [line for line in output.splitlines() if line.startswith(RESULT_MARK)]
    if not lines:
        probe.fail("tt run printed no result of the requires", output)
    result = json.loads(lines[-1].removeprefix(RESULT_MARK))
    interpreter = os.path.realpath(result["interpreter"])
    if interpreter != expected:
        probe.fail(f"tt run ran {interpreter}, not {expected}", output)
    # The error of a module not found lists every path searched.
    failed = [
        f"require('{item['name']}'): " + "\n".join(item["error"].splitlines()[:ERROR_LINES])
        for item in result["modules"]
        if not item["ok"]
    ]
    if failed:
        probe.fail(
            f"{len(failed)} of {len(modules)} modules of {rock} {installed[0].parent.name} "
            "do not load:\n" + "\n".join(failed),
        )


def test_catalog_entries():
    """Every entry of test/rocks-catalog.yaml names a rock of the manifest and
    says why it is there."""
    problems = list(CATALOG_PROBLEMS) + [
        f"{name}: the manifest of {registry()} has no such rock any more"
        for name in sorted(set(CATALOG["rocks"]) - set(MANIFEST))
    ]
    assert not problems, "test/rocks-catalog.yaml:\n" + "\n".join(problems)
