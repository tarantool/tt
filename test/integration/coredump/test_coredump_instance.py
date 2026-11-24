"""The cores of the instances tt runs, found by the crashes their watchdogs
record.

The watchdog of an instance records every exit of its process by a signal
in crashes.json of its run directory, with its PID, the signal and whether
the kernel dumped a core. tt coredump list shows them, with where the core
is now as kernel.core_pattern says, and tt coredump pack <APP>:<INSTANCE>
packs the core of the last crash of an instance with the tails of its logs.

The tests start an instance of a project with tt instance start and crash
it with the Lua that crashes a tarantool, over tt connect. The tests of a
crash with a core need a core size limit the kernel dumps under, and leave
the core where kernel.core_pattern puts it: they are run explicitly, with
TT_ENABLE_COREDUMP_TESTS=1, as the other tests of kernel cores are. A core
of a pattern of a file goes into the working directory of the process, the
project, under the temporary directory of the test.
"""

import json
import os
import re
import resource
import shutil
import signal
import subprocess
import sys
import tarfile
from pathlib import Path

import pytest
from coredump_helpers import CRASH, offline_env, skip_coredump_cond, skip_coredump_reason

from utils import (
    box_info_pid,
    extract_status_from_json,
    inst_log_dir,
    inst_run_dir,
    instances_file,
    run_command_and_get_output,
    wait_event,
)

# spell-checker:ignore getrlimit setrlimit rlimit coredumpctl apport

APP = "app"
INST = f"{APP}:{APP}"

pytestmark = pytest.mark.skipif(sys.platform != "linux", reason="kernel.core_pattern is Linux")


def make_app(tmp_path, restart="no"):
    """The project of the application APP, with the instance APP of a script
    that runs box.cfg, of the restart policy restart. It returns the
    directory of the project, the working directory of the process."""
    app_dir = tmp_path / APP
    (app_dir / instances_file).parent.mkdir(parents=True)
    (app_dir / instances_file).write_text(f"{APP}:\n  script: init.lua\n  restart: {restart}\n")
    (app_dir / "init.lua").write_text("box.cfg{}\n")
    return app_dir


def core_limit():
    """The preexec_fn of a process whose core size limit is the hard one, so
    that the kernel dumps a core of it; the test is skipped where the hard
    limit is 0."""
    _, hard = resource.getrlimit(resource.RLIMIT_CORE)
    if hard == 0:
        pytest.skip("the hard RLIMIT_CORE is 0: the kernel dumps no core")

    def unlimited():
        resource.setrlimit(resource.RLIMIT_CORE, (hard, hard))

    return unlimited


def start(tt_cmd, app_dir, preexec_fn=None):
    """Start the instances of the project app_dir with tt instance start,
    the watchdog and tarantool with the limits preexec_fn sets."""
    process = subprocess.run(
        [tt_cmd, "instance", "start"],
        cwd=app_dir,
        capture_output=True,
        text=True,
        preexec_fn=preexec_fn,
    )
    assert process.returncode == 0, process.stdout + process.stderr
    assert f"The instance {INST} is up." in process.stdout + process.stderr


def stop(tt_cmd, app_dir):
    """Stop the instances of the project app_dir."""
    run_command_and_get_output([tt_cmd, "instance", "stop", "-y"], cwd=app_dir)


def status(tt_cmd, app_dir):
    """The status of INST, as -o json gives it."""
    rc, out = run_command_and_get_output(
        [tt_cmd, "instance", "status", INST, "-o", "json"],
        cwd=app_dir,
        stderr=subprocess.DEVNULL,
    )
    assert rc == 0, out
    return extract_status_from_json(out)[INST]


def crash(tt_cmd, app_dir):
    """Crash the process of INST with the Lua that crashes a tarantool, over
    tt connect, and return its PID once the watchdog has recorded the
    crash."""
    pid = box_info_pid(tt_cmd, app_dir, INST)
    subprocess.run(
        [tt_cmd, "connect", INST, "-f", "-"],
        cwd=app_dir,
        input=CRASH + "\n",
        capture_output=True,
        text=True,
        timeout=60,
    )
    run_dir = inst_run_dir(app_dir, APP, APP)

    def recorded():
        try:
            with open(os.path.join(run_dir, "crashes.json")) as f:
                return pid in [entry["pid"] for entry in json.load(f)["crashes"]]
        except (OSError, ValueError):
            return False

    assert wait_event(60, recorded), f"no crash of {pid} is recorded in {run_dir}"
    return pid


def coredump_list(tt_cmd, app_dir, *args):
    """The exit code, the standard output and the standard error of tt
    coredump list with args."""
    process = subprocess.run(
        [tt_cmd, "coredump", "list", *args],
        cwd=app_dir,
        capture_output=True,
        text=True,
    )
    return process.returncode, process.stdout, process.stderr


def listed_row(out, pid):
    """The SIGNAL and the CORE of the row of the crash of pid of INST in the
    table of tt coredump list."""
    found = re.search(
        rf"^{re.escape(INST)}\s+\d{{4}}-\d\d-\d\d \d\d:\d\d:\d\d\s+{pid}\s+(SIG\w+)\s+(.+)$",
        out,
        re.MULTILINE,
    )
    assert found, out
    return found.group(1), found.group(2)


def read_core_pattern():
    """kernel.core_pattern as it is now."""
    with open("/proc/sys/kernel/core_pattern") as f:
        return f.read().strip()


def listed_file(core):
    """The path of the core file of the CORE of tt coredump list, which is to
    be there: "PATH, SIZE"."""
    found = re.fullmatch(r"(/.+), (\d+(?:\.\d)? [KMGT]?i?B)", core)
    assert found, core
    path = Path(found.group(1))
    assert path.is_file(), core
    return path


def check_listed_core(core, app_dir, pid):
    """Check the CORE of tt coredump list of a crash of pid with a core,
    where kernel.core_pattern says: a file of a pattern of a file, which is
    there and named by the PID; the core systemd-coredump keeps; the core
    apport keeps in /var/lib/apport/coredump, of the PID, or, of an older
    apport, in the working directory of the process, or else the report of
    apport the list names, which is to be there. Another pipe skips the
    test. It returns the path of a core file, or None."""
    pattern = read_core_pattern()
    if not pattern.startswith("|"):
        path = listed_file(core)
        assert str(pid) in path.name, core
        if not os.path.isabs(pattern):
            assert path.parent == app_dir, core
        return path
    if "systemd-coredump" in pattern:
        assert core.startswith(f"kept by systemd-coredump: coredumpctl dump {pid}"), core
        return None
    if "apport" in pattern:
        # A packaged tarantool gets a report rather than a core.
        report = re.search(r"apport may have reported the crash in (/\S+\.crash):", core)
        if report:
            assert Path(report.group(1)).is_file(), core
            return None
        path = listed_file(core)
        if path.parent == Path("/var/lib/apport/coredump"):
            assert path.name.split(".")[4] == str(pid), core
        else:
            assert path == app_dir / "core" or path == app_dir / f"core.{pid}", core
        return path
    pytest.skip(f"tt finds no core of kernel.core_pattern '{pattern}'")


def test_list_without_core(tt_cmd, tmp_path):
    """A process SIGKILL ended dumped no core: tt coredump list shows its
    crash, by its PID, through the restart that follows and a stop, and says
    so; -o json gives the same, and an instance with no crash is named."""
    app_dir = make_app(tmp_path, restart="on-failure")
    try:
        start(tt_cmd, app_dir)
        rc, out, err = coredump_list(tt_cmd, app_dir)
        assert rc == 0, err
        assert out == "", out
        assert f"No crash is recorded of {INST}." in err

        pid = box_info_pid(tt_cmd, app_dir, INST)
        os.kill(pid, signal.SIGKILL)
        assert wait_event(30, lambda: status(tt_cmd, app_dir)["pid"] not in (None, pid))
        assert wait_event(30, lambda: status(tt_cmd, app_dir)["box"] == "running")
    finally:
        stop(tt_cmd, app_dir)

    rc, out, err = coredump_list(tt_cmd, app_dir, INST)
    assert rc == 0, err
    assert out.startswith("INSTANCE  CRASHED AT "), out
    assert listed_row(out, pid) == ("SIGKILL", "none dumped")
    assert "No crash is recorded" not in err

    rc, out, err = coredump_list(tt_cmd, app_dir, "-o", "json")
    assert rc == 0, err
    listed = json.loads(out)
    assert [entry["instance"] for entry in listed] == [INST]
    (crashed,) = listed[0]["crashes"]
    assert crashed["pid"] == pid
    assert crashed["signal"] == signal.SIGKILL
    assert crashed["signal_name"] == "SIGKILL"
    assert crashed["core_dumped"] is False
    assert "no core was dumped" in crashed["missing"]
    assert "core" not in crashed

    rc, out, err = coredump_list(tt_cmd, app_dir, f"{APP}:nope")
    assert rc != 0
    assert "nope" in err


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_list_crash_with_core(tt_cmd, tmp_path):
    """A tarantool that crashed with a core: tt coredump list shows the
    crash and where the core is, as kernel.core_pattern says, and tt
    instance status --details points at the list."""
    app_dir = make_app(tmp_path)
    try:
        start(tt_cmd, app_dir, preexec_fn=core_limit())
        pid = crash(tt_cmd, app_dir)
    finally:
        stop(tt_cmd, app_dir)

    rc, out, err = coredump_list(tt_cmd, app_dir)
    assert rc == 0, err
    name, core = listed_row(out, pid)
    assert name in ("SIGSEGV", "SIGABRT"), out
    check_listed_core(core, app_dir, pid)
    assert f'kernel.core_pattern "{read_core_pattern()}"' in err
    assert "tt coredump pack <APP>:<INSTANCE> packs the core" in err

    rc, out = run_command_and_get_output(
        [tt_cmd, "instance", "status", INST, "--details"],
        cwd=app_dir,
    )
    assert rc == 0, out
    last_crash = rf"Last crash of {INST}: PID {pid}, it was killed by signal \d+ .*, core dumped, "
    assert re.search(last_crash, out), out
    assert f"tt coredump list {INST} tells where its core is." in out


def coredump_pack(tt_cmd, app_dir, *args):
    """The exit code and the output, stdout and stderr, of tt coredump pack
    with args, run in app_dir."""
    process = subprocess.run(
        [tt_cmd, "coredump", "pack", *args],
        cwd=app_dir,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    return process.returncode, process.stdout


def test_pack_instance_without_core(tt_cmd, tmp_path):
    """tt coredump pack of an instance with no crash, and of one whose last
    crash dumped no core, says why there is nothing to pack, and where to
    look."""
    app_dir = make_app(tmp_path)
    try:
        start(tt_cmd, app_dir)
        rc, out = coredump_pack(tt_cmd, app_dir, INST)
        assert rc != 0, out
        assert f"{INST}: no crash is recorded" in out
        assert f"tt coredump list {INST} lists the crashes of the instance" in out

        pid = box_info_pid(tt_cmd, app_dir, INST)
        os.kill(pid, signal.SIGKILL)
        assert wait_event(30, lambda: status(tt_cmd, app_dir)["status"] == "NOT RUNNING")
    finally:
        stop(tt_cmd, app_dir)

    rc, out = coredump_pack(tt_cmd, app_dir, INST)
    assert rc != 0, out
    assert re.search(
        rf"{INST}: no core was dumped by any of its 1 crashes: the last, PID {pid}, SIGKILL, ",
        out,
    ), out
    assert f"tt coredump list {INST}" in out
    assert not list(app_dir.glob("*.tar.gz")), "nothing is packed"


def inspect_bt(tt_cmd, tmp_path, archive):
    """The exit code and the output of tt coredump inspect of archive that
    runs bt in gdb, offline (offline_env)."""
    process = subprocess.run(
        [tt_cmd, "coredump", "inspect", archive],
        cwd=tmp_path,
        input="bt 1\nq\n",
        capture_output=True,
        text=True,
        env=offline_env(tmp_path),
    )
    return process.returncode, process.stdout + process.stderr


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_pack_instance(tt_cmd, tmp_path):
    """tt coredump pack <APP>:<INSTANCE> packs the core of the last crash of
    the instance, where kernel.core_pattern put it, into an archive named by
    the PID of the crash, says which crash and where the core came from, and
    tt coredump inspect opens the archive."""
    if shutil.which("gdb") is None:
        pytest.skip("needs gdb to inspect the archive")
    app_dir = make_app(tmp_path)
    try:
        start(tt_cmd, app_dir, preexec_fn=core_limit())
        pid = crash(tt_cmd, app_dir)
    finally:
        stop(tt_cmd, app_dir)

    rc, out, err = coredump_list(tt_cmd, app_dir, INST)
    assert rc == 0, err
    _, listed = listed_row(out, pid)
    core = check_listed_core(listed, app_dir, pid)

    out_dir = tmp_path / "out"
    out_dir.mkdir()
    rc, out = coredump_pack(tt_cmd, app_dir, INST, "-d", out_dir)
    assert rc == 0, out
    assert re.search(rf"Packing the core of the crash of {INST}, PID {pid}, SIG\w+, at ", out)
    if core is not None:
        source = "the core apport wrote" if "apport" in read_core_pattern() else "the file of"
        assert f"from {source}" in out and f", {core}." in out, out
    archives = list(out_dir.glob(f"tarantool-core-{pid}-*.tar.gz"))
    assert len(archives) == 1, out
    assert f"Core was successfully packed: {archives[0]}" in out
    # A core coredumpctl dumped for pack goes once it is packed.
    assert [path.name for path in out_dir.iterdir()] == [archives[0].name]

    # The tail of tt.log, with what tarantool said as it crashed, is in
    # logs/ of the archive; the config is not.
    log = Path(inst_log_dir(app_dir, APP, APP)) / "tt.log"
    assert f"Packing the log {log} as logs/tt.log of the archive." in out, out
    with tarfile.open(archives[0]) as archive:
        names = {name.split("/", 1)[1] for name in archive.getnames()}
        (member,) = [name for name in archive.getnames() if name.endswith("/logs/tt.log")]
        packed_log = archive.extractfile(member).read().decode()
    assert "logs/tt.log" in names
    assert not [name for name in names if name.endswith((".yaml", ".lua"))], names
    assert f"tarantool, PID {pid}, has ended: it was killed by signal" in packed_log
    assert "core dumped" in packed_log

    rc, out = inspect_bt(tt_cmd, tmp_path, archives[0])
    assert rc == 0, out
    assert re.search(r"#0 ", out), out
    note = (
        "The archive has the logs of the instance of the core in logs/, tt.log: "
        f"tt coredump unpack {archives[0]} puts them into "
    )
    assert note in out, out

    # tt coredump unpack keeps them.
    unpack_dir = tmp_path / "unpacked"
    unpack_dir.mkdir()
    rc, out = run_command_and_get_output(
        [tt_cmd, "coredump", "unpack", archives[0]],
        cwd=unpack_dir,
    )
    assert rc == 0, out
    (unpacked,) = unpack_dir.iterdir()
    assert (unpacked / "logs" / "tt.log").read_text() == packed_log


@pytest.mark.skipif(skip_coredump_cond, reason=skip_coredump_reason)
def test_list_after_restart_and_stop(tt_cmd, tmp_path):
    """A crash with a core that the restart policy followed with a start, and
    a clean stop later: the crash is listed with its core all the same, and
    after the next start too."""
    app_dir = make_app(tmp_path, restart="on-failure")
    try:
        start(tt_cmd, app_dir, preexec_fn=core_limit())
        pid = crash(tt_cmd, app_dir)
        assert wait_event(30, lambda: status(tt_cmd, app_dir)["pid"] not in (None, pid))
        assert wait_event(30, lambda: status(tt_cmd, app_dir)["box"] == "running")
        stop(tt_cmd, app_dir)

        info = status(tt_cmd, app_dir)
        assert info["status"] == "NOT RUNNING"
        assert info["last_exit"]["stopped"] is True
        assert info["last_crash"]["pid"] == pid
        assert info["last_crash"]["core_dumped"] is True

        rc, out, err = coredump_list(tt_cmd, app_dir, INST)
        assert rc == 0, err
        _, core = listed_row(out, pid)
        check_listed_core(core, app_dir, pid)

        start(tt_cmd, app_dir)
        rc, out, err = coredump_list(tt_cmd, app_dir, f"{APP}:*")
        assert rc == 0, err
        listed_row(out, pid)
    finally:
        stop(tt_cmd, app_dir)
