import subprocess

import pytest


def compose_switch_cmd(timeout=30):
    return f"""command: switch
new_master: some_instance
timeout: {timeout}
"""


@pytest.mark.parametrize("config_storage_type", ["etcd", "tcs"])
@pytest.mark.parametrize(
    "extra_args, switch_cmd",
    [
        pytest.param([], compose_switch_cmd(), id="default"),
        pytest.param(["--timeout", "42"], compose_switch_cmd(42), id="timeout"),
    ],
)
def test_cluster_failover_switch(
    tt_cmd,
    request,
    tmpdir_with_cfg,
    config_storage_type,
    extra_args,
    switch_cmd,
):
    config_storage = request.getfixturevalue(config_storage_type)
    tmpdir = tmpdir_with_cfg

    creds = f"{config_storage.connection_username}:{config_storage.connection_password}@"
    uri = f"http://{creds}{config_storage.host}:{config_storage.port}/prefix"
    cmd = [
        tt_cmd,
        "cluster",
        "failover",
        "switch",
        uri,
        "some_instance",
        *extra_args,
    ]
    p = subprocess.run(
        cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    print(p.stdout)
    assert "To check the switching status, run" in p.stdout
    assert f"tt cluster failover switch-status '{uri}'" in p.stdout

    task_id = p.stdout.split(" ")[-1]
    task_id = "/prefix/failover/command/" + task_id.strip()

    conn = config_storage.conn()
    if config_storage_type == "etcd":
        content, _ = conn.get(task_id)
        content = content.decode("utf-8")
    elif config_storage_type == "tcs":
        content = conn.call("config.storage.get", task_id)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]
    else:
        assert False, "Unreachable code"

    assert content == switch_cmd


@pytest.mark.parametrize("config_storage_type", ["etcd", "tcs"])
def test_cluster_failover_switch_status(tt_cmd, request, tmpdir_with_cfg, config_storage_type):
    config_storage = request.getfixturevalue(config_storage_type)
    tmpdir = tmpdir_with_cfg

    creds = f"{config_storage.connection_username}:{config_storage.connection_password}@"
    uri = f"http://{creds}{config_storage.host}:{config_storage.port}/prefix"
    cmd = [
        tt_cmd,
        "cluster",
        "failover",
        "switch",
        uri,
        "some_instance",
    ]
    p = subprocess.run(
        cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    assert "To check the switching status, run" in p.stdout
    assert f"tt cluster failover switch-status '{uri}'" in p.stdout

    task_id = p.stdout.split(" ")[-1].strip()

    status_cmd = [
        tt_cmd,
        "cluster",
        "failover",
        "switch-status",
        uri,
        task_id,
    ]
    p = subprocess.run(
        status_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    assert p.stdout == compose_switch_cmd()


@pytest.mark.parametrize("config_storage_type", ["etcd", "tcs"])
def test_cluster_failover_switch_wait_timeout(
    tt_cmd,
    request,
    tmpdir_with_cfg,
    config_storage_type,
):
    config_storage = request.getfixturevalue(config_storage_type)
    tmpdir = tmpdir_with_cfg

    creds = f"{config_storage.connection_username}:{config_storage.connection_password}@"
    uri = f"http://{creds}{config_storage.host}:{config_storage.port}/prefix"
    cmd = [
        tt_cmd,
        "cluster",
        "failover",
        "switch",
        uri,
        "some_instance",
        "--timeout",
        "1",
        "--wait",
    ]
    p = subprocess.run(
        cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    print(p.stdout)
    assert "Timeout for command execution reached" in p.stdout


@pytest.mark.slow
@pytest.mark.parametrize(
    "config_storage_type",
    [
        "etcd",
        pytest.param("tcs", marks=pytest.mark.skip(reason="manual failover is not supported yet")),
    ],
)
@pytest.mark.parametrize(
    "extra_args",
    [
        pytest.param([], id="default"),
        pytest.param(["--timeout", "42"], id="timeout"),
    ],
)
def test_cluster_failover_switch_wait(
    tt,
    extra_args,
    cluster_supervised,
):
    cs = cluster_supervised.config_storage
    creds = f"{cs.connection_username}:{cs.connection_password}@"
    uri = f"http://{creds}{cs.host}:{cs.port}/prefix"

    p = tt.run(
        "cluster",
        "failover",
        "switch",
        uri,
        "replicaset-001-c",
        "--wait",
        *extra_args,
    )
    assert p.returncode == 0
    assert "Timeout for command execution reached" not in p.stdout
