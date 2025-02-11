import os
from pathlib import Path
from signal import SIGQUIT
from subprocess import PIPE, STDOUT, Popen, run

import pytest

from utils import wait_for_lines_in_output


@pytest.fixture(scope="session")
def certificates(tmp_path_factory: pytest.TempPathFactory) -> dict[str, Path]:
    dir = tmp_path_factory.mktemp("aeon_cert")
    cmd = (Path(__file__).parent / "generate-keys.sh", dir)
    returncode = run(cmd).returncode
    assert returncode == 0, "Some error on generate certificates"
    cert = {
        "ca": dir / "ca.crt",
        "s_private": dir / "server.key",
        "s_public": dir / "server.crt",
        "c_private": dir / "client.key",
        "c_public": dir / "client.crt",
    }
    for k, v in cert.items():
        assert v.exists(), f"Not found {k} certificate"
    return cert


@pytest.fixture(scope="session")
def mock_aeon(tmp_path_factory) -> Path:
    server_dir = Path(__file__).parent / "server"
    exec = tmp_path_factory.mktemp("aeon_mock") / "aeon"
    result = run(f"go build -C {server_dir} -o {exec}".split())
    assert result.returncode == 0, "Failed build mock aeon server"
    return exec


@pytest.fixture(params=[50052, "@aeon_unix_socket", "AEON"])
def aeon_plain(mock_aeon, tmp_path, request):
    cmd = [mock_aeon]
    param = request.param
    if isinstance(param, int):
        cmd.append(f"-port={param}")
    elif isinstance(param, str):
        if param[0] != "@":
            param = tmp_path / param
        cmd.append(f"-unix={param}")

    aeon = Popen(
        cmd,
        env=dict(os.environ, GRPC_GO_LOG_SEVERITY_LEVEL="info"),
        stderr=STDOUT,
        stdout=PIPE,
        text=True,
    )
    print(wait_for_lines_in_output(aeon.stdout, ["ListenSocket created"]))
    yield param
    aeon.send_signal(SIGQUIT)
    assert aeon.wait(5) == 0, "Mock aeon server didn't stopped properly"


@pytest.fixture(params=[50052])
def aeon_plain_file(mock_aeon, request):
    cmd = [mock_aeon, f"-port={request.param}"]

    aeon = Popen(
        cmd,
        env=dict(os.environ, GRPC_GO_LOG_SEVERITY_LEVEL="info"),
        stderr=STDOUT,
        stdout=PIPE,
        text=True,
    )
    print(wait_for_lines_in_output(aeon.stdout, ["ListenSocket created"]))
    yield request.param

    aeon.send_signal(SIGQUIT)
    assert aeon.wait(5) == 0, "Mock aeon server didn't stopped properly"


@pytest.fixture(params=["server-side", "mutual-tls"])
def aeon_ssl(mock_aeon, certificates, request):
    cmd = [
        mock_aeon,
        "-ssl",
        f"-key={certificates['s_private']}",
        f"-cert={certificates['s_public']}",
    ]
    mode = request.param
    if mode == "mutual-tls":
        cmd.append(f"-ca={certificates['ca']}")
    elif mode == "server-side":
        pass
    else:
        assert False, "Unsupported TLS mode"

    aeon = Popen(
        cmd,
        env=dict(os.environ, GRPC_GO_LOG_SEVERITY_LEVEL="info"),
        stderr=STDOUT,
        stdout=PIPE,
        text=True,
    )
    print(wait_for_lines_in_output(aeon.stdout, ["ListenSocket created"]))
    yield mode
    aeon.send_signal(SIGQUIT)
    assert aeon.wait(5) == 0, "Mock aeon ssl server didn't stopped properly"
