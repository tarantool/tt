import contextlib
import ctypes
import filecmp
import glob
import json
import os
import platform
import re
import resource
import shutil
import subprocess
import sys
import tarfile
from pathlib import Path

import pytest

from utils import run_command_and_get_output

# The gdb extensions tt embeds, which pack puts into ext/ of the archive.
EXTENSIONS_DIR = (
    Path(__file__).parents[3] / "modules" / "coredump" / "internal" / "coredump" / "extensions"
)

# spell-checker:ignore getrlimit setrlimit rlimit rlim coredumps coredumpctl apport
# spell-checker:ignore prctl PTRACER Yama

# Fixture below produces a coredump. The location of the coredumps is
# configured over /proc/sys/kernel/core_pattern file in a various ways
# but this fixture recognizes only some of them for now:
# - direct file pattern
# - pipe to apport tool (assumes that *.crash files are in /var/crash)
# - pipe to systemd-coredump (assumes that coredumps are in
#                             /var/lib/systemd/coredump)
# For the cases above generated files are removed after tests but in general
# there is no guarantee. So tests that use this fixture are disabled by default
# (marked with skipif) in order to avoid coredumps "leaks".
# One may launch them explicitly using TT_ENABLE_COREDUMP_TESTS environment
# TT_ENABLE_COREDUMP_TESTS=1 python3 -m pytest test/integration/coredump/.


skip_coredump_cond = os.getenv("TT_ENABLE_COREDUMP_TESTS") is None
skip_coredump_reason = "Should be launched explicitly to control coredump it produces"


def generate_coredump(tmp_path_factory, tarantool_bin="tarantool") -> Path:
    coredump_dir = tmp_path_factory.mktemp("coredump")
    with open("/proc/sys/kernel/core_pattern", "r") as f:
        core_pattern = f.read().strip()

    def coredump_apport(core_source, outdir):
        process = subprocess.run(["apport-unpack", core_source, outdir])
        assert process.returncode == 0
        return outdir / "CoreDump"

    def coredump_systemd(core_source, outdir):
        core = outdir / core_source.stem
        process = subprocess.run(["coredumpctl", "dump", f"--output={core}"])
        assert process.returncode == 0
        return core

    to_coredump = None
    if not core_pattern.startswith("|"):
        core_wildcard = core_pattern.replace("%%", "%")
        # spell-checker:disable-next-line
        core_wildcard = re.sub("%[cdeEghiIpPstu]", "*", core_wildcard)
        # A pattern without %p gets ".PID" appended when core_uses_pid is set
        # (see core(5)).
        with open("/proc/sys/kernel/core_uses_pid", "r") as f:
            if f.read().strip() != "0" and "%p" not in core_pattern:
                core_wildcard += ".*"
        if not os.path.isabs(core_wildcard):
            core_wildcard = str(coredump_dir / core_wildcard)
    elif re.search(r"apport", core_pattern):
        core_wildcard = "/var/crash/*.crash"
        to_coredump = coredump_apport
    elif re.search(r"systemd-coredump", core_pattern):
        core_wildcard = "/var/lib/systemd/coredump/*"
        to_coredump = coredump_systemd
    else:
        assert False, "Unexpected core pattern '{}'".format(core_pattern)
    cores = set(glob.glob(core_wildcard))

    # Setup ulimit -c.
    rlim_core_soft, rlim_core_hard = resource.getrlimit(resource.RLIMIT_CORE)
    if rlim_core_soft != resource.RLIM_INFINITY:
        resource.setrlimit(resource.RLIMIT_CORE, (resource.RLIM_INFINITY, rlim_core_hard))
    # Crash tarantool.
    cmd = [tarantool_bin, "-e", "require('ffi').cast('char *', 0)[0] = 42"]
    rc, output = run_command_and_get_output(cmd, cwd=coredump_dir)
    # Restore ulimit -c.
    resource.setrlimit(resource.RLIMIT_CORE, (rlim_core_soft, rlim_core_hard))
    assert rc != 0
    assert re.search(r"Segmentation fault", output)

    # Find the newly generated coredump.
    new_cores = set(glob.glob(core_wildcard)) - cores
    assert len(new_cores) == 1
    core_path = Path(next(iter(new_cores)))

    return core_path if to_coredump is None else to_coredump(core_path, coredump_dir)


@pytest.fixture(scope="session")
def coredump(tmp_path_factory) -> Path:
    return generate_coredump(tmp_path_factory)


# The Lua that crashes a tarantool.
CRASH = "require('ffi').cast('char *', 0)[0] = 42"

# What gdb says when it cannot trace a process: "warning: ptrace: Operation
# not permitted" where it runs one in a container without ptrace, and
# "Could not attach to process" where the ptrace_scope of Yama keeps it off
# one that is not its child.
NO_PTRACE = re.compile(r"ptrace: |Could not (?:attach to|trace the inferior)")


def check_gcore(core, process):
    """Check that gdb, run as process, made core, or skip the test when gdb
    cannot trace the process to make it."""
    output = process.stdout + process.stderr
    if not core.is_file() and NO_PTRACE.search(output):
        pytest.skip(f"gdb cannot trace a process here to make a core: {output.strip()}")
    assert core.is_file(), output


def gcore_of_crash(tmp_path_factory, name, lua, commands=()) -> Path:
    """A core of the tarantool of PATH that crashed running lua, made by
    gcore of the gdb it runs under, at the signal, the commands of gdb run
    before it runs the tarantool. It leaves nothing behind the temporary
    directory, whatever core_pattern says, so the tests of such cores need
    no TT_ENABLE_COREDUMP_TESTS; they need gdb on Linux, allowed to trace a
    process, which a container may not allow."""
    if sys.platform != "linux" or shutil.which("gdb") is None:
        pytest.skip("needs gdb on Linux to make a core")
    tarantool = shutil.which("tarantool")
    assert tarantool is not None, "no tarantool on PATH"
    work = tmp_path_factory.mktemp(name)
    core = work / "core"
    before = [arg for command in commands for arg in ("-ex", command)]
    cmd = ["gdb", "-batch", "-n", *before, "-ex", "run", "-ex", f"gcore {core}", "-ex", "kill"]
    process = subprocess.run(
        [*cmd, "--args", tarantool, "-e", lua],
        cwd=work,
        capture_output=True,
        text=True,
        env=gdb_env(),
    )
    check_gcore(core, process)
    return core


@pytest.fixture(scope="session")
def gcore(tmp_path_factory) -> Path:
    return gcore_of_crash(tmp_path_factory, "gcore", CRASH)


# The name of the copy of libm a tarantool loads and deletes before it
# crashes, in its working directory.
DELETED_LIBRARY = "libtt-test-deleted.so"

# The Lua that loads a copy of the libm the tarantool has mapped, deletes
# the copy, as Tarantool deletes its copy of the module of a C function of
# box.func once it has loaded it, and crashes.
CRASH_WITH_DELETED_LIBRARY = f"""
local ffi = require('ffi')
local fio = require('fio')
local libm
for line in io.lines('/proc/self/maps') do
    libm = libm or line:match('%s(/%S*/libm%.so[.%d]*)$')
end
if libm ~= nil then
    local copy = fio.pathjoin(fio.cwd(), '{DELETED_LIBRARY}')
    assert(fio.copyfile(libm, copy))
    ffi.load(copy)
    os.remove(copy)
end
{CRASH}
"""


@pytest.fixture(scope="session")
def gcore_deleted_library(tmp_path_factory) -> Path:
    return gcore_of_crash(tmp_path_factory, "gcore_deleted", CRASH_WITH_DELETED_LIBRARY)


# The names of the copies of libm a tarantool loads and replaces before it
# crashes, in its working directory: one replaced with a new copy of the
# same content, as a package reinstalled replaces it, and a link to the
# other one re-pointed to a copy of libc, as an upgrade re-points a link
# to a new version.
SAME_LIBRARY = "libtt-test-same.so"
LINKED_LIBRARY = "libtt-test-link.so"

CRASH_WITH_REPLACED_LIBRARIES = f"""
local ffi = require('ffi')
local fio = require('fio')
local libm, libc
for line in io.lines('/proc/self/maps') do
    libm = libm or line:match('%s(/%S*/libm%.so[.%d]*)$')
    libc = libc or line:match('%s(/%S*/libc%.so[.%d]*)$')
end
if libm ~= nil and libc ~= nil then
    local same = fio.pathjoin(fio.cwd(), '{SAME_LIBRARY}')
    assert(fio.copyfile(libm, same))
    ffi.load(same)
    assert(fio.copyfile(libm, same .. '.new'))
    assert(fio.rename(same .. '.new', same))

    local old = fio.pathjoin(fio.cwd(), 'libtt-test-old.so')
    local new = fio.pathjoin(fio.cwd(), 'libtt-test-new.so')
    local link = fio.pathjoin(fio.cwd(), '{LINKED_LIBRARY}')
    assert(fio.copyfile(libm, old))
    assert(fio.copyfile(libc, new))
    assert(fio.symlink(old, link))
    ffi.load(link)
    assert(fio.unlink(link))
    assert(fio.symlink(new, link))
end
{CRASH}
"""


@pytest.fixture(scope="session")
def gcore_replaced_libraries(tmp_path_factory) -> Path:
    return gcore_of_crash(tmp_path_factory, "gcore_replaced", CRASH_WITH_REPLACED_LIBRARIES)


# The Lua that sets the coredump_filter of the tarantool to 0x23, the bit of
# the ELF headers off (see core(5)), and crashes: Linux and gcore of gdb
# leave the first page of a file mapped out of the core, that of the
# tarantool too, where the interpreter of the tarantool, the name glibc
# gives the dynamic loader, is. They do unless the process wrote to the
# mapping of the page: a mapping with a page of its own, a copy of that of
# the file, is anonymous memory to both, dumped whole, as that of the
# dynamic loader, where gdb writes its breakpoints, may be.
CRASH_WITH_FILTER = f"""
local filter = io.open('/proc/self/coredump_filter', 'w')
filter:write('0x23')
filter:close()
{CRASH}
"""


@pytest.fixture(scope="session")
def gcore_filtered(tmp_path_factory) -> Path:
    """A gcore of the tarantool of PATH that crashed with CRASH_WITH_FILTER,
    without the first page of the tarantool, as the fixture checks.

    gdb runs the tarantool with displaced stepping off. On, gdb steps over
    each of its breakpoints in the dynamic loader, where it learns of the
    libraries loaded, on a copy of the instruction at the entry point of the
    tarantool, which gives the mapping of the entry a page of its own. The
    entry and the ELF headers are in the same mapping of the static
    tarantools of 3.3 to 3.6 on x86-64 and of those of aarch64, which no -z
    separate-code links, and the first page of the tarantool was in the
    core there: the tests of the core did not check what they were for. Off,
    gdb steps over a breakpoint where it is and writes nothing to the
    tarantool."""
    in_place = ["set displaced-stepping off"]
    core = gcore_of_crash(tmp_path_factory, "gcore_filtered", CRASH_WITH_FILTER, in_place)
    tarantool = shutil.which("tarantool")
    assert not has_first_page(core, tarantool), f"the core holds the first page of {tarantool}"
    return core


# The types of a program header and of a note of an ELF core: PT_LOAD of a
# segment, PT_NOTE of the notes, and NT_FILE of the files the core maps.
PT_LOAD, PT_NOTE, NT_FILE = 1, 4, 0x46494C45


def word(data, at, size=8):
    """The little-endian number of size bytes of data at the offset at."""
    return int.from_bytes(data[at : at + size], "little")


def core_segments(core):
    """The segments of core, an ELF core of 64 bits of a little-endian
    machine, as of x86-64 and aarch64: the ranges of addresses of those
    with content, PT_LOAD, and the files mapped, NT_FILE, the address, the
    offset in pages and the path of each mapping. The headers and the notes
    alone are read, not the whole core."""
    contents, mappings, notes = [], [], []
    with open(core, "rb") as f:
        header = f.read(64)
        f.seek(word(header, 32))
        segments = [f.read(word(header, 54, 2)) for _ in range(word(header, 56, 2))]
        for segment in segments:
            kind, offset = word(segment, 0, 4), word(segment, 8)
            address, length = word(segment, 16), word(segment, 32)
            if kind == PT_LOAD and length > 0:
                contents.append((address, address + length))
            elif kind == PT_NOTE:
                f.seek(offset)
                notes.append(f.read(length))
    for data in notes:
        note = 0
        while note + 12 <= len(data):
            name_size, desc_size = word(data, note, 4), word(data, note + 4, 4)
            note_type = word(data, note + 8, 4)
            desc = note + 12 + (name_size + 3) // 4 * 4
            note = desc + (desc_size + 3) // 4 * 4
            if note_type != NT_FILE:
                continue
            files = word(data, desc)
            paths = data[desc + 16 + 24 * files : desc + desc_size].split(b"\0")
            for index in range(files):
                entry = desc + 16 + 24 * index
                path = os.fsdecode(paths[index])
                mappings.append((word(data, entry), word(data, entry + 16), path))
    return contents, mappings


def has_first_page(core, path):
    """Whether core holds the first page of the file at path where the file
    is loaded: at the lowest address the core maps it at from the offset 0,
    that of the ELF headers of an executable. A small executable maps the
    page again, for its data."""
    contents, mappings = core_segments(core)
    path = os.path.realpath(path)
    starts = [start for start, page, mapped in mappings if page == 0 and mapped == path]
    assert starts, f"the core maps no {path}"
    return any(low <= min(starts) < high for low, high in contents)


# The dynamic loaders of glibc by the machine of the host, which run the
# program they are given: ld.so PROGRAM.
LOADERS = {
    "x86_64": "/lib64/ld-linux-x86-64.so.2",
    "aarch64": "/lib/ld-linux-aarch64.so.1",
}


@pytest.fixture(scope="session")
def gcore_via_loader(tmp_path_factory) -> Path:
    """A core of the tarantool of PATH that the dynamic loader ran, ld.so
    TARANTOOL, crashed, made by gcore of the gdb it runs under: the kernel
    ran the loader, whose program headers AT_PHDR of the core gives, and the
    loader mapped the tarantool."""
    if sys.platform != "linux" or shutil.which("gdb") is None:
        pytest.skip("needs gdb on Linux to make a core")
    loader = LOADERS.get(platform.machine())
    if loader is None or not os.path.isfile(loader):
        pytest.skip(f"no dynamic loader of glibc known for {platform.machine()}")
    tarantool = shutil.which("tarantool")
    assert tarantool is not None, "no tarantool on PATH"
    work = tmp_path_factory.mktemp("gcore_via_loader")
    core = work / "core"
    cmd = ["gdb", "-batch", "-n", "-ex", "run", "-ex", f"gcore {core}", "-ex", "kill"]
    process = subprocess.run(
        [*cmd, "--args", loader, tarantool, "-e", CRASH],
        cwd=work,
        capture_output=True,
        text=True,
        env=gdb_env(),
    )
    assert core.is_file(), process.stdout + process.stderr
    return core


@pytest.fixture(scope="session")
def alt_tarantool(tmp_path_factory) -> Path:
    """A tarantool other than the one on PATH, for the tests of which executable
    gets packed.

    A copy of the system one with a marker appended, so the two are different
    files and the archive shows which of them was packed; the loader, gdb and
    the pack script all ignore what follows the ELF image. It used to be a
    Tarantool built from source, which took minutes, and the archive was
    checked by version alone.
    """
    system = shutil.which("tarantool")
    assert system is not None, "no tarantool on PATH"
    alt = tmp_path_factory.mktemp("alt_tarantool") / "bin" / "tarantool"
    alt.parent.mkdir()
    shutil.copy(system, alt)
    with alt.open("ab") as f:
        f.write(b"\ntt coredump tests: the alternative tarantool\n")
    return alt


@pytest.fixture(scope="session")
def coredump_alt(tmp_path_factory, alt_tarantool) -> Path:
    return generate_coredump(tmp_path_factory, alt_tarantool)


@pytest.fixture(scope="session")
def coredump_pack(tt_cmd, coredump):
    """The exit code and the output of tt coredump pack of coredump, run in
    the directory of the core once for the session.

    test_coredump_pack checks them, and the tests of unpack and inspect take
    the archive. It is the same command on the same core, and a pack, which
    runs gdb on the core and the tarantool and gzips the tarantool and its
    libraries, takes seconds."""
    cmd = [tt_cmd, "coredump", "pack", coredump.as_posix()]
    return run_command_and_get_output(cmd, cwd=coredump.parents[0])


@pytest.fixture(scope="session")
def coredump_packed(coredump, coredump_pack):
    rc, output = coredump_pack
    assert rc == 0, output
    files = glob.glob(os.path.join(os.path.dirname(coredump), "*.tar.gz"))
    assert len(files) == 1
    return files[0]


@pytest.fixture(scope="session")
def coredump_unpacked(tt_cmd, coredump_packed):
    cmd = [tt_cmd, "coredump", "unpack", coredump_packed]
    rc, _ = run_command_and_get_output(cmd, cwd=os.path.dirname(coredump_packed))
    assert rc == 0
    packed_name = os.path.basename(coredump_packed)
    unpacked = os.path.join(os.path.dirname(coredump_packed), packed_name.split(".")[0])
    assert os.path.isdir(unpacked)
    return unpacked


def test_coredump_pack_no_arg(tt_cmd, tmp_path):
    cmd = [tt_cmd, "coredump", "pack"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0
    assert re.search(r"tt coredump pack", output)


def test_coredump_pack_no_such_file(tt_cmd, tmp_path):
    cmd = [tt_cmd, "coredump", "pack", "wrong_core_file"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0
    assert re.search(r"not a core dump: wrong_core_file: .*no such file or directory", output)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_pack(coredump, coredump_pack):
    rc, output = coredump_pack
    assert rc == 0, output
    assert re.search(r"Core was successfully packed.", output)
    archives = list(coredump.parent.glob("*.tar.gz"))
    assert len(archives) == 1
    check_archive_members(archives[0])


def check_tarantool_in_archive(tmp_path, expected_tarantool):
    archives = list(tmp_path.glob("*.tar.gz"))
    assert len(archives) == 1

    # Extract the Tarantool executable: it is the expected one, byte for byte.
    rc = subprocess.run(
        ["tar", "xzf", archives[0], "--wildcards", "tarantool-core-*/tarantool"],
        cwd=tmp_path,
    ).returncode
    assert rc == 0
    unpacked_tarantools = list(tmp_path.glob("tarantool-core-*/tarantool"))
    assert len(unpacked_tarantools) == 1
    assert unpacked_tarantools[0].read_bytes() == Path(expected_tarantool).read_bytes()


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_pack_executable(tt_cmd, tmp_path, coredump_alt, alt_tarantool):
    cmd = [tt_cmd, "coredump", "pack", "-e", alt_tarantool, coredump_alt]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Core was successfully packed.", output)

    check_tarantool_in_archive(tmp_path, alt_tarantool)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_pack_executable_project(tt_cmd, tmp_path, coredump_alt, alt_tarantool):
    # The tarantool of the project scope, as tt binary install selects it.
    (tmp_path / ".tarantool" / "bin").mkdir(parents=True)
    os.symlink(alt_tarantool, tmp_path / ".tarantool" / "bin" / "tarantool")

    cmd = [tt_cmd, "coredump", "pack", coredump_alt]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Core was successfully packed.", output)

    check_tarantool_in_archive(tmp_path, alt_tarantool)


def test_coredump_pack_other_tarantool(tt_cmd, tmp_path, gcore):
    """pack refuses a core with a tarantool of another build ID than the
    executable of the core: here a copy of the one that dumped it with the
    last byte of its build ID changed. The copy has the layout of the
    original, and gdb, which only warns of it, found the shared libraries of
    the core with it: the check of pack was that gdb found some."""
    if shutil.which("objcopy") is None:
        pytest.skip("needs objcopy")
    tarantool = shutil.which("tarantool")
    note = tmp_path / "build-id"
    only = ["objcopy", "-O", "binary", "--only-section=.note.gnu.build-id"]
    subprocess.run([*only, tarantool, note], check=True)
    data = bytearray(note.read_bytes())
    if not data:
        pytest.skip("the tarantool of PATH has no build ID")
    data[-1] ^= 0xFF
    note.write_bytes(data)
    other = tmp_path / "bin" / "tarantool"
    other.parent.mkdir()
    update = ["objcopy", f"--update-section=.note.gnu.build-id={note}"]
    subprocess.run([*update, tarantool, other], check=True)

    cmd = [tt_cmd, "coredump", "pack", "-e", other, gcore]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0, output
    assert "core dump file is produced by the different Tarantool executable" in output
    assert re.search(r"the executable of the core has build ID [0-9a-f]+, the binary", output)
    # The check is made before anything is staged.
    assert set(tmp_path.iterdir()) == {note, other.parent}


# The programs the pack script runs: bash runs it, tar packs the archive,
# with gzip, and the rest stage the artefacts. Neither gdb nor file(1) is
# among them.
PACK_TOOLS = (
    "bash",
    "basename",
    "cat",
    "date",
    "dirname",
    "getopt",
    "gzip",
    "hostname",
    "ln",
    "mkdir",
    "mktemp",
    "rm",
    "tar",
)


def test_coredump_pack_without_gdb(tt_cmd, tmp_path, gcore):
    """pack needs no debugger, so that a host with lldb or with none packs a
    core: with a PATH of the programs of the pack script alone, without gdb
    and file(1), it packs the core with its shared libraries, and inspect,
    with gdb, shows its backtrace. pack refused a host without gdb, which it
    asked for the shared libraries of the core."""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    for tool in PACK_TOOLS:
        path = shutil.which(tool)
        if path is None:
            pytest.skip(f"no {tool} on PATH")
        os.symlink(path, bin_dir / tool)
    env = gdb_env()
    env["PATH"] = bin_dir.as_posix()

    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-e", shutil.which("tarantool"), "-d", out, gcore]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0, output
    assert re.search(r"Core was successfully packed.", output)

    archives = list(out.glob("*.tar.gz"))
    assert len(archives) == 1
    members = check_archive_members(archives[0])
    assert any(re.search(r"/libc[.-]", "/" + member) for member in members), members

    rc, output = inspect(tt_cmd, tmp_path, archives[0], ["bt 1"])
    assert rc == 0, output
    assert re.search(r"#0 ", output), output


def gdb_shared_libraries(tarantool, core):
    """The shared libraries gdb lists for core of tarantool with info shared,
    by their absolute paths, and the libthread_db gdb says it loads for the
    core, None for none."""
    listed = subprocess.run(
        ["gdb", "-batch", "-n", tarantool, "-c", core, "-ex", "info shared"],
        capture_output=True,
        text=True,
        env=gdb_env(),
    )
    libs, table = [], False
    for line in listed.stdout.splitlines():
        fields = line.split()
        if "Shared Object Library" in line:
            table = True
        elif table and fields and fields[-1].startswith("/"):
            libs.append(fields[-1])

    greeting = subprocess.run(
        ["gdb", "-n", tarantool, "-c", core, "-ex", "quit"],
        capture_output=True,
        text=True,
        env=gdb_env(),
    )
    thread_db = re.search(r'Using host libthread_db library "([^"]+)"', greeting.stdout)
    return libs, thread_db.group(1) if thread_db is not None else None


@pytest.mark.parametrize("source", ["gcore", "gcore_filtered", "coredump"])
def test_coredump_pack_libraries_of_gdb(tt_cmd, tmp_path, request, source):
    """pack reads the shared libraries of a core from the core, where it
    asked gdb: the archive has every library info shared of gdb lists,
    under its path, and the libthread_db gdb loads for the core. The core
    is a gcore, one without the first page of the tarantool, gcore_filtered,
    where pack reads the name of the dynamic loader from the tarantool, as
    gdb does, and a core of Linux where the coredump tests are enabled."""
    if source == "coredump" and skip_coredump_cond:
        pytest.skip(skip_coredump_reason)
    if shutil.which("gdb") is None:
        pytest.skip("needs gdb to compare with")
    core = request.getfixturevalue(source)
    tarantool = shutil.which("tarantool")
    libs, thread_db = gdb_shared_libraries(tarantool, core)
    assert libs

    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-e", tarantool, "-d", out, core]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output

    archives = list(out.glob("*.tar.gz"))
    assert len(archives) == 1
    members = check_archive_members(archives[0])
    assert {lib.lstrip("/") for lib in libs} <= members, (libs, members)
    if thread_db is not None:
        assert thread_db.lstrip("/") in members, (thread_db, members)


def test_coredump_pack_via_loader(tt_cmd, tmp_path, gcore_via_loader):
    """pack takes a core of a tarantool the dynamic loader ran, ld.so
    TARANTOOL, with the shared libraries gdb lists: AT_PHDR of the core is
    that of the loader, whose build ID pack compared with that of the
    tarantool, and refused the core. The tarantool is packed once, as the
    tarantool of the archive, and not among the libraries too."""
    tarantool = shutil.which("tarantool")
    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-e", tarantool, "-d", out, gcore_via_loader]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    assert "Cannot read the link map" not in output, output

    archives = list(out.glob("*.tar.gz"))
    assert len(archives) == 1
    members = check_archive_members(archives[0])
    copies = [
        member
        for member in members - {"tarantool"}
        if os.path.isfile("/" + member) and os.path.samefile("/" + member, tarantool)
    ]
    assert not copies, copies

    if shutil.which("gdb") is not None:
        libs, _ = gdb_shared_libraries(tarantool, gcore_via_loader)
        assert {lib.lstrip("/") for lib in libs} <= members, (libs, members)


def test_coredump_pack_deleted_library(tt_cmd, tmp_path, gcore_deleted_library):
    """pack of a core with a shared library deleted since it was loaded
    leaves the library out with a notice and packs the rest. The symbolic
    link the pack script staged for the library dangled, tar failed on it
    with "Cannot stat", and pack failed with exit status 2, leaving a
    partial archive and its temporary directory behind."""
    core = gcore_deleted_library
    deleted = core.parent / DELETED_LIBRARY
    libs, _ = gdb_shared_libraries(shutil.which("tarantool"), core)
    if deleted.as_posix() not in libs:
        pytest.skip("the tarantool of PATH maps no libm to load a copy of")

    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-d", out, core]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    assert f"The shared library {deleted} of the core is not packed: " in output
    assert "gdb will have no symbols of it." in output

    # The archive and nothing else, with every other library gdb lists.
    archives = list(out.glob("*.tar.gz"))
    assert set(out.iterdir()) == set(archives)
    assert len(archives) == 1
    members = check_archive_members(archives[0])
    assert deleted.as_posix().lstrip("/") not in members
    others = {lib.lstrip("/") for lib in libs if lib != deleted.as_posix()}
    assert others <= members, (others, members)


def test_coredump_pack_replaced_libraries(tt_cmd, tmp_path, gcore_replaced_libraries):
    """pack compares the build ID of a shared library in the core with that
    of the file at its path. A library replaced with a file of the same
    content, whose mapping the core marks deleted, is packed, where it was
    left out; a link re-pointed to another library is left out with a
    notice, where the other library was packed under its name, and gdb read
    the core with the symbols of the wrong file."""
    core = gcore_replaced_libraries
    same, link = core.parent / SAME_LIBRARY, core.parent / LINKED_LIBRARY
    libs, _ = gdb_shared_libraries(shutil.which("tarantool"), core)
    if same.as_posix() not in libs:
        pytest.skip("the tarantool of PATH maps no libm and libc to load copies of")

    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-d", out, core]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    assert (
        f"The shared library {link} of the core is not packed: the file at its path is "
        "not the one the process loaded: it has build ID "
    ) in output, output
    assert f"The shared library {same} " not in output, output

    archives = list(out.glob("*.tar.gz"))
    assert len(archives) == 1
    members = check_archive_members(archives[0])
    assert same.as_posix().lstrip("/") in members
    assert link.as_posix().lstrip("/") not in members


# prctl(2) of Yama: PR_SET_PTRACER with PR_SET_PTRACER_ANY lets any process
# of the user trace the caller.
PR_SET_PTRACER = 0x59616D61
PR_SET_PTRACER_ANY = ctypes.c_ulong(-1)


def allow_tracing():
    """Let gdb attach to this process, a child of the test that gdb is not
    the parent of, past the ptrace_scope of Yama. A kernel without Yama
    refuses the call, and needs none."""
    ctypes.CDLL(None).prctl(PR_SET_PTRACER, PR_SET_PTRACER_ANY, 0, 0, 0)


@contextlib.contextmanager
def running(cmd, cwd):
    """The process of cmd, running in cwd with its standard output piped,
    which gdb may attach to; it is killed on the exit."""
    process = subprocess.Popen(cmd, cwd=cwd, stdout=subprocess.PIPE, preexec_fn=allow_tracing)
    try:
        yield process
    finally:
        process.kill()
        process.wait()


def gcore_of_running(process, core):
    """Make core, a core of the running process, with gcore of gdb, which
    leaves it running."""
    cmd = ["gdb", "-batch", "-n", "-p", str(process.pid), "-ex", f"gcore {core}", "-ex", "detach"]
    check_gcore(core, subprocess.run(cmd, capture_output=True, text=True, env=gdb_env()))


# The Lua of a tarantool that says it is up and sleeps.
SLEEP = 'io.stdout:write("ready\\n") io.stdout:flush() require("fiber").sleep(100)'


@pytest.mark.parametrize("arg", [[], ["-"]], ids=["omitted", "stdin"])
def test_coredump_pack_pipe(tt_cmd, tmp_path, arg):
    """tt coredump pack -p PID is a kernel.core_pattern pipe: with COREDUMP
    omitted or -, it reads the core from its standard input, stores it as
    <dir>/tarantool-core.PID.TIME and packs it, with the /proc/PID/exe of the
    process, into an archive named by the PID. The core is a gcore of a
    running tarantool. tt fed the pack script to bash on its standard input,
    and the script read the rest of its own text for the core, and pack
    required COREDUMP."""
    if sys.platform != "linux" or shutil.which("gdb") is None:
        pytest.skip("needs gdb on Linux to make a core of a running tarantool")
    tarantool = shutil.which("tarantool")
    assert tarantool is not None, "no tarantool on PATH"
    core = tmp_path / "gcore"
    out = tmp_path / "out"
    out.mkdir()
    unpacked = tmp_path / "unpacked"
    unpacked.mkdir()

    with running([tarantool, "-e", SLEEP], tmp_path) as process:
        assert process.stdout.readline() == b"ready\n"
        gcore_of_running(process, core)
        exe = Path(f"/proc/{process.pid}/exe")

        cmd = [tt_cmd, "coredump", "pack", "-p", str(process.pid), "-d", out, *arg]
        with core.open("rb") as stdin:
            pack = subprocess.run(cmd, cwd=tmp_path, stdin=stdin, capture_output=True, text=True)
        output = pack.stdout + pack.stderr
        assert pack.returncode == 0, output
        assert "Core was successfully packed." in output

        # The archive, named by the PID, and the core, stored as the pack
        # script stores it.
        archives = list(out.glob("*.tar.gz"))
        assert len(archives) == 1, list(out.iterdir())
        assert archives[0].name.startswith(f"tarantool-core-{process.pid}-"), archives[0]
        stored = list(out.glob(f"tarantool-core.{process.pid}.*"))
        assert len(stored) == 1, list(out.iterdir())
        assert set(out.iterdir()) == {archives[0], stored[0]}
        assert filecmp.cmp(stored[0], core, shallow=False)

        # The core of the archive is the one of the standard input, and the
        # tarantool the executable of the process.
        check_archive_members(archives[0])
        members = ["--wildcards", "*/coredump", "*/tarantool"]
        subprocess.run(["tar", "xzf", archives[0], *members], cwd=unpacked, check=True)
        (member_dir,) = unpacked.iterdir()
        assert filecmp.cmp(member_dir / "coredump", core, shallow=False)
        assert filecmp.cmp(member_dir / "tarantool", exe, shallow=False)


def test_coredump_pack_pipe_not_tarantool(tt_cmd, tmp_path):
    """tt coredump pack -p PID of a process whose comm is not tarantool, as
    the kernel.core_pattern pipe gets the core of every process of the host,
    stores the core of the standard input as <dir>/<comm>-core.PID.TIME,
    packs nothing and exits with 0, as the pack script does."""
    if not Path("/proc/self/comm").is_file():
        pytest.skip("needs procfs")
    out = tmp_path / "out"
    out.mkdir()
    # The core is not read: any bytes do, with zeros of the pages the
    # process never touched.
    core = b"\x7fELF the core of sleep\n" + bytes(1 << 20) + b"the end\n"

    with running(["sleep", "100"], tmp_path) as process:
        comm = Path(f"/proc/{process.pid}/comm").read_text().strip()
        cmd = [tt_cmd, "coredump", "pack", "-p", str(process.pid), "-d", out, "-t", "1763869440"]
        pack = subprocess.run(cmd, cwd=tmp_path, input=core, capture_output=True)
    output = (pack.stdout + pack.stderr).decode()
    assert pack.returncode == 0, output
    assert comm != "tarantool"

    stored = out / f"{comm}-core.{process.pid}.1763869440"
    assert set(out.iterdir()) == {stored}, "the core alone, no archive"
    assert stored.read_bytes() == core
    assert f"its core is stored and not packed: {stored}" in output, output
    assert "Core was successfully packed." not in output


def check_archive_members(archive):
    """Check the members of an archive of tt coredump pack: all of them under
    the directory named after the archive, the version, the core, the
    tarantool, gdb.sh and the extensions of tt under their names there, and
    every other member a file of the host under its path, as the shared
    libraries are, but for the debug file of the tarantool, which pack takes
    from a debug directory of gdb. A transformation of the names that
    misfires renames one of them: the version goes missing, and inspect
    refuses the archive, or a library gets a path of no file. Returns the
    members, without the name of the archive."""
    name = os.path.basename(archive).split(".")[0]
    with tarfile.open(archive) as tar:
        members = tar.getnames()
        version = tar.extractfile(f"{name}/version").read().decode()

    assert version.startswith("Tarantool "), version

    rests = set()
    for member in members:
        assert member.startswith(name + "/"), member
        rests.add(member[len(name) + 1 :])

    expected = {"version", "coredump", "tarantool", "gdb.sh"}
    extensions = {"ext/" + path.name for path in EXTENSIONS_DIR.glob("*.py")}
    assert extensions
    assert expected | extensions <= rests, members

    for rest in rests - expected - extensions:
        if rest.startswith("usr/lib/debug/.build-id/"):
            assert re.fullmatch(r"usr/lib/debug/\.build-id/[0-9a-f]{2}/[0-9a-f]+\.debug", rest)
        else:
            assert os.path.isfile("/" + rest), rest

    return rests


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
@pytest.mark.parametrize(
    ("core_dir", "core_name", "cwd", "arg", "out"),
    [
        # A relative path to a core in the working directory, where the
        # archive is made, whose path has "core" in it, as
        # /var/lib/systemd/coredump does: the name of the core matched in the
        # path of the version, which went missing.
        pytest.param("var/lib/systemd/coredump", "core", "core_dir", "core", None, id="cwd"),
        # A relative path through such a directory, and a relative -d.
        pytest.param("coredump", "core.1", "tmp", "coredump/core.1", "out", id="subdir"),
        # The absolute path of a core in such a directory.
        pytest.param("coredump", "core", "tmp", "absolute", None, id="absolute"),
    ],
)
def test_coredump_pack_paths(tt_cmd, tmp_path, coredump, core_dir, core_name, cwd, arg, out):
    core = tmp_path / core_dir / core_name
    core.parent.mkdir(parents=True)
    shutil.copy(coredump, core)

    cwd = core.parent if cwd == "core_dir" else tmp_path
    arg = core.as_posix() if arg == "absolute" else arg
    cmd = [tt_cmd, "coredump", "pack", arg]
    out_dir = cwd
    if out is not None:
        out_dir = cwd / out
        out_dir.mkdir()
        cmd += ["-d", out]
    before = set(out_dir.iterdir())

    rc, output = run_command_and_get_output(cmd, cwd=cwd)
    assert rc == 0, output
    assert re.search(r"Core was successfully packed.", output)

    # The archive is in the -d directory, relative to the working one, or in
    # the working one, and pack leaves nothing else behind.
    archives = list(out_dir.glob("*.tar.gz"))
    assert len(archives) == 1
    assert set(out_dir.iterdir()) - before == {archives[0]}
    check_archive_members(archives[0])


def test_coredump_unpack_no_arg(tt_cmd, tmp_path):
    cmd = [tt_cmd, "coredump", "unpack"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0
    assert re.search(r"tt coredump unpack", output)


def test_coredump_unpack_no_such_file(tt_cmd, tmp_path):
    cmd = [tt_cmd, "coredump", "unpack", "file_that_does_not_exist"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0
    assert re.search(r"failed to unpack", output)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_unpack(tt_cmd, tmp_path, coredump_packed):
    cmd = [tt_cmd, "coredump", "unpack", coredump_packed]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Archive was successfully unpacked", output)


def test_coredump_inspect_no_arg(tt_cmd, tmp_path):
    cmd = [tt_cmd, "coredump", "inspect"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0
    assert re.search(r"tt coredump inspect", output)


def test_coredump_inspect_no_such_file(tt_cmd, tmp_path):
    cmd = [tt_cmd, "coredump", "inspect", "file_that_does_not_exist"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0
    assert re.search(r"failed to inspect", output)


def has_debug_info(tarantool):
    """Whether gdb finds the debugging information of tarantool: the line of
    its main, as gdb.sh asks for it."""
    process = subprocess.run(
        ["gdb", "-batch", "-n", tarantool, "-ex", "info line main"],
        capture_output=True,
        text=True,
    )
    return re.search(r'Line \d+ of ".*/src/main\.cc"', process.stdout) is not None


def gdb_env(env=None):
    """env, the environment of the tests by default, for a tt that runs gdb
    with its Python. actions/setup-python points LD_LIBRARY_PATH at the lib
    of the Python it installs, and gdb, linked against the libpython of the
    system, loads that one instead: it finds no _struct, which the libpython
    of Debian has built in, and the extensions fail on import struct."""
    env = dict(os.environ if env is None else env)
    env.pop("LD_LIBRARY_PATH", None)
    return env


def offline_env(tmp_path):
    """The environment of a tt that cannot reach GitHub, where inspect looks
    up the gdb extensions of the version of a core: every request goes to a
    proxy that refuses the connection. The cache of the user, which may
    hold them, is replaced with an empty one. gdb runs with its own Python
    (gdb_env)."""
    env = gdb_env()
    for name in ("HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"):
        env[name] = "http://127.0.0.1:9"
    for name in ("NO_PROXY", "no_proxy"):
        env.pop(name, None)
    env["XDG_CACHE_HOME"] = (tmp_path / "cache").as_posix()
    return env


def inspect(tt_cmd, tmp_path, target, commands, *args):
    """The exit code and the output, stdout and stderr, of tt coredump
    inspect of target given the gdb commands on its standard input, with no
    GitHub (offline_env)."""
    process = subprocess.run(
        [tt_cmd, "coredump", "inspect", target, *args],
        cwd=tmp_path,
        input="".join(command + "\n" for command in [*commands, "q"]),
        capture_output=True,
        text=True,
        env=offline_env(tmp_path),
    )
    print(process.stdout, process.stderr)
    return process.returncode, process.stdout + process.stderr


def fallback_warning(name):
    """The pattern of the warning of inspect that loads the copy of tt of
    the extension name, GitHub out of reach."""
    return (
        rf"cannot get the {re.escape(name)} of Tarantool [^:]+: cannot reach GitHub: [^;]+; "
        r"using the one of tt, of [^,]+, which may not match"
    )


# The gdb commands that tell the extensions are loaded, help of a command of
# the LuaJIT one and a command of the Tarantool one, which lists the fibers,
# and a backtrace, which needs neither.
EXTENSION_COMMANDS = ["help lj-stack", "info tt-fibers", "bt 1"]

# The warning of inspect of a tarantool gdb finds no debugging information
# for.
NO_DEBUG_INFO_WARNING = (
    r"the tarantool of the core has no debugging information: .*: the lj-\* commands of "
    r"LuaJIT and the pretty-printers of Tarantool will not work, plain backtraces will"
)


def check_extensions(output, debug_info):
    """Check that the gdb extensions answered EXTENSION_COMMANDS when the
    tarantool has debugging information, and that inspect warned of none
    otherwise. A backtrace works either way."""
    assert re.search(r"#0 ", output), output
    if not debug_info:
        assert re.search(NO_DEBUG_INFO_WARNING, output), output
        return

    assert not re.search(NO_DEBUG_INFO_WARNING, output), output
    assert "The command receives a lua_State address" in output
    assert re.search(r'TtFiber\s+"sched"', output), output
    assert "Undefined command" not in output
    assert "has no attribute 'printing'" not in output


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_packed(tt_cmd, tmp_path, coredump_packed):
    """With no sources and no GitHub inspect loads the extensions of tt, and
    warns that they may not match the core."""
    rc, output = inspect(tt_cmd, tmp_path, coredump_packed, EXTENSION_COMMANDS)
    assert rc == 0, output
    assert re.search(fallback_warning("luajit_dbg.py"), output), output
    assert re.search(fallback_warning("tarantool-gdb.py"), output), output
    check_extensions(output, has_debug_info(shutil.which("tarantool")))


def debug_file_member():
    """The tarantool of PATH and the member of its debug file in an archive
    of pack, usr/lib/debug/.build-id/<xx>/<rest>.debug, by its build ID.
    The test is skipped without objcopy, to split the tarantool into a
    stripped one and its debug file, and with a tarantool without
    debugging information of its own."""
    tarantool = shutil.which("tarantool")
    described = subprocess.run(["file", "-L", tarantool], capture_output=True, text=True)
    if shutil.which("objcopy") is None or "with debug_info" not in described.stdout:
        pytest.skip("needs objcopy and a tarantool with debugging information of its own")
    build_id = re.search(r"BuildID\[[^]]*\]=([0-9a-f]+)", described.stdout).group(1)
    return tarantool, f"usr/lib/debug/.build-id/{build_id[:2]}/{build_id[2:]}.debug"


def pack_stripped(tt_cmd, tmp_path, coredump, tarantool):
    """The archive of pack of coredump with tarantool stripped of its
    debugging information by objcopy, in tmp_path/out."""
    stripped = tmp_path / "bin" / "tarantool"
    stripped.parent.mkdir()
    subprocess.run(["objcopy", "--strip-debug", tarantool, stripped], check=True)

    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-e", stripped, "-d", out, coredump]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output

    archives = list(out.glob("*.tar.gz"))
    assert len(archives) == 1
    return archives[0]


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_pack_debug_file(tt_cmd, tmp_path, coredump):
    """pack of a core of a tarantool without debugging information takes the
    debug file of its build ID from /usr/lib/debug, where a debuginfo
    package installs it, into usr/lib/debug of the archive, and inspect
    points gdb to it there. The tarantool is the one of PATH stripped by
    objcopy, and the debug file is one the host has for it: a test cannot
    write to /usr/lib/debug, and is skipped on a host without the file.
    The unit tests of the probe pack the debug file of a temporary debug
    directory."""
    tarantool, member = debug_file_member()
    if not os.path.isfile("/" + member):
        pytest.skip(f"the host has no debug file of the tarantool of PATH, /{member}")

    archive = pack_stripped(tt_cmd, tmp_path, coredump, tarantool)
    assert member in check_archive_members(archive)

    rc, output = inspect(tt_cmd, tmp_path, archive, EXTENSION_COMMANDS)
    assert rc == 0, output
    check_extensions(output, True)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_debug_file(tt_cmd, tmp_path, coredump):
    """inspect of a core of a tarantool without debugging information points
    gdb to the debug file of its build ID in usr/lib/debug of the archive,
    where pack puts it, and warns before gdb starts when the archive has
    none. The tarantool and its debug file are those of the tarantool of
    PATH, split by objcopy, and the test puts the debug file into the
    unpacked archive."""
    tarantool, member = debug_file_member()
    archive = pack_stripped(tt_cmd, tmp_path, coredump, tarantool)

    # An archive without the debug file, packed where the host has none.
    if member not in check_archive_members(archive):
        rc, output = inspect(tt_cmd, tmp_path, archive, EXTENSION_COMMANDS)
        assert rc == 0, output
        check_extensions(output, False)

    cmd = [tt_cmd, "coredump", "unpack", archive]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    unpacked = tmp_path / archive.name.split(".")[0]
    debug_file = unpacked / member
    debug_file.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["objcopy", "--only-keep-debug", tarantool, debug_file], check=True)

    rc, output = inspect(tt_cmd, tmp_path, unpacked, EXTENSION_COMMANDS)
    assert rc == 0, output
    check_extensions(output, True)


# The lines each extension of the fake sources starts with, which gdb prints
# when it sources the extension, before what may fail to load without the
# debugging information of the tarantool.
SOURCES_MARK = "import gdb as tt_test_gdb\ntt_test_gdb.write('tt-test: {} of the sources\\n')\n"


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_sourcedir(tt_cmd, tmp_path, coredump_unpacked):
    """inspect -s loads the extensions of the sources: luajit_dbg.py of the
    LuaJIT submodule and tools/tarantool-gdb.py, here the copies of tt with
    a mark, and asks GitHub nothing."""
    sources = tmp_path / "tarantool"
    luajit = sources / "third_party" / "luajit" / "src"
    tools = sources / "tools"
    luajit.mkdir(parents=True)
    tools.mkdir()
    for directory, name in ((luajit, "luajit_dbg.py"), (tools, "tarantool-gdb.py")):
        content = (EXTENSIONS_DIR / name).read_text()
        (directory / name).write_text(SOURCES_MARK.format(name) + content)
    # An older LuaJIT extension the newer one is preferred to.
    (luajit / "luajit-gdb.py").write_text("raise Exception('luajit-gdb.py is loaded')\n")

    rc, output = inspect(
        tt_cmd,
        tmp_path,
        coredump_unpacked,
        EXTENSION_COMMANDS,
        "-s",
        sources.as_posix(),
    )
    assert rc == 0, output
    assert "tt-test: luajit_dbg.py of the sources" in output
    assert "tt-test: tarantool-gdb.py of the sources" in output
    assert "luajit-gdb.py is loaded" not in output
    assert "on GitHub" not in output
    assert "cannot get the" not in output
    check_extensions(output, has_debug_info(shutil.which("tarantool")))


# The gdb commands of the universal extensions of tarantool-gdb-tools: lj-bt,
# the backtrace of C and Lua frames in one that only they have, the fibers and
# the Lua stack.
UNIVERSAL_COMMANDS = ["lj-bt", "info tt-fibers", "lj-stack"]

# The manifest of the universal extensions of tarantool-gdb-tools tt embeds,
# which names the versions of Tarantool and the architectures each of them was
# checked on.
GDB_TOOLS_MANIFEST = EXTENSIONS_DIR / "gdb-tools" / "extensions.json"

# The warning of inspect --experimental of an extension the core is out of
# the range of: what it was checked on, and what the core is of.
UNCHECKED_WARNING = r"[\w.-]+\.py is checked on [^\n]*?, the core is of [^\n]*?: it may not work"


def gdb_tools_extensions():
    """The entries of the manifest of the extensions of tarantool-gdb-tools tt
    embeds."""
    return json.loads(GDB_TOOLS_MANIFEST.read_text())["extensions"]


def minor_of(release):
    """The minor of a release x.y.z, (x, y)."""
    major, minor, _ = release.split(".")
    return int(major), int(minor)


def join_and(names):
    """names as a sentence lists them, as the warnings of inspect do."""
    return names[0] if len(names) == 1 else ", ".join(names[:-1]) + " and " + names[-1]


def unchecked_warnings(version_line):
    """The warnings of inspect --experimental of a core of this host of the
    Tarantool of version_line, the first line of its version file: one for
    each extension of the manifest whose range the minor of the version is out
    of, as the manifest means a range, every patch of every minor from that of
    min to that of max, or whose architectures that of the host is none of."""
    match = re.match(r"Tarantool (?:Enterprise )?((\d+\.\d+\.\d+)(?:-[a-z]+\d*)?)", version_line)
    assert match, version_line
    tag, release = match.groups()
    machine = platform.machine()
    warnings = set()
    for entry in gdb_tools_extensions():
        checked, core = [], []
        first = minor_of(entry["tarantool"]["min"])
        last = minor_of(entry["tarantool"]["max"])
        if not first <= minor_of(release) <= last:
            minors = "{}.{}".format(*first)
            if first != last:
                minors += " to {}.{}".format(*last)
            checked.append("Tarantool " + minors)
            core.append(tag)
        if machine not in entry["arch"]:
            checked.append(join_and(entry["arch"]))
            core.append(machine)
        if checked:
            warnings.add(
                f"{entry['file']} is checked on {' on '.join(checked)}, "
                f"the core is of {' on '.join(core)}: it may not work",
            )
    return warnings


def archive_version(archive):
    """The first line of the version file of an archive of tt coredump pack,
    the version of the Tarantool of the core."""
    name = os.path.basename(archive).split(".")[0]
    with tarfile.open(archive) as tar:
        return tar.extractfile(f"{name}/version").read().decode().splitlines()[0]


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_experimental(tt_cmd, tmp_path, coredump_packed):
    """inspect --experimental loads the extensions of tarantool-gdb-tools tt
    embeds, lj-bt among them, rather than those of the Tarantool of the core,
    and asks GitHub nothing: lj-bt puts the Lua frame of the chunk that crashed
    among the C frames, info tt-fibers lists the fibers and lj-stack the Lua
    stack. inspect warns of those whose range of their manifest the Tarantool
    of the core is out of, by its minor, and of no other: none while that of
    the tests is in all of them."""
    if not has_debug_info(shutil.which("tarantool")):
        pytest.skip("the extensions need the debugging information of the tarantool")

    rc, output = inspect(tt_cmd, tmp_path, coredump_packed, UNIVERSAL_COMMANDS, "--experimental")
    assert rc == 0, output
    assert re.search(
        r"gdb extensions of tarantool-gdb-tools [0-9a-f]{8}, which tt embeds: luajit_gdb\.py, "
        r"tarantool_gdb\.py, luajit_backtrace_gdb\.py, gcore_pages_gdb\.py",
        output,
    ), output
    assert "on GitHub" not in output
    assert "cannot get the" not in output
    expected = unchecked_warnings(archive_version(coredump_packed))
    assert set(re.findall(UNCHECKED_WARNING, output)) == expected, output
    # The chunk of tarantool -e that crashed, under the C frames of the FFI
    # store, between the C frames of the VM entry and lua_pcall.
    assert re.search(r"lj-bt: L=0x[0-9a-f]+", output), output
    assert re.search(r"#\d+\.\d+\s+\[lua\] \(command line\):1 in main chunk", output), output
    assert re.search(r'TtFiber\s+"sched"', output), output
    lua_frame = r'FRAME: \[\w+\] delta=\d+, Lua function .*"=\(command line\)"'
    assert re.search(lua_frame, output), output
    assert "FRAME: dummy L" in output, output
    assert "Undefined command" not in output
    assert "Python Exception" not in output


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_experimental_unchecked(tt_cmd, tmp_path, coredump_unpacked):
    """inspect --experimental warns of each extension whose range the minor of
    the Tarantool of the core is out of, loading it all the same, and of no
    other. The artefacts are those of the core, linked to, but for a version
    file of the minor before the first one of the range that begins last:
    that range leaves it out, and one that begins before it has it."""
    first = max(minor_of(entry["tarantool"]["min"]) for entry in gdb_tools_extensions())
    major, minor = first
    version_line = (
        f"Tarantool {major}.{minor - 1}.0-0-g0123456"
        if minor > 0
        else f"Tarantool {major - 1}.0.0-0-g0123456"
    )
    expected = unchecked_warnings(version_line)
    assert expected

    artefacts = tmp_path / "artefacts"
    artefacts.mkdir()
    for path in Path(coredump_unpacked).iterdir():
        if path.name != "version":
            (artefacts / path.name).symlink_to(path)
    (artefacts / "version").write_text(version_line + "\n")

    rc, output = inspect(tt_cmd, tmp_path, artefacts.as_posix(), [], "--experimental")
    assert rc == 0, output
    assert set(re.findall(UNCHECKED_WARNING, output)) == expected, output
