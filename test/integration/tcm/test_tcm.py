import os
from subprocess import PIPE, STDOUT, Popen, run

from utils import skip_if_tcm_not_supported, wait_for_lines_in_output

TcmStartCommand = ("tcm", "start")
TcmStartWatchdogCommand = ("tcm", "start", "--watchdog")
TcmStatusCommand = ("tcm", "status")
TcmStopCommand = ("tcm", "stop")


def test_tcm_start_success(tt_cmd, tmp_path):
    skip_if_tcm_not_supported()

    start_cmd = [tt_cmd, *TcmStartCommand]
    print(f"Run: {start_cmd}")

    tcm = Popen(
        start_cmd,
        cwd=tmp_path,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=STDOUT,
    )

    output = wait_for_lines_in_output(tcm.stdout, ["process PID"])

    assert tcm.pid

    with open(os.path.join(tmp_path, "tcm.pid"), "r") as f:
        tcm_pid = f.read().strip()
    assert f'• Interactive process PID {tcm_pid} written to "tcm.pid"' in output.strip()

    cmdStatus = [str(tt_cmd), *TcmStatusCommand]
    print(f"Run: {' '.join(cmdStatus)}")

    status = Popen(
        cmdStatus,
        cwd=tmp_path,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=STDOUT,
    )

    output = wait_for_lines_in_output(status.stdout, ["TCM", "RUNNING"])
    assert "RUNNING" in output

    cmdStop = [str(tt_cmd), *TcmStopCommand]
    print(f"Run: {' '.join(cmdStop)}")

    stop = Popen(
        cmdStop,
        cwd=tmp_path,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=STDOUT,
    )

    output = wait_for_lines_in_output(stop.stdout, ["TCM"])

    assert "TCM stopped" in output.strip()
    assert tcm.poll() is not None


def test_tcm_start_with_watchdog_success(tt_cmd, tmp_path):
    skip_if_tcm_not_supported()

    cmd = [str(tt_cmd), *TcmStartWatchdogCommand]
    print(f"Run: {' '.join(cmd)}")

    tcm = Popen(
        cmd,
        cwd=tmp_path,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=STDOUT,
    )

    output = wait_for_lines_in_output(tcm.stdout, ["Process started successfully"])
    assert "• Process started successfully" in output.strip()

    cmdStatus = [str(tt_cmd), *TcmStatusCommand]
    print(f"Run: {' '.join(cmdStatus)}")

    status = run(
        cmdStatus,
        cwd=tmp_path,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=STDOUT,
    )

    with open(os.path.join(tmp_path, "tcm.pid"), "r") as f:
        tcm_pid = f.read().strip()

    assert "TCM" and "RUNNING" and tcm_pid in status.stdout

    tcm.terminate()
    tcm.wait()

    assert tcm.pid is not None
    assert tcm.poll() is not None

    start_cmd = [tt_cmd, *TcmStartCommand]
    print(f"Run: {start_cmd}")

    tcm = Popen(
        start_cmd,
        cwd=tmp_path,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=STDOUT,
    )

    output = wait_for_lines_in_output(tcm.stdout, ["process PID"])
    assert tcm.pid

    with open(os.path.join(tmp_path, "tcm.pid"), "r") as f:
        tcm_pid = f.read().strip()
    assert f'• Interactive process PID {tcm_pid} written to "tcm.pid"' in output.strip()

    tcmDouble = Popen(
        start_cmd,
        cwd=tmp_path,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=STDOUT,
    )

    output = wait_for_lines_in_output(tcmDouble.stdout, ["process PID"])
    assert tcm.pid
