import pytest

from utils import run_command_and_get_output

pytestmark = pytest.mark.tls


def test_replicaset_status_tls(tt_cmd, tls_cluster):
    rc, out = run_command_and_get_output(
        [tt_cmd, "replicaset", "status", tls_cluster.uri, *tls_cluster.ssl_args],
        cwd=tls_cluster.directory,
    )
    assert rc == 0, out
    assert "Orchestrator:      centralized config" in out
    assert "tls-rs" in out
    assert "tls-storage" in out


def test_replicaset_tls_rejects_wrong_ca(tt_cmd, tls_cluster):
    args = [*tls_cluster.ssl_args[:-1], str(tls_cluster.directory / "wrong-ca.crt")]
    rc, out = run_command_and_get_output(
        [tt_cmd, "replicaset", "status", tls_cluster.uri, *args],
        cwd=tls_cluster.directory,
    )
    assert rc != 0, out
    assert "certificate" in out.lower(), out
