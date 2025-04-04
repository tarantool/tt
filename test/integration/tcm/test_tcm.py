
from subprocess import PIPE, Popen

from utils import skip_if_tarantool_ce, wait_for_lines_in_output

TcmStartCommand = ("tcm", "start")
TcmStartWatchdogCommand = ("tcm", "start", "--watchdog")
TcmStatusCommand = ("tcm", "status")


def test_tcm_start_success(tt_cmd):
    skip_if_tarantool_ce()

    cmd = [str(tt_cmd), *TcmStartCommand]
    print(f"Run: {' '.join(cmd)}")

    tcm = Popen(
        cmd,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=PIPE,
    )

    wait_for_lines_in_output(tcm.stdout, ["TCM_CLUSTER_CONNECTION_RATE_LIMIT"])
    tcm.terminate()
    tcm.wait()

    assert tcm.poll() is not None


def test_tcm_start_with_watchdog_success(tt_cmd):
    skip_if_tarantool_ce()

    cmd = [str(tt_cmd), *TcmStartWatchdogCommand]
    print(f"Run: {' '.join(cmd)}")

    tcm = Popen(
        cmd,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=PIPE,
    )

    wait_for_lines_in_output(tcm.stdout, ["connecting to storage..."])
    tcm.terminate()
    tcm.wait()

    assert tcm.pid is not None

    assert tcm.poll() is not None


def test_tcm_start_status_running(tt_cmd):
    skip_if_tarantool_ce()

    cmd = [str(tt_cmd), *TcmStartWatchdogCommand]
    print(f"Run: {' '.join(cmd)}")

    tcm = Popen(
        cmd,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=PIPE,
    )

    wait_for_lines_in_output(tcm.stdout, ["TCM_CLUSTER_CONNECTION_RATE_LIMIT"])

    print(f"tcm: {tcm.returncode}")

    cmdStatus = [str(tt_cmd), *TcmStatusCommand]
    print(f"Run cmdStatus: {' '.join(cmdStatus)}")

    tcmStatus = Popen(
        cmdStatus,
        text=True,
        encoding="utf-8",
        stdout=PIPE,
        stderr=PIPE,
    )

    print(f"tcmStatus.returncode: {tcmStatus.returncode}")
    # print(f"status_out: {' '.join(status_out)}")

    # tcm.terminate()
    # tcmStatus.wait()

    # assert tcm.pid is not None

    # assert tcm.poll() is not None
