import pytest
from tarantool.connection import os

from utils import (
    get_fixture_tcs_params,
    is_tarantool_ee,
    is_tarantool_less_3,
    run_command_and_get_output,
)

fixture_tcs_params = get_fixture_tcs_params(
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "test_tcs_app"),
)


def to_etcd_key(key):
    return f"/prefix/config/{key}"


def test_cluster_expel_missing_instance_arg(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    uri = "http://localhost:2379"  # Fictive.
    cmd = [tt_cmd, "cluster", "rs", "expel", uri]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc != 0
    assert "Error: accepts 2 arg(s), received 1" in out


cfg = """\
groups:
  group-1:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002: {}
"""


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_expel_no_instance(tt_cmd, tmpdir_with_cfg, instance_name, fixture_params, request):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)

    conn = instance.conn()
    tmpdir = tmpdir_with_cfg

    key = to_etcd_key("all")
    if instance_name == "etcd":
        conn.put(key, cfg)
    else:
        conn.call("config.storage.put", key, cfg)

    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5"
    cmd = [tt_cmd, "cluster", "rs", "expel", "-f", url, "instance-003"]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc != 0
    assert 'instance "instance-003" not found in the cluster configuration' in out


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_expel_single_key(tt_cmd, tmpdir_with_cfg, instance_name, fixture_params, request):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    conn = instance.conn()
    tmpdir = tmpdir_with_cfg
    key = to_etcd_key("all")
    if instance_name == "etcd":
        conn.put(key, cfg)
    else:
        conn.call("config.storage.put", key, cfg)
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5"
    cmd = [tt_cmd, "cluster", "rs", "expel", "-f", url, "instance-002"]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert f'Patching the config by the key: "{key}"' in out

    if instance_name == "etcd":
        content, _ = conn.get(key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]

    assert (
        content
        == """\
groups:
  group-1:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002:
            iproto:
              listen: {}
"""
    )
