import glob
import os
import re
import resource
import subprocess
from pathlib import Path

import pytest

from utils import get_tarantool_version, run_command_and_get_output

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


skip_coredump_cond = os.getenv('TT_ENABLE_COREDUMP_TESTS') is None
skip_coredump_reason = "Should be launched explicitly to control coredump it produces"


def generate_coredump(tmp_path_factory, tarantool_bin='tarantool') -> Path:
    coredump_dir = tmp_path_factory.mktemp("coredump")
    with open('/proc/sys/kernel/core_pattern', 'r') as f:
        core_pattern = f.read().strip()

    def coredump_apport(core_source, outdir):
        process = subprocess.run(['apport-unpack', core_source, outdir])
        assert process.returncode == 0
        return outdir / 'CoreDump'

    def coredump_systemd(core_source, outdir):
        core = outdir / core_source.stem
        process = subprocess.run(['coredumpctl', 'dump', f'--output={core}'])
        assert process.returncode == 0
        return core

    to_coredump = None
    if not core_pattern.startswith('|'):
        core_wildcard = core_pattern.replace('%%', '%')
        core_wildcard = re.sub('%[cdeEghiIpPstu]', '*', core_wildcard)
        if not os.path.isabs(core_wildcard):
            core_wildcard = str(coredump_dir / core_wildcard)
    elif re.search(r"apport", core_pattern):
        core_wildcard = '/var/crash/*.crash'
        to_coredump = coredump_apport
    elif re.search(r"systemd-coredump", core_pattern):
        core_wildcard = '/var/lib/systemd/coredump/*'
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
def coredump_alt(tmp_path_factory, tmpdir_with_tarantool) -> Path:
    return generate_coredump(tmp_path_factory, tmpdir_with_tarantool / 'bin' / 'tarantool')


@pytest.fixture(scope="session")
def coredump_packed(tt_cmd, coredump):
    cmd = [tt_cmd, "coredump", "pack", coredump.as_posix()]
    rc, _ = run_command_and_get_output(cmd, cwd=coredump.parents[0])
    assert rc == 0
    files = glob.glob(os.path.join(os.path.dirname(coredump), '*.tar.gz'))
    assert len(files) == 1
    return files[0]


@pytest.fixture(scope="session")
def coredump_unpacked(tt_cmd, coredump_packed):
    cmd = [tt_cmd, "coredump", "unpack", coredump_packed]
    rc, _ = run_command_and_get_output(cmd, cwd=os.path.dirname(coredump_packed))
    assert rc == 0
    packed_name = os.path.basename(coredump_packed)
    unpacked = os.path.join(os.path.dirname(coredump_packed), packed_name.split('.')[0])
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
def test_coredump_pack(tt_cmd, tmp_path, coredump):
    cmd = [tt_cmd, "coredump", "pack", coredump]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Core was successfully packed.", output)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
@pytest.mark.slow
def test_coredump_pack_executable(tt_cmd, tmp_path, coredump_alt, tmpdir_with_tarantool):
    tarantool_bin = tmpdir_with_tarantool / 'bin' / 'tarantool'
    version_expected = get_tarantool_version(tarantool_bin)

    cmd = [tt_cmd, "coredump", "pack", "-e", tarantool_bin, coredump_alt]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Core was successfully packed.", output)
    archives = list(tmp_path.glob("*.tar.gz"))
    assert len(archives) == 1

    # Extract Tarantool executable and check its version.
    rc = subprocess.run(["tar", "xzf", archives[0], "--wildcards", "*/tarantool"],
                        cwd=tmp_path).returncode
    assert rc == 0
    unpacked_tarantools = list(tmp_path.glob("*/tarantool"))
    assert len(unpacked_tarantools) == 1
    version = get_tarantool_version(unpacked_tarantools[0])
    assert version == version_expected


def test_coredump_unpack_no_arg(tt_cmd, tmp_path):
    cmd = [tt_cmd, "coredump", "unpack"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0
    assert re.search(r"tt coredump unpack", output)


def test_coredump_unpack_no_such_file(tt_cmd, tmp_path):
    cmd = [tt_cmd, "coredump", "unpack", "file_that_doesnt_exist"]
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
    cmd = [tt_cmd, "coredump", "inspect", "file_that_doesnt_exist"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc != 0
    assert re.search(r"failed to inspect", output)


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_packed(tt_cmd, tmp_path, coredump_packed):
    cmd = [tt_cmd, "coredump", "inspect", coredump_packed]
    process = subprocess.run(
        cmd,
        cwd=tmp_path,
        input="\nq\n",
        text=True,
    )
    assert process.returncode == 0


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_coredump_inspect_unpacked(tt_cmd, tmp_path, coredump_unpacked):
    cmd = [tt_cmd, "coredump", "inspect", coredump_unpacked]
    process = subprocess.run(
        cmd,
        cwd=tmp_path,
        input="\nq\n",
        text=True,
    )
    assert process.returncode == 0
