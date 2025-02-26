
from subprocess import PIPE, Popen

from utils import skip_if_tarantool_ce

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

    while True:
        output = tcm.stdout.readline().strip()

        if not output and tcm.poll() is not None:
            break
        if "TCM_CLUSTER_CONNECTION_RATE_LIMIT" in output:
            tcm.terminate()
            tcm.wait()
            break
        if output:
            print(output)

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

    while True:
        output = tcm.stdout.readline().strip()

        if not output and tcm.poll() is not None:
            break
        if "connecting to storage..." in output:
            tcm.terminate()
            tcm.wait()
            break
        if output:
            print(output)

    assert tcm.pid is not None

    tcm.terminate()
    tcm.wait()

    assert tcm.poll() is not None
