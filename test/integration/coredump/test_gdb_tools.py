"""The gdb extensions of tarantool-gdb-tools that tt vendors, at work on a core.

tt coredump inspect --experimental loads the copies of tt into gdb. These
tests open a kernel core of a Lua script that crashed, packed by tt, the way
a user does, and check what gdb has then: the commands and the parameters
the extensions register, against their manifest, and what lj-bt, info
tt-fibers, tt-mp and the tuple printer show of the script. A last test
compares the copies with the commit of tarantool-gdb-tools they are of.

The core is one of the tarantool of PATH, and of TT_TEST_PIE_TARANTOOL as
well when it names a tarantool built as a PIE, as a build of a user is,
where the official packages are static executables: gdb relocates a PIE, and
an extension that keeps an address from before breaks on it. Either needs
its debugging information, which the static deb of Tarantool has.
ci/build-tarantool-pie.sh builds such a PIE of a release from its source,
RelWithDebInfo, and prints its path, which the coredump-pie job of Full CI
sets the variable to, on the newest release the manifest of the extensions
covers, so that every test of the PIE runs there.
"""

import dataclasses
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

import pytest
from coredump_helpers import (
    GDB_TOOLS_DIR,
    GDB_TOOLS_VENDOR,
    checked_on,
    gdb_env,
    gdb_tools_extensions,
    has_debug_info,
    is_pie,
    kernel_core,
    offline_env,
    skip_coredump_cond,
    skip_coredump_reason,
)

from utils import run_command_and_get_output

# The tarantool built as a PIE the tests take a core of as well, and the
# variable that makes a tarantool without debugging information a failure
# rather than a skip.
PIE_TARANTOOL_ENV = "TT_TEST_PIE_TARANTOOL"
REQUIRE_DEBUG_INFO_ENV = "TT_TEST_REQUIRE_DEBUG_INFO"

# The script whose core the tests read. It starts two fibers with names of
# their own, which sleep, makes a tuple and a MsgPack string, writes what
# the tests compare with to oracle.json, the addresses of the two among it,
# and crashes in the FFI three Lua calls deep. A call is made as a statement
# or with its result kept, not as a tail call, which leaves no frame of the
# caller. The comments name the lines the tests expect in lj-bt.
SCENARIO_NAME = "gdb_tools.lua"
SCENARIO = """\
local ffi = require('ffi')
local fiber = require('fiber')
local json = require('json')
local msgpack = require('msgpack')

local sleepers = {}
for _, name in ipairs({'tt-test-sleeper-1', 'tt-test-sleeper-2'}) do
    local sleeper = fiber.new(function() fiber.sleep(1000) end)
    sleeper:name(name)
    table.insert(sleepers, sleeper)
end
fiber.yield()

local tuple = box.tuple.new({1, 'abc', {x = 2}})
local mp = msgpack.encode({42, -7, 1.5, 'tt-test', true, box.NULL, {key = 'value'}})

local function address(ptr)
    return ('0x%x'):format(tonumber(ffi.cast('uintptr_t', ptr)))
end

local fibers = {}
for _, sleeper in ipairs(sleepers) do
    table.insert(fibers, {id = sleeper:id(), name = sleeper:name()})
end
local oracle = assert(io.open('oracle.json', 'w'))
oracle:write(json.encode({
    tuple = address(ffi.cast('void *', tuple)),
    mp = address(ffi.cast('const char *', mp)),
    sleepers = fibers,
    running = {id = fiber.self():id(), name = fiber.self():name()},
}))
oracle:close()

local function crash() -- tt-test: crash
    ffi.cast('char *', 0)[0] = 42 -- tt-test: store
end

local function middle() -- tt-test: middle
    crash() -- tt-test: call crash
    return true
end

local function outer() -- tt-test: outer
    local ok = middle() -- tt-test: call middle
    return ok
end

outer() -- tt-test: call outer
print(tuple, mp, sleepers)
"""

# How gdb prints the MsgPack and the data of the tuple of SCENARIO, and the
# data of the value of print of a tuple, the last of its fields.
MP_PRINTED = '{42U, -7, 1.5, "tt-test", true, null, {["key"] = "value"}}'
TUPLE_DATA_PRINTED = '{1U, "abc", {["x"] = 2U}}'
TUPLE_DATA = re.compile(r"^\$\d+ = \{.*, data = (\{.*\})\}$", re.MULTILINE)

# What gdb or an extension prints when the Python of an extension fails, and
# when a command is not there.
PYTHON_ERRORS = re.compile(r"Python Exception|Error occurred in Python|Traceback|Undefined command")

# The prompts gdb prints, reading the commands of the tests from a pipe, at
# the start of a line of its output.
PROMPTS = re.compile(r"^(?:\(gdb\) )+", re.MULTILINE)

# The line the tests have gdb print before the output of each command.
MARK = re.compile(r"^@@tt-test (.+?)@@\n", re.MULTILINE)


def line_of(mark):
    """The number of the line of SCENARIO with the comment "tt-test: mark"."""
    lines = SCENARIO.splitlines()
    found = [i for i, line in enumerate(lines, 1) if line.endswith(f"-- tt-test: {mark}")]
    assert len(found) == 1, mark
    return found[0]


@dataclasses.dataclass
class Scenario:
    """A core of SCENARIO, packed and unpacked by tt."""

    # The tarantool that ran SCENARIO.
    tarantool: Path
    # The archive of tt coredump pack and the directory tt coredump unpack
    # makes of it.
    archive: Path
    unpacked: Path
    # What SCENARIO wrote to oracle.json.
    oracle: dict
    # The first line of the version file of the archive.
    version: str


def tarantool_under_test(flavor):
    """The tarantool the cores of flavor are of: that of PATH, or the PIE of
    TT_TEST_PIE_TARANTOOL. A test is skipped for one with no debugging
    information gdb finds, which the extensions need, or fails with
    TT_TEST_REQUIRE_DEBUG_INFO, as in the CI jobs of the gdb of each runner
    and of the PIE: a gdb that cannot read the debugging information of a
    tarantool would skip every test there."""
    if flavor == "path":
        tarantool = shutil.which("tarantool")
        assert tarantool is not None, "no tarantool on PATH"
    else:
        tarantool = os.getenv(PIE_TARANTOOL_ENV)
        if not tarantool:
            pytest.skip(
                f"{PIE_TARANTOOL_ENV} names no tarantool built as a PIE, "
                "which ci/build-tarantool-pie.sh builds",
            )
        assert os.access(tarantool, os.X_OK), f"{PIE_TARANTOOL_ENV}={tarantool} is no executable"
        assert is_pie(tarantool), f"{PIE_TARANTOOL_ENV}={tarantool} is not a PIE"
    if not has_debug_info(tarantool):
        reason = f"gdb finds no debugging information of {tarantool}, which the extensions need"
        if os.getenv(REQUIRE_DEBUG_INFO_ENV):
            pytest.fail(f"{reason}, and {REQUIRE_DEBUG_INFO_ENV} is set")
        pytest.skip(reason)
    return Path(tarantool)


@pytest.fixture(scope="session", params=["path", "pie"])
def scenario(request, tt_cmd, tmp_path_factory) -> Scenario:
    """A kernel core of a tarantool that ran SCENARIO, packed by tt coredump
    pack and unpacked by tt coredump unpack: of the tarantool of PATH and of
    TT_TEST_PIE_TARANTOOL. The core itself is removed once it is packed: the
    archive has it, and the unpacked one. A gcore would not do: gdb before
    18.1 writes the stacks of the threads but the main one as zeros on glibc
    2.42 and later, and the stacks of the fibers are what the extensions
    read."""
    if skip_coredump_cond:
        pytest.skip(skip_coredump_reason)
    if sys.platform != "linux" or shutil.which("gdb") is None:
        pytest.skip("needs gdb on Linux")
    tarantool = tarantool_under_test(request.param)

    work = tmp_path_factory.mktemp(f"gdb_tools_{request.param}")
    (work / SCENARIO_NAME).write_text(SCENARIO)
    core, output = kernel_core(work, [tarantool, SCENARIO_NAME])
    oracle = json.loads((work / "oracle.json").read_text())
    print(output)

    out = work / "out"
    out.mkdir()
    rc, output = run_command_and_get_output(
        [tt_cmd, "coredump", "pack", "-e", tarantool, "-d", out, core],
        cwd=work,
    )
    assert rc == 0, output
    (archive,) = out.glob("*.tar.gz")
    core.unlink()

    rc, output = run_command_and_get_output([tt_cmd, "coredump", "unpack", archive], cwd=work)
    assert rc == 0, output
    unpacked = work / archive.name.split(".")[0]
    version = (unpacked / "version").read_text().splitlines()[0]
    return Scenario(tarantool, archive, unpacked, oracle, version)


def gdb_script(commands, help_file=None):
    """The standard input of gdb that runs commands, (name, command) each,
    with the line of MARK of the name before its output, and writes the
    output of help all to help_file first, if given."""
    lines = ["set pagination off", "set width 0", "set confirm off"]
    if help_file is not None:
        write = f"open({str(help_file)!r}, 'w').write(gdb.execute('help all', False, True))"
        lines.append("python " + write)
    for name, command in commands:
        lines += [f"echo @@tt-test {name}@@\\n", command]
    lines += ["echo @@tt-test end@@\\n", "quit"]
    return "".join(line + "\n" for line in lines)


@dataclasses.dataclass
class Session:
    """What gdb printed in a session of tt coredump inspect --experimental."""

    stdout: str
    stderr: str
    # The output of each command of the session, by its name.
    outputs: dict
    # The output of help all, if the session wrote it.
    help: str

    def output(self, name):
        """The output of the command name."""
        assert name in self.outputs, self.stdout + self.stderr
        return self.outputs[name]

    def failure(self, name):
        """What an assertion on the output of the command name says."""
        return f"output:\n{self.output(name)}\nstderr of gdb:\n{self.stderr}"


def inspect_experimental(tt_cmd, cwd, scenario, commands, with_help=False) -> Session:
    """The session of tt coredump inspect --experimental of the archive of
    scenario that runs commands, (name, command) each, given on the standard
    input of tt, as a user types them, with no GitHub (offline_env)."""
    help_file = cwd / "help-all.txt" if with_help else None
    process = subprocess.run(
        [tt_cmd, "coredump", "inspect", "--experimental", scenario.archive],
        cwd=cwd,
        input=gdb_script(commands, help_file),
        capture_output=True,
        text=True,
        env=offline_env(cwd),
        timeout=300,
    )
    print(process.stdout, process.stderr)
    assert process.returncode == 0, process.stdout + process.stderr
    parts = MARK.split(PROMPTS.sub("", process.stdout))
    outputs = dict(zip(parts[1::2], parts[2::2]))
    help_text = help_file.read_text() if help_file is not None else ""
    return Session(process.stdout, process.stderr, outputs, help_text)


def gdb_names(help_text):
    """The commands and the parameters gdb has, by their full names, as help
    all lists them, the way tests/manifest/probe.py of tarantool-gdb-tools
    reads them: a line "name, alias... -- doc" is a command, a parameter is a
    "set X" with a "show X", and "function X" is a convenience function."""
    names = set()
    for line in help_text.splitlines():
        if " -- " not in line:
            continue
        name = " ".join(line.split(" -- ", 1)[0].split(",")[0].split())
        if name:
            names.add(name)
    parameters = {
        name[4:] for name in names if name.startswith("set ") and "show " + name[4:] in names
    }
    commands = {
        name
        for name in names
        if not name.startswith("function ")
        and not (name.startswith(("set ", "show ")) and name.split(" ", 1)[1] in parameters)
    }
    return commands, parameters


def checked_here(entry, scenario):
    """Whether the manifest says the extension of entry is checked on the
    Tarantool of the core of scenario, by its minor, as inspect tells it, on
    the machine of this host, where the core is made (see checked_on)."""
    return checked_on(entry, scenario.version, platform.machine())


def require_checked(scenario, name):
    """Skip a test of the extension name on a core it is not checked on."""
    (entry,) = [entry for entry in gdb_tools_extensions() if entry["file"] == name]
    if not checked_here(entry, scenario):
        pytest.skip(f"{name} is not checked on {scenario.version} on {platform.machine()}")


@pytest.fixture(scope="session")
def loaded(tt_cmd, scenario, tmp_path_factory) -> Session:
    """The session of inspect --experimental of the core of scenario that
    writes the output of help all and runs nothing else: what gdb has with
    the extensions, and what they print as they load."""
    cwd = tmp_path_factory.mktemp("gdb_tools_loaded")
    return inspect_experimental(tt_cmd, cwd, scenario, [], with_help=True)


@pytest.fixture(scope="session")
def baseline(scenario, tmp_path_factory) -> str:
    """The output of help all of gdb on the core of scenario without the
    extensions: the gdb.sh of the archive, the one of inspect, run on the
    unpacked archive as inspect runs it, given /dev/null for the extensions
    to source, so that gdb runs with the same files and settings, no init
    file of the user among them."""
    cwd = tmp_path_factory.mktemp("gdb_tools_baseline")
    help_file = cwd / "help-all.txt"
    gdb_sh = scenario.unpacked / "gdb.sh"
    process = subprocess.run(
        ["sh", gdb_sh, "-d", scenario.unpacked, "-x", "/dev/null"],
        cwd=cwd,
        input=gdb_script([], help_file),
        capture_output=True,
        text=True,
        env=gdb_env(home=cwd / "home"),
        timeout=300,
    )
    assert process.returncode == 0, process.stdout + process.stderr
    return help_file.read_text()


@pytest.fixture(scope="session")
def shown(tt_cmd, scenario, tmp_path_factory) -> Session:
    """The session of inspect --experimental of the core of scenario that
    runs the commands the tests compare with what SCENARIO did: lj-bt, info
    tt-fibers, tt-mp of the MsgPack and print of the tuple, at the addresses
    SCENARIO wrote. The tuple is cast to struct tuple: the printer takes no
    box_tuple_t (tarantool-gdb-tools#14)."""
    cwd = tmp_path_factory.mktemp("gdb_tools_shown")
    commands = [
        ("lj-bt", "lj-bt"),
        ("info tt-fibers", "info tt-fibers"),
        ("tt-mp", f"tt-mp (char *){scenario.oracle['mp']}"),
        ("tuple", f"print *(struct tuple *){scenario.oracle['tuple']}"),
    ]
    return inspect_experimental(tt_cmd, cwd, scenario, commands)


def test_gdb_tools_manifest(scenario, loaded, baseline):
    """The extensions inspect --experimental loads register the commands and
    the parameters their manifest lists, and no other, read back from gdb
    with help all against gdb on the same core without them, as
    tests/manifest/check.py of tarantool-gdb-tools does with each file on its
    own: every one of a file the manifest says is checked on the version of
    the core and the machine, by the minor, as inspect tells them, and none
    that no file lists. inspect loads every file all the same, with a
    warning, so that one the core is out of the range of may register its
    own or not. They load with no error of Python."""
    assert not PYTHON_ERRORS.search(loaded.stdout + loaded.stderr), loaded.stdout + loaded.stderr
    commands, parameters = gdb_names(loaded.help)
    base_commands, base_parameters = gdb_names(baseline)
    assert "backtrace" in base_commands, baseline
    registered = {"commands": commands - base_commands, "parameters": parameters - base_parameters}

    errors = []
    for key, added in registered.items():
        listed = set()
        for entry in gdb_tools_extensions():
            listed |= set(entry.get(key, []))
            missing = set(entry.get(key, [])) - added
            if checked_here(entry, scenario) and missing:
                errors.append(f"{entry['file']} does not register {key} of the manifest: {missing}")
        if added - listed:
            errors.append(f"the extensions register {key} the manifest has not: {added - listed}")
    assert not errors, "\n".join(errors)


def test_gdb_tools_lj_bt(scenario, shown):
    """lj-bt shows the Lua frames of the functions of SCENARIO in the order of
    the calls, each with the line it is at and the line the function is
    defined at, the main chunk last: crash, which stores through a NULL
    pointer, middle and outer. The C frames around them, and their number,
    are those of the version, and are not compared.

    The FFI store runs in the builtin ffi.meta.__newindex, which lj-bt folds
    into the C frame of its C function, lj_cf_ffi_meta___newindex. Where it
    cannot tell the frame is that of the function, it lists the builtin
    among the Lua frames, on top, as it does on a build of Tarantool 3.7.0
    Enterprise, whose debugging information names the function with the
    parameters of C++, lj_cf_ffi_meta___newindex(lua_State*): such a frame is
    left out of the comparison.

    The header names the Lua state lj-bt found, never "L=0x0 (none)", which
    it prints where gdb reads no tarantool_L, a thread-local variable since
    Tarantool 3.7: inspect warns of such a gdb and host, and the Lua frames
    shown then are those of the VM entries alone."""
    require_checked(scenario, "luajit_backtrace_gdb.py")
    output = shown.output("lj-bt")
    header = re.search(r"^lj-bt: L=(0x[0-9a-f]+)(.*)$", output, re.MULTILINE)
    assert header, shown.failure("lj-bt")
    assert int(header.group(1), 16) != 0, shown.failure("lj-bt")
    assert "(none)" not in header.group(2), shown.failure("lj-bt")
    frames = re.findall(r"^#\d+\.\d+\s+\[lua\] (.+?)(?:\s{2,}<.*)?$", output, re.MULTILINE)
    while frames and re.fullmatch(r"builtin [\w.]+", frames[0]):
        frames.pop(0)

    def in_function(at, defined):
        return f"{SCENARIO_NAME}:{line_of(at)} in function <{SCENARIO_NAME}:{line_of(defined)}>"

    expected = [
        in_function("store", "crash"),
        in_function("call crash", "middle"),
        in_function("call middle", "outer"),
        f"{SCENARIO_NAME}:{line_of('call outer')} in main chunk",
    ]
    assert frames == expected, shown.failure("lj-bt")


def test_gdb_tools_fibers(scenario, shown):
    """info tt-fibers lists the fibers SCENARIO started, by the IDs and the
    names it gave them, the running one, which runs SCENARIO, marked as the
    current one, and the scheduler. The addresses and the other fibers are
    those of the version, and are not compared."""
    require_checked(scenario, "tarantool_gdb.py")
    output = shown.output("info tt-fibers")
    failure = shown.failure("info tt-fibers")
    rows = re.findall(r'^\s*(\*)?\s*(\d+)\s+TtFiber\s+"([^"]*)"', output, re.MULTILINE)
    fibers = {int(fid): (name, current == "*") for current, fid, name in rows}
    assert len(fibers) == len(rows), failure
    for sleeper in scenario.oracle["sleepers"]:
        assert fibers.get(sleeper["id"]) == (sleeper["name"], False), failure
    running = scenario.oracle["running"]
    assert fibers.get(running["id"]) == (running["name"], True), failure
    assert [fid for fid, (_, current) in fibers.items() if current] == [running["id"]], failure
    assert ("sched", False) in fibers.values(), failure


def test_gdb_tools_msgpack(scenario, shown):
    """tt-mp decodes the MsgPack SCENARIO encoded, at the address it wrote:
    unsigned and negative integers, a double, a string, a boolean, a nil,
    which it prints as null, and a map."""
    require_checked(scenario, "tarantool_gdb.py")
    printed = re.findall(r"^\$\d+ = (.*)$", shown.output("tt-mp"), re.MULTILINE)
    assert printed == [MP_PRINTED], shown.failure("tt-mp")


def test_gdb_tools_tuple(scenario, shown):
    """The printer of struct tuple shows the data of the tuple SCENARIO made,
    at the address it wrote, decoded as MsgPack."""
    require_checked(scenario, "tarantool_gdb.py")
    printed = TUPLE_DATA.findall(shown.output("tuple"))
    assert printed == [TUPLE_DATA_PRINTED], shown.failure("tuple")


# What the printer of struct tuple says of a value of another type, as it
# takes a box_tuple_t for one (tarantool-gdb-tools#14).
NOT_A_TUPLE = "expression doesn't evaluate to tuple"


class BoxTupleRefused(Exception):
    """The printer of struct tuple refused a box_tuple_t, as
    tarantool-gdb-tools#14 says it does."""


@pytest.mark.xfail(
    strict=True,
    raises=BoxTupleRefused,
    reason="tarantool-gdb-tools#14: the tuple printer takes no box_tuple_t, "
    "which is what a C module has; it says the expression does not evaluate to a tuple",
)
def test_gdb_tools_box_tuple_t(tt_cmd, scenario, tmp_path):
    """The printer of struct tuple shows a box_tuple_t, the typedef of the
    API of modules, as it shows a struct tuple. It fails while the vendored
    copy has tarantool-gdb-tools#14, which test_gdb_tools_tuple works around
    with a cast to struct tuple; once a pin fixes it, this test passes and
    says so. The failure expected is the one of the printer, NOT_A_TUPLE,
    alone: any other, of inspect or of gdb, is a failure of the test. gdb
    prints the error of Python on its standard error, which the print is the
    only command of the session to write to."""
    require_checked(scenario, "tarantool_gdb.py")
    commands = [("tuple", f"print *(box_tuple_t *){scenario.oracle['tuple']}")]
    session = inspect_experimental(tt_cmd, tmp_path, scenario, commands)
    if NOT_A_TUPLE in session.output("tuple") + session.stderr:
        raise BoxTupleRefused(session.failure("tuple"))
    printed = TUPLE_DATA.findall(session.output("tuple"))
    assert printed == [TUPLE_DATA_PRINTED], session.failure("tuple")


def fetch(url, attempts=3):
    """The content at url; a failure to reach it is retried, and a status of
    HTTP is not."""

    def read():
        with urllib.request.urlopen(url, timeout=30) as response:
            return response.read()

    for _ in range(attempts - 1):
        try:
            return read()
        except urllib.error.HTTPError:
            raise
        except (urllib.error.URLError, TimeoutError):
            time.sleep(2)
    return read()


# The variable that runs test_gdb_tools_vendored_from_upstream, which needs
# GitHub and checks what is the same on every runner: one job of CI sets it.
UPSTREAM_ENV = "TT_TEST_GDB_TOOLS_UPSTREAM"


@pytest.mark.slow
@pytest.mark.skipif(
    not os.getenv(UPSTREAM_ENV),
    reason=f"{UPSTREAM_ENV} is not set: the copies are checked against GitHub in one job",
)
def test_gdb_tools_vendored_from_upstream():
    """Every file vendor.json lists, the manifest among them, is the file of
    tarantool-gdb-tools at the commit it names, byte for byte, with the
    checksum it records: the file served by GitHub at that commit has it, and
    so has the copy of tt. A copy changed by hand, or a vendor.json that
    names another commit than the files are of, fails it, which the checks
    of the copies against vendor.json alone do not catch.

    It needs GitHub, as the other slow tests do, and runs only with
    TT_TEST_GDB_TOOLS_UPSTREAM set, which one cell of the coredump job of
    Full CI sets, through the gdb-tools-upstream input of _integration.yml:
    the copies and GitHub are the same for every job that runs the coredump
    tests. Run it by hand with TT_TEST_GDB_TOOLS_UPSTREAM=1."""
    record = json.loads(GDB_TOOLS_VENDOR.read_text())
    assert record["files"]
    errors = []
    for entry in record["files"]:
        url = f"https://raw.githubusercontent.com/{entry['repo']}/{entry['commit']}/{entry['path']}"
        upstream = hashlib.sha256(fetch(url)).hexdigest()
        copy = hashlib.sha256((GDB_TOOLS_DIR / entry["file"]).read_bytes()).hexdigest()
        if upstream != entry["sha256"]:
            errors.append(f"{url} has sha256 {upstream}, vendor.json records {entry['sha256']}")
        if copy != upstream:
            errors.append(f"the copy of {entry['file']} has sha256 {copy}, {url} has {upstream}")
    assert not errors, "\n".join(errors)
