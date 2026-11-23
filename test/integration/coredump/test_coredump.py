import glob
import json
import os
import platform
import re
import resource
import shutil
import subprocess
import tarfile
from pathlib import Path

import pytest

from utils import run_command_and_get_output

# The gdb extensions tt embeds, which pack puts into ext/ of the archive.
EXTENSIONS_DIR = (
    Path(__file__).parents[3] / "modules" / "coredump" / "internal" / "coredump" / "extensions"
)

# spell-checker:ignore getrlimit setrlimit rlimit rlim coredumps coredumpctl apport

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
    assert re.search(r"pack script execution failed", output)


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


def gdb_debug_dir_env(tmp_path, debug_dir):
    """The environment of a tt whose gdb looks for separate debug files in
    debug_dir alone, as a debug-file-directory of the gdbinit of its home
    says."""
    home = tmp_path / ("home-" + debug_dir.name)
    home.mkdir()
    (home / ".gdbinit").write_text(f"set debug-file-directory {debug_dir}\n")
    env = os.environ.copy()
    env["HOME"] = home.as_posix()
    env["XDG_CONFIG_HOME"] = (home / ".config").as_posix()
    return env


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_pack_debug_file(tt_cmd, tmp_path, coredump):
    """pack of a core of a tarantool without debugging information takes the
    debug file of its build ID from a debug directory of gdb into the
    archive, and inspect points gdb to it; with none there, inspect warns
    before gdb starts. The tarantool and its debug file are those of the
    tarantool of PATH, split by objcopy."""
    tarantool = shutil.which("tarantool")
    described = subprocess.run(["file", "-L", tarantool], capture_output=True, text=True)
    if shutil.which("objcopy") is None or "with debug_info" not in described.stdout:
        pytest.skip("needs objcopy and a tarantool with debugging information of its own")
    build_id = re.search(r"BuildID\[[^]]*\]=([0-9a-f]+)", described.stdout).group(1)

    stripped = tmp_path / "bin" / "tarantool"
    stripped.parent.mkdir()
    subprocess.run(["objcopy", "--strip-debug", tarantool, stripped], check=True)

    debug_dir = tmp_path / "debug"
    member = f"usr/lib/debug/.build-id/{build_id[:2]}/{build_id[2:]}.debug"
    (debug_dir / member).parent.mkdir(parents=True)
    subprocess.run(["objcopy", "--only-keep-debug", tarantool, debug_dir / member], check=True)

    empty_dir = tmp_path / "empty"
    empty_dir.mkdir()

    for collected, gdb_dir in ((True, debug_dir / "usr" / "lib" / "debug"), (False, empty_dir)):
        out = tmp_path / f"out-{collected}"
        out.mkdir()
        cmd = [tt_cmd, "coredump", "pack", "-e", stripped, "-d", out, coredump]
        env = gdb_debug_dir_env(tmp_path, gdb_dir)
        rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
        assert rc == 0, output

        archives = list(out.glob("*.tar.gz"))
        assert len(archives) == 1
        assert (member in check_archive_members(archives[0])) == collected

        rc, output = inspect(tt_cmd, tmp_path, archives[0], EXTENSION_COMMANDS)
        assert rc == 0, output
        check_extensions(output, collected)


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
