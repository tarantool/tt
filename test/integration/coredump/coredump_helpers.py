"""What the coredump tests share: the environment they run tt and gdb in,
the kernel cores they take, and the manifest of the extensions of
tarantool-gdb-tools tt vendors."""

import contextlib
import glob
import json
import os
import re
import resource
import subprocess
import time
from pathlib import Path

import pytest

# spell-checker:ignore getrlimit setrlimit rlimit coredumpctl apport

# The gdb extensions tt embeds, which pack puts into ext/ of the archive,
# and among them those of tarantool-gdb-tools tt vendors: their manifest,
# which names the versions of Tarantool and the architectures each of them
# was checked on, and vendor.json, which says the commit and the checksum
# of each file.
EXTENSIONS_DIR = (
    Path(__file__).parents[3] / "modules" / "coredump" / "internal" / "coredump" / "extensions"
)
GDB_TOOLS_DIR = EXTENSIONS_DIR / "gdb-tools"
GDB_TOOLS_MANIFEST = GDB_TOOLS_DIR / "extensions.json"
GDB_TOOLS_VENDOR = GDB_TOOLS_DIR / "vendor.json"

# A kernel core goes where kernel.core_pattern says, which the tests may not
# clean up (see kernel_core): the tests of such cores are run explicitly,
# with TT_ENABLE_COREDUMP_TESTS=1 python3 -m pytest test/integration/coredump/.
skip_coredump_cond = os.getenv("TT_ENABLE_COREDUMP_TESTS") is None
skip_coredump_reason = "Should be launched explicitly to control coredump it produces"

# The Lua that crashes a tarantool.
CRASH = "require('ffi').cast('char *', 0)[0] = 42"

# How long the tests wait for a core the program core_pattern pipes it to
# writes, which it may do after the process is gone, in seconds.
CORE_WAIT = 30


def gdb_env(env=None, home=None):
    """env, the environment of the tests by default, for a tt that runs gdb
    with its Python. actions/setup-python points LD_LIBRARY_PATH at the lib
    of the Python it installs, and gdb, linked against the libpython of the
    system, loads that one instead: it finds no _struct, which the libpython
    of Debian has built in, and the extensions fail on import struct. With
    home, an empty directory, gdb finds no init file of the user there:
    gdb.sh runs gdb without -nx, as a user keeps theirs, and one with "set
    print pretty on" breaks what the tests read of the output of gdb."""
    env = dict(os.environ if env is None else env)
    env.pop("LD_LIBRARY_PATH", None)
    if home is not None:
        home.mkdir(parents=True, exist_ok=True)
        env["HOME"] = home.as_posix()
        env["XDG_CONFIG_HOME"] = (home / ".config").as_posix()
    return env


def offline_env(tmp_path):
    """The environment of a tt that cannot reach GitHub, where inspect looks
    up the gdb extensions of the version of a core: every request goes to a
    proxy that refuses the connection. The cache of the user, which may
    hold them, is replaced with an empty one. gdb runs with its own Python
    and no init file of the user (gdb_env)."""
    env = gdb_env(home=tmp_path / "home")
    for name in ("HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"):
        env[name] = "http://127.0.0.1:9"
    for name in ("NO_PROXY", "no_proxy"):
        env.pop(name, None)
    env["XDG_CACHE_HOME"] = (tmp_path / "cache").as_posix()
    return env


def has_debug_info(tarantool):
    """Whether gdb finds the debugging information of tarantool: the line of
    its main, as gdb.sh asks for it."""
    process = subprocess.run(
        ["gdb", "-batch", "-n", tarantool, "-ex", "info line main"],
        capture_output=True,
        text=True,
        env=gdb_env(),
    )
    return re.search(r'Line \d+ of ".*/src/main\.cc"', process.stdout) is not None


def elf_type(path):
    """The type of the ELF file at path, e_type: 2 of an executable linked
    at its addresses, ET_EXEC, as the static builds of Tarantool are, 3 of a
    shared object or a PIE, ET_DYN; None of a file that is no ELF one."""
    with open(path, "rb") as f:
        header = f.read(18)
    if len(header) < 18 or header[:4] != b"\x7fELF":
        return None
    return int.from_bytes(header[16:18], "little" if header[5] == 1 else "big")


def is_pie(path):
    """Whether the ELF file at path is a position-independent executable,
    of type ET_DYN, where an official tarantool is one of ET_EXEC."""
    return elf_type(path) == 3


def matching(wildcards):
    """The files that match wildcards."""
    return {path for wildcard in wildcards for path in glob.glob(wildcard)}


def wait_for_new(wildcards, before):
    """The files that match wildcards and are not among before, once there
    are some, which the program core_pattern pipes a core to may write after
    the process is gone; none after CORE_WAIT seconds."""
    deadline = time.monotonic() + CORE_WAIT
    while True:
        new = matching(wildcards) - before
        if new or time.monotonic() > deadline:
            return new
        time.sleep(0.2)


def coredumpctl_core(work, pid):
    """The core of the process pid that systemd-coredump keeps, written into
    work: coredumpctl knows of it once systemd-coredump has stored it, which
    may be after the process is gone, and is asked again until it does, for
    CORE_WAIT seconds."""
    core = work / "core"
    deadline = time.monotonic() + CORE_WAIT
    while True:
        dump = subprocess.run(
            ["coredumpctl", "dump", str(pid), f"--output={core}"],
            capture_output=True,
            text=True,
        )
        if dump.returncode == 0 and core.is_file():
            return core
        if time.monotonic() > deadline:
            pytest.fail(f"coredumpctl has no core of {pid}: {dump.stderr}")
        time.sleep(0.5)


def kernel_core(work, argv):
    """The core the kernel dumps of the process of argv, run in work, which
    crashes, and the output of the process: the file of a core_pattern of a
    file; with apport, the core of its report, or the one it writes into
    work as the kernel would, of an executable of no package, which it makes
    no report of ("executable does not belong to a package, ignoring"); or
    the core systemd-coredump keeps, written into work. A core_pattern of
    another program fails the test.

    The report of apport is removed once it is unpacked: apport writes none
    of an executable while an unseen one of it is there. A core of a file
    pattern outside work, or the one systemd-coredump keeps, is left where
    it is: that is why the tests of kernel cores are run explicitly."""
    with open("/proc/sys/kernel/core_pattern") as f:
        core_pattern = f.read().strip()
    _, hard = resource.getrlimit(resource.RLIMIT_CORE)
    if hard == 0:
        pytest.skip("the hard RLIMIT_CORE is 0: the kernel dumps no core")

    if not core_pattern.startswith("|"):
        # spell-checker:disable-next-line
        wildcard = re.sub("%[cdeEghiIpPstu]", "*", core_pattern.replace("%%", "%"))
        # A pattern without %p gets ".PID" appended when core_uses_pid is set
        # (see core(5)).
        with open("/proc/sys/kernel/core_uses_pid") as f:
            if f.read().strip() != "0" and "%p" not in core_pattern:
                wildcard += ".*"
        if not os.path.isabs(wildcard):
            wildcard = str(work / wildcard)
        wildcards = [wildcard]
    elif "apport" in core_pattern:
        wildcards = ["/var/crash/*.crash", str(work / "core"), str(work / "core.*")]
    elif "systemd-coredump" in core_pattern:
        wildcards = None
    else:
        pytest.fail(f"Unexpected core pattern '{core_pattern}'")
    before = matching(wildcards) if wildcards is not None else set()

    def unlimited_core():
        resource.setrlimit(resource.RLIMIT_CORE, (hard, hard))

    process = subprocess.Popen(
        argv,
        cwd=work,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        preexec_fn=unlimited_core,
    )
    output, _ = process.communicate(timeout=120)
    assert process.returncode != 0, output
    assert "Segmentation fault" in output, output

    if wildcards is None:
        return coredumpctl_core(work, process.pid), output

    # apport may write the core into work as well as a report, with the
    # core of RLIMIT_CORE of the process.
    new = wait_for_new(wildcards, before)
    cores = [path for path in new if not path.endswith(".crash")]
    reports = [path for path in new if path.endswith(".crash")]
    assert len(cores) <= 1 and len(reports) <= 1 and new, (wildcards, new, output)
    if cores:
        for report in reports:
            with contextlib.suppress(OSError):
                os.remove(report)
        return Path(cores[0]), output

    unpacked = work / "apport"
    subprocess.run(["apport-unpack", reports[0], unpacked], check=True)
    with contextlib.suppress(OSError):
        os.remove(reports[0])
    return unpacked / "CoreDump", output


def gdb_tools_extensions():
    """The entries of the manifest of the extensions of tarantool-gdb-tools tt
    vendors that load in gdb."""
    entries = json.loads(GDB_TOOLS_MANIFEST.read_text())["extensions"]
    return [entry for entry in entries if "gdb" in entry["debuggers"]]


def release_of(version_line):
    """The release x.y.z of the first line of a version file, as a tuple."""
    match = re.match(r"Tarantool (?:Enterprise )?(\d+)\.(\d+)\.(\d+)", version_line)
    assert match, version_line
    return tuple(int(part) for part in match.groups())


def minor_of(release):
    """The minor of a release x.y.z, (x, y)."""
    major, minor, _ = release.split(".")
    return int(major), int(minor)


def checked_minors(entry):
    """The first and the last minor the manifest says the extension of entry
    is checked on: every patch of every minor from that of min to that of max,
    as inspect reads the range (CI runs every minor of a range on its last
    patch)."""
    return minor_of(entry["tarantool"]["min"]), minor_of(entry["tarantool"]["max"])


def checked_on(entry, version_line, machine):
    """Whether the manifest says the extension of entry is checked on the
    Tarantool of version_line, the first line of its version file, by the
    minor, and on machine."""
    first, last = checked_minors(entry)
    return first <= release_of(version_line)[:2] <= last and machine in entry["arch"]
