"""An Enterprise config storage instance with a TLS-only iproto listener."""

import shutil
import subprocess
from pathlib import Path
from types import SimpleNamespace
from urllib.parse import urlencode

import pytest
import yaml

from utils import is_tarantool_ee, wait_event


@pytest.fixture
def tls_cluster(tmp_path, port_factory):
    if not is_tarantool_ee():
        pytest.skip("iproto TLS requires Tarantool Enterprise")
    certificates = Path(__file__).parent / "integration" / "connect" / "test_ssl_app"
    for name in ["ca.crt", "localhost.crt", "localhost.key"]:
        shutil.copy2(certificates / name, tmp_path / name)
    shutil.copy2(
        Path(__file__).parent / "integration" / "backup" / "ssl_app" / "unrelated_ca.crt",
        tmp_path / "wrong-ca.crt",
    )
    address = f"localhost:{port_factory()}"
    params = {
        "transport": "ssl",
        "ssl_key_file": str(tmp_path / "localhost.key"),
        "ssl_cert_file": str(tmp_path / "localhost.crt"),
        "ssl_ca_file": str(tmp_path / "ca.crt"),
    }
    config = {
        "credentials": {"users": {"client": {"password": "secret", "roles": ["super"]}}},
        "database": {"use_mvcc_engine": True},
        "replication": {"failover": "election"},
        "iproto": {"listen": [{"uri": address, "params": params}]},
        "groups": {
            "g": {
                "replicasets": {
                    "tls-rs": {
                        "roles": ["config.storage"],
                        "instances": {"tls-storage": {}},
                    },
                },
            },
        },
    }
    path = tmp_path / "config.yaml"
    path.write_text(yaml.safe_dump(config))
    log_path = tmp_path / "tarantool.log"
    with log_path.open("w") as log:
        process = subprocess.Popen(
            ["tarantool", "--name", "tls-storage", "--config", str(path)],
            cwd=tmp_path,
            stdout=log,
            stderr=subprocess.STDOUT,
        )
        try:

            def started():
                if process.poll() is not None:
                    pytest.fail(log_path.read_text())
                return "entering the event loop" in log_path.read_text()

            assert wait_event(10, started), log_path.read_text()
            yield SimpleNamespace(
                directory=tmp_path,
                uri=f"client:secret@{address}",
                storage_url=f"tarantool://client:secret@{address}/tls?" + urlencode(params),
                ssl_args=[
                    "--sslkeyfile",
                    str(tmp_path / "localhost.key"),
                    "--sslcertfile",
                    str(tmp_path / "localhost.crt"),
                    "--sslcafile",
                    str(tmp_path / "ca.crt"),
                ],
            )
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
