
from subprocess import PIPE, Popen

from utils import skip_if_tarantool_ce, wait_for_lines_in_output

TcmStartCommand = ("tcm", "start")
TcmStartWatchdogCommand = ("tcm", "start", "--watchdog")


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

    tcm.terminate()
    tcm.wait()

    assert tcm.poll() is not None
