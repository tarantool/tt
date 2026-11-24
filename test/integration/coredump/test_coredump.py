import contextlib
import ctypes
import fcntl
import filecmp
import glob
import os
import platform
import re
import select
import shutil
import stat
import struct
import subprocess
import sys
import tarfile
import termios
import time
from pathlib import Path

import pytest
from coredump_helpers import (
    CRASH,
    EXTENSIONS_DIR,
    checked_minors,
    elf_type,
    gdb_env,
    gdb_tools_extensions,
    has_debug_info,
    kernel_core,
    minor_of,
    offline_env,
    release_of,
    skip_coredump_cond,
    skip_coredump_reason,
)

from utils import run_command_and_get_output

# spell-checker:ignore prctl PTRACER Yama


def generate_coredump(tmp_path_factory, tarantool_bin="tarantool", lua=CRASH) -> Path:
    """The core the kernel dumps of tarantool_bin that crashed running lua,
    in a directory of its own (see kernel_core), where core_pattern puts it
    there. The tests of such cores are run explicitly, with
    TT_ENABLE_COREDUMP_TESTS: a core may be left behind."""
    core, _ = kernel_core(tmp_path_factory.mktemp("coredump"), [tarantool_bin, "-e", lua])
    return core


@pytest.fixture(scope="session")
def coredump(tmp_path_factory) -> Path:
    return generate_coredump(tmp_path_factory)


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


# The Lua that crashes a tarantool once a request of the file system has
# started a thread of the pool of coio: the core has a thread besides the
# main one, on a stack pthread_create made. tarantool -e alone has none.
CRASH_WITH_THREAD = "require('fio').stat('/') " + CRASH


@pytest.fixture(scope="session")
def gcore_with_thread(tmp_path_factory) -> Path:
    return gcore_of_crash(tmp_path_factory, "gcore_with_thread", CRASH_WITH_THREAD)


@pytest.fixture(scope="session")
def coredump_with_thread(tmp_path_factory) -> Path:
    return generate_coredump(tmp_path_factory, lua=CRASH_WITH_THREAD)


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
    """pack of a kernel core says where the archive is with no terminal on
    its standard output, as from a script, where the pack script tells it to
    a terminal alone and pack said nothing of it. The version of the archive
    is the first line of tarantool --version, which pack reads from the
    file of the tarantool rather than runs it, and the line alone. The
    archive, the memory of a process, is its user's alone, whatever the
    umask."""
    rc, output = coredump_pack
    assert rc == 0, output
    assert "nothing but zeros" not in output, output
    assert "cannot read the version" not in output, output
    archives = list(coredump.parent.glob("*.tar.gz"))
    assert len(archives) == 1
    assert f"Core was successfully packed: {archives[0]}" in output, output
    assert "The resulting archive is located here" not in output, output
    check_archive_members(archives[0])
    assert stat.S_IMODE(archives[0].stat().st_mode) == 0o600

    shown = subprocess.run(
        [shutil.which("tarantool"), "--version"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    name = os.path.basename(archives[0]).split(".")[0]
    with tarfile.open(archives[0]) as tar:
        version = tar.extractfile(f"{name}/version").read().decode()
    assert version == shown.splitlines()[0] + "\n"


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


def write_other_build(tarantool, other, note):
    """Write a copy of tarantool to other with the last byte of its build ID
    changed, through note, a file for the section: a tarantool of the layout
    of tarantool, and of another build. The test skips without objcopy, or
    when tarantool has no build ID."""
    if shutil.which("objcopy") is None:
        pytest.skip("needs objcopy")
    only = ["objcopy", "-O", "binary", "--only-section=.note.gnu.build-id"]
    subprocess.run([*only, tarantool, note], check=True)
    data = bytearray(note.read_bytes())
    if not data:
        pytest.skip(f"{tarantool} has no build ID")
    data[-1] ^= 0xFF
    note.write_bytes(data)
    other.parent.mkdir(parents=True, exist_ok=True)
    update = ["objcopy", f"--update-section=.note.gnu.build-id={note}"]
    subprocess.run([*update, tarantool, other], check=True)


def test_coredump_pack_other_tarantool(tt_cmd, tmp_path, gcore):
    """pack refuses a core with a tarantool of another build ID than the
    executable of the core: here a copy of the one that dumped it with the
    last byte of its build ID changed. The copy has the layout of the
    original, and gdb, which only warns of it, found the shared libraries of
    the core with it: the check of pack was that gdb found some."""
    note = tmp_path / "build-id"
    other = tmp_path / "bin" / "tarantool"
    write_other_build(shutil.which("tarantool"), other, note)

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


def link_pack_tools(bin_dir):
    """Make bin_dir a directory of links to the programs of the pack script
    alone, PACK_TOOLS, for the PATH of a tt that is to find neither gdb nor
    a tarantool there. The test skips without one of them."""
    bin_dir.mkdir()
    for tool in PACK_TOOLS:
        path = shutil.which(tool)
        if path is None:
            pytest.skip(f"no {tool} on PATH")
        os.symlink(path, bin_dir / tool)


def test_coredump_pack_without_gdb(tt_cmd, tmp_path, gcore):
    """pack needs no debugger, so that a host with lldb or with none packs a
    core: with a PATH of the programs of the pack script alone, without gdb
    and file(1), it packs the core with its shared libraries, and inspect,
    with gdb, shows its backtrace. pack refused a host without gdb, which it
    asked for the shared libraries of the core."""
    bin_dir = tmp_path / "bin"
    link_pack_tools(bin_dir)
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


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
@pytest.mark.parametrize("on_path", ["none", "other build"])
def test_coredump_pack_named_tarantool(tt_cmd, tmp_path, coredump_alt, alt_tarantool, on_path):
    """pack given no -e packs a core with the executable the core names: a
    tarantool that is not in PATH, as that of bin/ of a project or of tt
    binary install which an instance ran. PATH has the programs of the pack
    script and no tarantool, or a tarantool of another build, a copy of the
    one that dumped the core with its build ID changed: pack took the
    tarantool of PATH alone, and found none, or refused the core. The
    archive has the tarantool, and inspect shows the backtrace of the core."""
    bin_dir = tmp_path / "bin"
    link_pack_tools(bin_dir)
    if on_path == "other build":
        write_other_build(alt_tarantool, bin_dir / "tarantool", tmp_path / "build-id")
    env = dict(os.environ, PATH=bin_dir.as_posix())

    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-d", out, coredump_alt]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0, output
    assert f"packing with {alt_tarantool}, the executable the core names" in output, output
    assert "not packing with" not in output, output
    check_tarantool_in_archive(out, alt_tarantool)

    if on_path == "none" and shutil.which("gdb") is not None:
        rc, output = inspect(tt_cmd, tmp_path, next(out.glob("*.tar.gz")), ["bt 1"])
        assert rc == 0, output
        assert re.search(r"#0 ", output), output


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_pack_path_tarantool(tt_cmd, tmp_path):
    """pack given no -e packs a core whose executable the core names is gone
    with the tarantool of PATH of the build ID of the core, and says why:
    the core is of a copy of that tarantool, removed since it dumped the
    core. With a PATH of a tarantool of another build instead, pack fails,
    and lists the tarantools it tried, with why each is not the one, and
    the path the core records, for -e."""
    tarantool = shutil.which("tarantool")
    assert tarantool is not None, "no tarantool on PATH"
    copy = tmp_path / "gone" / "tarantool"
    copy.parent.mkdir()
    shutil.copy(tarantool, copy)
    work = tmp_path / "work"
    work.mkdir()
    core, _ = kernel_core(work, [copy.as_posix(), "-e", CRASH])
    copy.unlink()

    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-d", out, core]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    assert f"not packing with {copy}, the executable the core names: missing" in output, output
    assert re.search(
        rf"packing with {re.escape(tarantool)}, the tarantool of PATH: it has the build ID of the "
        r"core, [0-9a-f]+",
        output,
    ), output
    check_tarantool_in_archive(out, tarantool)

    # The scopes tt binary installs into, which pack tries after PATH, are
    # empty ones: those of the host may have the tarantool of the core.
    bin_dir = tmp_path / "bin"
    link_pack_tools(bin_dir)
    other = bin_dir / "tarantool"
    write_other_build(tarantool, other, tmp_path / "build-id")
    env = dict(os.environ, PATH=bin_dir.as_posix())
    for name in ("TT_USER_ROOT", "TT_SYSTEM_ROOT"):
        root = tmp_path / name.lower()
        root.mkdir()
        env[name] = root.as_posix()
    failed = tmp_path / "failed"
    failed.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-d", failed, core]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc != 0, output
    assert (
        "core dump file is produced by the different Tarantool executable: none of the "
        f"tarantools tried dumped '{core}':" in output
    ), output
    assert f"{copy}, the executable the core names: missing" in output, output
    assert re.search(
        rf"{re.escape(str(other))}, the tarantool of PATH: the executable of the core has build "
        r"ID [0-9a-f]+, the binary [0-9a-f]+",
        output,
    ), output
    assert (
        f"specify the tarantool that dumped the core with -e <path>; the core records it as {copy}"
        in output
    ), output
    assert not list(failed.iterdir())


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

        # The archive, named by the PID, and the core, stored as the pack
        # script stores it.
        archives = list(out.glob("*.tar.gz"))
        assert len(archives) == 1, list(out.iterdir())
        assert f"Core was successfully packed: {archives[0]}" in output, output
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


def test_coredump_pack_archive_name(tt_cmd, tmp_path):
    """pack of a core given as a file, without -p, names the archive by the
    PID of the process of the core, which its NT_PRPSINFO note has, where it
    named it tarantool-core-N-...; -p names it as before. The core is a
    gcore of a running tarantool."""
    if sys.platform != "linux" or shutil.which("gdb") is None:
        pytest.skip("needs gdb on Linux to make a core of a running tarantool")
    tarantool = shutil.which("tarantool")
    assert tarantool is not None, "no tarantool on PATH"
    core = tmp_path / "gcore"

    with running([tarantool, "-e", SLEEP], tmp_path) as process:
        assert process.stdout.readline() == b"ready\n"
        gcore_of_running(process, core)
        pid = process.pid

    for arg, name in (([], pid), (["-p", "42"], 42)):
        out = tmp_path / f"out-{name}"
        out.mkdir()
        cmd = [tt_cmd, "coredump", "pack", "-d", out, *arg, core]
        rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 0, output
        archives = list(out.glob("*.tar.gz"))
        assert len(archives) == 1, list(out.iterdir())
        assert archives[0].name.startswith(f"tarantool-core-{name}-"), archives[0]
        assert f"Core was successfully packed: {archives[0]}" in output, output


def test_coredump_pack_taken_name(tt_cmd, tmp_path, gcore):
    """pack of a core into the name of an archive there already, as of the
    same core packed again in the minute of the name, keeps that archive and
    names the new one with -2, then -3, before .tar.gz, its directory alike,
    and says so. The pack replaced the archive without a word."""
    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-d", out, "-p", "42", "-t", "1763869440", gcore]
    archives = []
    for suffix in ("", "-2", "-3"):
        rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 0, output
        packed = sorted(set(out.glob("*.tar.gz")) - set(archives))
        assert len(packed) == 1, (list(out.iterdir()), output)
        archive = packed[0]
        archives.append(archive)
        if suffix:
            first = archives[0]
            assert archive.name == first.name.removesuffix(".tar.gz") + suffix + ".tar.gz"
            assert (
                f"Core was successfully packed: {archive}, since {first.name} is there already"
                in output
            ), output
        else:
            assert re.search(rf"Core was successfully packed: {re.escape(str(archive))}\s", output)
            first_content = archive.read_bytes()
        check_archive_members(archive)

    assert archives[0].read_bytes() == first_content
    assert set(out.iterdir()) == set(archives)


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
    assert "Core was successfully packed" not in output


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


def disk_size(path):
    """The bytes the file at path takes on the disk."""
    return os.stat(path).st_blocks * 512


# The blocks unpack writes a file in, sparseBlock of files.go: a block of
# zeros is skipped, a hole of the file, and any other one written.
SPARSE_BLOCK = 4 << 10


def data_blocks(path, block):
    """The blocks of block bytes of the file at path that are not zeros, and
    the runs of them in a row. The file is read a MB and more at a time, and
    a chunk of zeros is skipped at once, the holes of a core of more than a
    GB too: the blocks of a chunk are compared one by one only where the
    chunk has data."""
    zeros = bytes(block)
    zero_chunk = bytes(256 * block)
    data = runs = 0
    previous = False
    with open(path, "rb") as f:
        while chunk := f.read(len(zero_chunk)):
            if chunk == zero_chunk[: len(chunk)]:
                previous = False
                continue
            for start in range(0, len(chunk), block):
                piece = chunk[start : start + block]
                current = piece != zeros[: len(piece)]
                data += current
                runs += current and not previous
                previous = current
    return data, runs


def keeps_holes(directory):
    """Whether the file system of directory keeps holes: a file of 16 MiB of
    one byte at its end takes far less of the disk there."""
    probe = directory / "holes"
    with probe.open("wb") as f:
        f.seek((16 << 20) - 1)
        f.write(b"\1")
    kept = disk_size(probe) < 1 << 20
    probe.unlink()
    return kept


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_unpack(tt_cmd, tmp_path, coredump, coredump_packed):
    """unpack extracts the core with its holes, and inspect of what it
    unpacked opens the core. A core has the pages of its mappings the
    process never touched as zeros, which pack archives as holes, and unpack
    wrote them out as zeros: a core of a bare tarantool took its size on the
    disk, more than a GB, of tens of MB of data.

    unpack skips every block of zeros of SPARSE_BLOCK, so the core takes on
    the disk its blocks with data, counted in the blocks of the file system
    where those are bigger, as it allocates a block with any data whole, and
    the blocks the file system maps them with. It maps a run of blocks in a
    row with an extent, or a few, and ext4 keeps 340 extents in a block of 4
    KiB: a block for each run is room enough. The core must have more zeros
    than that room, or the check would pass with the zeros written. How many
    it has depends on the tarantool: a core of 3.9 on x86-64 is mostly
    holes, one of 3.3, 8 MB of data of 18 MB, is not, and the check was that
    the core took less than a quarter of its size, which the core of 3.3
    failed with every hole kept. A file system that keeps no holes fails the
    test on Linux, whose file systems keep them, ext4, XFS, Btrfs and tmpfs,
    so that the check is not dropped there unseen, and skips it elsewhere.

    What unpack extracts is its user's alone: the core and the rest, the
    tarantool of the entry 0755 too, and the directories."""
    if not keeps_holes(tmp_path):
        message = f"the file system of {tmp_path} keeps no holes"
        if sys.platform == "linux":
            pytest.fail(f"{message}, where those of Linux keep them: the check runs on Linux")
        pytest.skip(message)
    cmd = [tt_cmd, "coredump", "unpack", coredump_packed]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Archive was successfully unpacked", output)

    core = tmp_path / os.path.basename(coredump_packed).split(".")[0] / "coredump"
    size = core.stat().st_size
    assert size == coredump.stat().st_size
    block = max(SPARSE_BLOCK, os.statvfs(core).f_frsize)
    data, runs = data_blocks(core, block)
    room = runs * block
    assert size - data * block > room, (size, data * block, room)
    assert disk_size(core) <= data * block + room, (disk_size(core), data * block, room)
    assert stat.S_IMODE(core.stat().st_mode) == 0o600
    assert stat.S_IMODE((core.parent / "tarantool").stat().st_mode) == 0o700
    assert stat.S_IMODE(core.parent.stat().st_mode) == 0o700

    rc, output = inspect(tt_cmd, tmp_path, core.parent.as_posix(), EXTENSION_COMMANDS)
    assert rc == 0, output
    check_extensions(output, has_debug_info(shutil.which("tarantool")))


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


def test_coredump_inspect_without_gdb(tt_cmd, tmp_path):
    """inspect on a host without gdb in PATH fails with an error of tt that
    says so and what to do, before gdb.sh runs: the wrapper said it on its
    standard output, and tt failed with "inspect script execution failed".
    PATH has what gdb.sh runs before it looks gdb up, and no gdb."""
    if sys.platform != "linux":
        pytest.skip("gdb.sh needs GNU getopt")
    artefacts = tmp_path / "artefacts"
    artefacts.mkdir()
    (artefacts / "version").write_text("Tarantool 3.9.0-0-g8b9a0f59a95b\n")
    (artefacts / "tarantool").write_text("ELF")
    (artefacts / "coredump").write_text("ELF")
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    for tool in ("sh", "basename", "dirname", "cat", "getopt"):
        (bin_dir / tool).symlink_to(shutil.which(tool))
    env = offline_env(tmp_path)
    env["PATH"] = bin_dir.as_posix()

    process = subprocess.run(
        [os.path.abspath(tt_cmd), "coredump", "inspect", artefacts],
        cwd=tmp_path,
        capture_output=True,
        text=True,
        env=env,
    )
    output = process.stdout + process.stderr
    assert process.returncode != 0, output
    assert (
        "gdb is not found in PATH: install gdb, or put the directory of the gdb to use first in "
        "PATH" in output
    ), output
    assert "gdb is not installed" not in output, output


def inspect(tt_cmd, tmp_path, target, commands, *args, tt_args=()):
    """The exit code and the output, stdout and stderr, of tt coredump
    inspect of target given the gdb commands on its standard input, with no
    GitHub (offline_env). tt_args are the global options of tt, which go
    before the command."""
    process = subprocess.run(
        [tt_cmd, *tt_args, "coredump", "inspect", target, *args],
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

# The hint after the warning of a tarantool gdb finds no debugging
# information for: the tarantool of the static deb of the release of the
# core, of a static build of Tarantool Community, or the debug file of the
# host the core is packed on.
NO_DEBUG_INFO_HINT = (
    r"hint: (?:the tarantool of the static deb of Tarantool \d+\.\d+\.\d+, which `tt binary "
    r"install tarantool \d+\.\d+\.\d+` installs, has the debugging information: if its build "
    r"ID is that of the core, [0-9a-f]+, |pack the core again where the debug file of its "
    r"tarantool is installed, under /usr/lib/debug)"
)


def static_deb_release(tarantool):
    """The release of the static deb tt names in the hint of a core of
    tarantool without debugging information, or None where it names none: of
    what may be a static build of Tarantool Community, an executable of type
    ET_EXEC of a release of series 3 at its tag, with nothing after the
    commit."""
    shown = subprocess.run([tarantool, "--version"], capture_output=True, text=True).stdout
    line = shown.splitlines()[0] if shown else ""
    match = re.fullmatch(r"Tarantool (3\.\d+\.\d+)-0-g[0-9a-f]{7,40}", line)
    return match.group(1) if match and elf_type(tarantool) == 2 else None


def static_deb_hint(release, action):
    """The pattern of the hint of the tarantool of the static deb of release,
    with its action, what to do with it."""
    return (
        rf"hint: the tarantool of the static deb of Tarantool {re.escape(release)}, which `tt "
        rf"binary install tarantool {re.escape(release)}` installs, has the debugging "
        rf"information: if its build ID is that of the core, [0-9a-f]+, {re.escape(action)}"
    )


def inspect_action(artefacts):
    """What the hint of inspect of the artefacts in the directory artefacts
    says to do with the tarantool of the static deb."""
    return (
        f"copy it over {artefacts}/tarantool, or to the debug file named above under "
        f"{artefacts}, and run `tt coredump inspect {artefacts}`"
    )


def check_extensions(output, debug_info):
    """Check that the gdb extensions answered EXTENSION_COMMANDS when the
    tarantool has debugging information, and that inspect warned of none
    otherwise, with a hint. A backtrace works either way."""
    assert re.search(r"#0 ", output), output
    if not debug_info:
        assert re.search(NO_DEBUG_INFO_WARNING, output), output
        assert re.search(NO_DEBUG_INFO_HINT, output), output
        return

    assert not re.search(NO_DEBUG_INFO_WARNING, output), output
    assert "The command receives a lua_State address" in output
    assert re.search(r'TtFiber\s+"sched"', output), output
    assert "Undefined command" not in output
    assert "has no attribute 'printing'" not in output


# What gdb.sh says of the sources of the Tarantool of a core it has none of:
# the commands that check them out at the commit of the core into
# tarantool-<commit>, a directory of the working directory, and the -s that
# gives it. It said to clone them into sources/ of the artefacts, of an
# archive a temporary directory inspect removes.
SOURCES_HINT = re.compile(
    r"gdb shows no lines of the C code of Tarantool without its sources\. Clone them\s+"
    r"once, at the commit of the core:\s+"
    r"git clone https://github\.com/tarantool/tarantool\.git (tarantool-[0-9a-f.]+)\s+"
    r"git -C \1 checkout [0-9a-f.]+\s+"
    r"git -C \1 submodule update --init --recursive\s+"
    r"and give them with -s \1\.",
)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_packed(tt_cmd, tmp_path, coredump_packed):
    """With no sources and no GitHub inspect loads the extensions of tt, and
    warns that they may not match the core. gdb.sh says how to get the
    sources."""
    rc, output = inspect(tt_cmd, tmp_path, coredump_packed, EXTENSION_COMMANDS)
    assert rc == 0, output
    assert re.search(fallback_warning("luajit_dbg.py"), output), output
    assert re.search(fallback_warning("tarantool-gdb.py"), output), output
    assert SOURCES_HINT.search(output), output
    check_extensions(output, has_debug_info(shutil.which("tarantool")))


def test_coredump_inspect_renamed_archive(tt_cmd, tmp_path, gcore):
    """inspect of an archive renamed from the name pack gave it opens the
    artefacts of the directory it holds, which keeps that name, and the
    checks before gdb read them, as -V shows. inspect took the directory of
    the new name, where gdb.sh found no version file and failed, and every
    check read nothing."""
    tarantool = shutil.which("tarantool")
    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-e", tarantool, "-d", out, gcore]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    archives = list(out.glob("*.tar.gz"))
    assert len(archives) == 1
    renamed = archives[0].rename(tmp_path / "core.tar.gz")

    rc, output = inspect(tt_cmd, tmp_path, renamed.as_posix(), EXTENSION_COMMANDS, tt_args=["-V"])
    assert rc == 0, output
    assert "version file is missing" not in output, output
    assert "cannot tell whether the tarantool has debugging information" not in output, output
    assert re.search(r"read the stacks of \d+ of the \d+ threads of the core", output), output
    check_extensions(output, has_debug_info(tarantool))


# What gdb says of a file the core maps that it cannot open under its sysroot,
# the artefacts.
CANNOT_OPEN = re.compile(r"Can't open file (.+) during file-backed mapping note processing")

# What inspect says before gdb starts of the files the core maps that are not
# in the archive and hold no code: how many, but for one, and the first two.
MAPPED_NOTE = re.compile(
    r"gdb will warn of (?:a file|(\d+) files) the core maps that (?:is|are) not in the "
    r"archive, (.+?)(?: and \d+ more)?: (?:it holds|they hold) no code gdb needs",
)


def check_mapped_files(output, tarantool):
    """Check that gdb opened the files the core maps of the archive, the
    tarantool, at the path it ran from, and the dynamic loader, at its real
    path, among them, and that inspect said before gdb started how many it
    would not open, naming some of them. gdb opens them in no given order."""
    missing = CANNOT_OPEN.findall(output)
    assert tarantool not in missing, output
    assert not [path for path in missing if "/ld-linux" in path], output

    note = MAPPED_NOTE.search(output)
    if not missing:
        assert note is None, output
        return

    assert note is not None, output
    assert int(note.group(1) or 1) == len(missing), output
    named = re.split(r", | and ", note.group(2))
    assert named and set(named) <= set(missing), (named, missing)


def test_coredump_inspect_mapped_files(tt_cmd, tmp_path, gcore):
    """gdb opens under its sysroot, the artefacts, each file the core maps, for
    the pages of it the core does not hold, and warned "Can't open file ...
    during file-backed mapping note processing" of the tarantool at the path it
    ran from and of the dynamic loader at its real path, which the archive
    holds by other names, as of the locale archive, which it does not hold.
    inspect gives the files of the archive those paths, and says of the others
    before gdb starts: of an archive, and of the directory tt coredump unpack
    unpacks it into, where the links stay for the next inspect."""
    tarantool = os.path.realpath(shutil.which("tarantool"))
    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-d", out, gcore]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    (archive,) = out.glob("*.tar.gz")

    rc, output = inspect(tt_cmd, tmp_path, archive.as_posix(), ["bt 1"])
    assert rc == 0, output
    check_mapped_files(output, tarantool)

    cmd = [tt_cmd, "coredump", "unpack", archive]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    unpacked = tmp_path / archive.name.removesuffix(".tar.gz")

    for _ in range(2):
        rc, output = inspect(tt_cmd, tmp_path, unpacked.as_posix(), ["bt 1"])
        assert rc == 0, output
        check_mapped_files(output, tarantool)

    assert os.path.samefile(unpacked / tarantool.lstrip("/"), unpacked / "tarantool")


# What gdb prints when its output runs past a screen of the terminal, and
# waits for the answer.
PAGER_PROMPT = b"--Type <RET> for more, q to quit, c to continue without paging--"


def on_terminal(cmd, cwd, env, commands, rows=20, cols=80, timeout=120):
    """The output of cmd, a tt that runs gdb, on a terminal of rows and cols,
    a pty, and the pagers of gdb there, each True when it came after the
    first prompt of gdb. gdb is given the commands, one at each of its
    prompts, then quit. A pager is answered c, to go on, before the prompt,
    and q at a command."""
    master, slave = os.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    def controlling_terminal():
        os.setsid()
        fcntl.ioctl(0, termios.TIOCSCTTY, 0)

    process = subprocess.Popen(
        cmd,
        cwd=cwd,
        env=env,
        stdin=slave,
        stdout=slave,
        stderr=slave,
        preexec_fn=controlling_terminal,
    )
    os.close(slave)

    pending = [*commands, "quit"]
    output, answered, prompted, pagers = b"", 0, False, []
    deadline = time.monotonic() + timeout
    try:
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.5)
            if ready:
                try:
                    data = os.read(master, 65536)
                except OSError:  # EIO: the other end is closed.
                    break
                if not data:
                    break
                output += data
            unanswered = output[answered:]
            if PAGER_PROMPT in unanswered:
                pagers.append(prompted)
                answered = len(output)
                os.write(master, b"q\n" if prompted else b"c\n")
            elif unanswered.endswith(b"(gdb) "):
                prompted, answered = True, len(output)
                if pending:
                    os.write(master, pending.pop(0).encode() + b"\n")
            elif not ready and process.poll() is not None:
                break
    finally:
        if process.poll() is None:
            process.kill()
        process.wait()
        os.close(master)
    text = output.decode(errors="replace")
    print(text)
    return text, pagers


def terminal_env(tmp_path):
    """The environment of tt coredump inspect on a terminal, as offline_env:
    gdb takes the size of the terminal from the pty, and styles nothing on a
    dumb one, so that its prompt is plain."""
    env = offline_env(tmp_path)
    env["TERM"] = "dumb"
    for name in ("LINES", "COLUMNS", "EMACS", "INSIDE_EMACS"):
        env.pop(name, None)
    return env


def test_coredump_inspect_pagination(tt_cmd, tmp_path, gcore):
    """gdb does not page its start on a terminal and pages the commands at its
    prompt, as it does by default: the loading of the core and the lines the
    extensions print ran past a screen, and gdb stopped at its pager before
    its first prompt, where q quit the loading. The pagination off in the
    gdbinit of the user stays off."""
    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "coredump", "pack", "-e", shutil.which("tarantool"), "-d", out, gcore]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    archives = list(out.glob("*.tar.gz"))
    assert len(archives) == 1
    inspect_cmd = [tt_cmd, "coredump", "inspect", archives[0]]

    env = terminal_env(tmp_path / "default")
    output, pagers = on_terminal(inspect_cmd, tmp_path, env, ["show pagination", "help all"])
    assert "(gdb) " in output, output
    assert False not in pagers, "gdb paged its start"
    assert "State of pagination is on." in output, output
    assert True in pagers, "gdb did not page help all"

    env = terminal_env(tmp_path / "user")
    (Path(env["HOME"]) / ".gdbinit").write_text("set pagination off\n")
    output, pagers = on_terminal(inspect_cmd, tmp_path, env, ["show pagination", "help all"])
    assert "State of pagination is off." in output, output
    assert not pagers, "gdb paged with the pagination off"


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
    debugging information by objcopy, in tmp_path/out, and the output of
    pack."""
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
    return archives[0], output


# The warning of pack of a tarantool with no debugging information, of its
# own or of a debug file of its build ID on the host, and the hint of pack of
# any tarantool but a static build of Community.
PACK_NO_DEBUG_INFO_WARNING = (
    r"the tarantool \S+ has no debugging information, and no debug file of its build ID "
    r"[0-9a-f]+ is under /usr/lib/debug: the archive will have none, and the lj-\* commands of "
    r"LuaJIT and the pretty-printers of Tarantool will not work on the core, plain backtraces "
    r"will"
)
PACK_NO_DEBUG_INFO_HINT = (
    "hint: install the debug file of the tarantool, of the debuginfo or dbgsym package of its "
    "package, and pack the core again"
)


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

    archive, packed = pack_stripped(tt_cmd, tmp_path, coredump, tarantool)
    assert member in check_archive_members(archive)
    assert "has no debugging information" not in packed, packed

    rc, output = inspect(tt_cmd, tmp_path, archive, EXTENSION_COMMANDS)
    assert rc == 0, output
    check_extensions(output, True)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_debug_file(tt_cmd, tmp_path, coredump):
    """inspect of a core of a tarantool without debugging information points
    gdb to the debug file of its build ID in usr/lib/debug of the archive,
    where pack puts it, and warns before gdb starts when the archive has
    none, with a hint. The tarantool and its debug file are those of the
    tarantool of PATH, split by objcopy, and the test puts the debug file
    into the unpacked archive.

    Of a static build of Community the hints of pack and inspect name the
    tarantool of the static deb, which the tarantool of PATH is in CI, and
    the test does what the hint of inspect says: it unpacks the archive and
    copies the tarantool over the stripped one, then puts it back and copies
    the debug file to the path the warning names."""
    tarantool, member = debug_file_member()
    archive, packed = pack_stripped(tt_cmd, tmp_path, coredump, tarantool)
    release = static_deb_release(tarantool)
    name = archive.name.split(".")[0]

    # An archive without the debug file, packed where the host has none:
    # pack warns of it, while the debug file may still be installed.
    if member not in check_archive_members(archive):
        assert re.search(PACK_NO_DEBUG_INFO_WARNING, packed), packed
        if release is None:
            assert PACK_NO_DEBUG_INFO_HINT in packed, packed
        else:
            action = "pack the core with it, `tt coredump pack -e <its path>`"
            assert re.search(static_deb_hint(release, action), packed), packed
        rc, output = inspect(tt_cmd, tmp_path, archive, EXTENSION_COMMANDS)
        assert rc == 0, output
        check_extensions(output, False)
        if release is not None:
            action = (
                f"unpack the archive with `tt coredump unpack {archive}`, copy it over "
                f"{name}/tarantool, or to the debug file named above under {name}, and run "
                f"`tt coredump inspect {name}`"
            )
            assert re.search(static_deb_hint(release, action), output), output

    cmd = [tt_cmd, "coredump", "unpack", archive]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, output
    unpacked = tmp_path / name
    stripped = unpacked / "tarantool"

    if release is not None:
        rc, output = inspect(tt_cmd, tmp_path, unpacked, EXTENSION_COMMANDS)
        assert rc == 0, output
        check_extensions(output, False)
        assert re.search(static_deb_hint(release, inspect_action(unpacked)), output), output

        # The tarantool with the debugging information over the stripped
        # one, as the hint says.
        saved = tmp_path / "stripped"
        shutil.copy(stripped, saved)
        shutil.copy(tarantool, stripped)
        rc, output = inspect(tt_cmd, tmp_path, unpacked, EXTENSION_COMMANDS)
        assert rc == 0, output
        check_extensions(output, True)
        shutil.copy(saved, stripped)

    debug_file = unpacked / member
    debug_file.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["objcopy", "--only-keep-debug", tarantool, debug_file], check=True)

    rc, output = inspect(tt_cmd, tmp_path, unpacked, EXTENSION_COMMANDS)
    assert rc == 0, output
    check_extensions(output, True)


# What gdb says of the libthread_db it loads for a core, which it calls one
# of the host whatever directory of its search path it is in.
THREAD_DB_LOADED = re.compile(
    r"\[Thread debugging using libthread_db enabled\]\s*"
    r'Using host libthread_db library "([^"]+)"',
)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_thread_db(tt_cmd, tmp_path, coredump_unpacked):
    """inspect loads the libthread_db of the archive, which pack takes next
    to the thread library of the core, the one of the glibc of the core,
    rather than that of the host: gdb looks in the directories of the host
    first by default, "$sdir:$pdir", and took the one of the host whenever it
    matched the core, as on the host that packed it, and the archive's went
    untried."""
    unpacked = Path(coredump_unpacked).resolve()
    packed = list(unpacked.glob("**/libthread_db.so.1"))
    if not packed:
        pytest.skip("pack found no libthread_db on this host")

    rc, output = inspect(tt_cmd, tmp_path, unpacked.as_posix(), ["info threads"])
    assert rc == 0, output
    loaded = THREAD_DB_LOADED.search(output)
    assert loaded, output
    assert Path(loaded.group(1)).resolve() in [path.resolve() for path in packed], output
    # The libthread_db of the archive is of the glibc of this host, and the
    # kernel writes every stack: gdb reads tarantool_L of a core of 3.7
    # through it, and inspect warns of nothing. A warning of tarantool_L,
    # that gdb will or may not read it, names it, as nothing gdb prints for
    # info threads does.
    assert "tarantool_L" not in output, output


# The gdb script that prints the pid of the process of a core, and for each
# thread its LWP and whether the 1 KiB above its stack pointer is zeros:
# True, False, or None when gdb cannot read it.
STACKS_SCRIPT = """\
import gdb

inferior = gdb.selected_inferior()
gdb.write("tt-test-pid %d\\n" % inferior.pid)
for thread in inferior.threads():
    thread.switch()
    sp = int(gdb.parse_and_eval("$sp"))
    try:
        zeros = not any(bytes(inferior.read_memory(sp, 1024)))
    except gdb.MemoryError:
        zeros = None
    gdb.write("tt-test-thread %d %s\\n" % (thread.ptid[1], zeros))
"""


def gdb_thread_stacks(tmp_path, core):
    """The number of the threads of core but the main one, as gdb reads the
    core with the tarantool of PATH, and of those of them that have nothing
    but zeros in the 1 KiB above their stack pointers, as tt checks them."""
    script = tmp_path / "stacks.py"
    script.write_text(STACKS_SCRIPT)
    process = subprocess.run(
        ["gdb", "-batch", "-nx", "-x", script, shutil.which("tarantool"), core],
        capture_output=True,
        text=True,
        env=gdb_env(),
    )
    pid = re.search(r"^tt-test-pid (\d+)$", process.stdout, re.MULTILINE)
    assert pid, process.stdout + process.stderr
    threads = re.findall(r"^tt-test-thread (\d+) (\w+)$", process.stdout, re.MULTILINE)
    others = [zeros for lwp, zeros in threads if lwp != pid.group(1)]
    return len(others), others.count("True")


def gcore_zeroing_host():
    """What tells whether gcore of the gdb of PATH writes the stacks of the
    threads but the main one as zeros on this host, as a line to print: a gdb
    older than 18.1 does on glibc 2.42 or later, which guards them with
    MADV_GUARD_INSTALL, and Linux 6.13 or later, which has it, unless its
    distribution backports the fix, c1da013915 of gdb."""
    try:
        glibc = os.confstr("CS_GNU_LIBC_VERSION") or "no glibc"
    except (ValueError, OSError):
        glibc = "no glibc"
    shown = subprocess.run(
        ["gdb", "--version"],
        capture_output=True,
        text=True,
        env=gdb_env(),
    ).stdout
    gdb = shown.splitlines()[0] if shown else "no gdb version"
    return f"{gdb}, {glibc}, Linux {platform.release()}"


def check_stacks_warning(output, others, zeroed):
    """Check that tt warned of the zeroed of the others threads of a core
    besides the main one whose stacks are zeros, and hinted at what to do, and
    of none without them."""
    if not zeroed:
        assert "nothing but zeros" not in output, output
        return
    threads = "threads of the core besides the main one"
    if zeroed == others == 1:
        subject = "the thread of the core besides the main one has"
    elif zeroed == others:
        subject = f"all {others} {threads} have"
    else:
        subject = f"{zeroed} of the {others} {threads} {'has' if zeroed == 1 else 'have'}"
    backtraces = (
        "the backtrace of that thread will be empty"
        if zeroed == 1
        else "the backtraces of those threads will be empty"
    )
    assert (
        f"{subject} nothing but zeros in the 1024 bytes above the stack pointer: {backtraces}, "
        "and libthread_db may fail on the core" in output
    ), output
    assert (
        "hint: gcore of gdb older than 18.1 writes such stacks on glibc 2.42 or later with Linux "
        "6.13 or later; take the core the kernel writes, or write it with gcore-pages of "
        "tarantool-gdb-tools" in output
    ), output


def pack_and_inspect(tt_cmd, tmp_path, core):
    """The outputs of tt coredump pack of core with the tarantool of PATH and
    of tt coredump inspect of the archive, each checked to succeed, both with
    -V, which shows what they say at the debug level."""
    out = tmp_path / "out"
    out.mkdir()
    cmd = [tt_cmd, "-V", "coredump", "pack", "-e", shutil.which("tarantool"), "-d", out, core]
    rc, packed = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0, packed
    archives = list(out.glob("*.tar.gz"))
    assert len(archives) == 1
    rc, inspected = inspect(tt_cmd, tmp_path, archives[0].as_posix(), ["bt 1"], tt_args=["-V"])
    assert rc == 0, inspected
    return packed, inspected


def test_coredump_zeroed_stacks(tt_cmd, tmp_path, gcore_with_thread):
    """pack and inspect of a gcore of a tarantool with a thread besides the
    main one warn of the threads whose stacks the core has as zeros, the ones
    gdb finds zeros at their stack pointers in the core, and of no other.
    gcore of a gdb older than 18.1 writes them so on glibc 2.42 or later with
    Linux 6.13 or later, which the test prints along with what it found: a
    distribution may backport the fix, and the check is that tt and gdb
    agree."""
    others, zeroed = gdb_thread_stacks(tmp_path, gcore_with_thread)
    assert others > 0, "the tarantool started no thread besides the main one"
    print(f"gcore of {gcore_zeroing_host()}: {zeroed} of {others} stacks are zeros")

    packed, inspected = pack_and_inspect(tt_cmd, tmp_path, gcore_with_thread)
    check_stacks_warning(packed, others, zeroed)
    check_stacks_warning(inspected, others, zeroed)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_kernel_stacks(tt_cmd, tmp_path, coredump_with_thread):
    """pack and inspect of the core the kernel writes of a tarantool with a
    thread besides the main one read the stack of every thread and warn of
    none: the kernel writes every stack, whatever guards it. What they read
    is said at the debug level, which -V shows."""
    others, zeroed = gdb_thread_stacks(tmp_path, coredump_with_thread)
    assert others > 0, "the tarantool started no thread besides the main one"
    assert zeroed == 0

    packed, inspected = pack_and_inspect(tt_cmd, tmp_path, coredump_with_thread)
    read = (
        f"read the stacks of {others} of the {others} threads of the core besides the main one, "
        "0 of them zeros"
    )
    for output in (packed, inspected):
        assert read in output, output
        assert "cannot tell whether the stacks" not in output, output
        check_stacks_warning(output, others, 0)


# The lines each extension of the fake sources starts with, which gdb prints
# when it sources the extension, before what may fail to load without the
# debugging information of the tarantool.
SOURCES_MARK = "import gdb as tt_test_gdb\ntt_test_gdb.write('tt-test: {} of the sources\\n')\n"


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_sourcedir(tt_cmd, tmp_path, coredump_unpacked):
    """inspect -s loads the extensions of the sources: luajit_dbg.py of the
    LuaJIT submodule and tools/tarantool-gdb.py, here the copies of tt with
    a mark, and asks GitHub nothing. gdb.sh says nothing of how to get the
    sources."""
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
    assert "git clone" not in output, output
    check_extensions(output, has_debug_info(shutil.which("tarantool")))


# The warning of inspect --experimental of an extension the core is out of
# the range of: what it was checked on, and what the core is of.
UNCHECKED_WARNING = r"[\w.-]+\.py is checked on [^\n]*?, the core is of [^\n]*?: it may not work"


def join_and(names):
    """names as a sentence lists them, as the warnings of inspect do."""
    return names[0] if len(names) == 1 else ", ".join(names[:-1]) + " and " + names[-1]


def unchecked_warnings(version_line):
    """The warnings of inspect --experimental of a core of this host of the
    Tarantool of version_line, the first line of its version file: one for
    each extension of the manifest whose range the minor of the version is out
    of, as the manifest means a range, every patch of every minor from that of
    min to that of max, or whose architectures that of the host is none of."""
    match = re.match(r"Tarantool (?:Enterprise )?(\d+\.\d+\.\d+(?:-[a-z]+\d*)?)", version_line)
    assert match, version_line
    tag = match.group(1)
    machine = platform.machine()
    warnings = set()
    for entry in gdb_tools_extensions():
        checked, core = [], []
        first, last = checked_minors(entry)
        if not first <= release_of(version_line)[:2] <= last:
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
    """inspect --experimental of an archive loads the extensions of
    tarantool-gdb-tools tt embeds rather than those of the Tarantool of the
    core, says so, and asks GitHub nothing; lj-stack, which test_gdb_tools.py
    does not run, shows the Lua stack of the chunk that crashed. inspect
    warns of the extensions whose range of their manifest the Tarantool of
    the core is out of, by its minor, and of no other: none while that of
    the tests is in all of them. What the other commands show of a core is
    checked by test_gdb_tools.py."""
    if not has_debug_info(shutil.which("tarantool")):
        pytest.skip("the extensions need the debugging information of the tarantool")

    rc, output = inspect(tt_cmd, tmp_path, coredump_packed, ["lj-stack"], "--experimental")
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
