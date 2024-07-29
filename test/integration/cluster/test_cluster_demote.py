import pytest
from tarantool.connection import os

from utils import (get_fixture_tcs_params, is_tarantool_ee,
                   is_tarantool_less_3, run_command_and_get_output)

fixture_tcs_params = get_fixture_tcs_params(os.path.join(os.path.dirname(
                                            os.path.abspath(__file__)), "test_tcs_app"))


def to_etcd_key(key):
    return f"/prefix/config/{key}"


cfg1 = """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            database:
              mode: rw
"""

cfg2 = """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-002:
            database:
              mode: rw
"""


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_demote_single_key(tt_cmd, tmpdir_with_cfg, instance_name, request, fixture_params):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    conn = instance.conn()
    key = to_etcd_key("all")
    if instance_name == "etcd":
        conn.put(key, cfg1)
    else:
        conn.call("config.storage.put", key, cfg1)
    creds = (
            f"{instance.connection_username}:{instance.connection_password}@"
            if instance_name == "tcs"
            else ""
    )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5"
    demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f", url, "instance-001"]
    rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
    assert rc == 0
    assert f'Patching the config by the key: "{key}"' in out

    content = ""
    if instance_name == "etcd":
        content, _ = conn.get(key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]
    assert content == """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            database:
              mode: ro
"""


@pytest.mark.parametrize("instance_name, key", [
    ("etcd", None),
    ("etcd", "b"),
    ("tcs", None),
    ("tcs", "b"),
])
def test_cluster_demote_many_keys(tt_cmd,
                                  tmpdir_with_cfg,
                                  key,
                                  instance_name,
                                  request,
                                  fixture_params):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    conn = instance.conn()
    a_key = to_etcd_key("a")
    b_key = to_etcd_key("b")
    if instance_name == "etcd":
        conn.put(a_key, cfg1)
        conn.put(b_key, cfg2)
    else:
        conn.call("config.storage.put", a_key, cfg1)
        conn.call("config.storage.put", b_key, cfg2)
    creds = (
            f"{instance.connection_username}:{instance.connection_password}@"
            if instance_name == "tcs"
            else ""
        )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5"
    if key:
        url = f"{url}&key={key}"
    demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f", url, "instance-002"]
    rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
    assert rc == 0
    assert f'Patching the config by the key: "{b_key}"' in out

    content = ""
    if instance_name == "etcd":
        content, _ = conn.get(a_key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", a_key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]
    assert content == cfg1  # Nothing was changed.

    if instance_name == "etcd":
        content, _ = conn.get(b_key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", b_key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]
    assert content == """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-002:
            database:
              mode: ro
"""


@pytest.mark.parametrize("instance_name, err_msg", [
    ("etcd", "⨯ failed to collect cluster config: " +
             "failed to fetch data from etcd: etcdserver: user name is empty"),
    ("tcs", "⨯ failed to collect cluster config: failed to fetch data from tarantool:" +
            " Execute access to function 'config.storage.get' is denied for user 'guest'")
])
def test_cluster_demote_no_auth(tt_cmd,
                                tmpdir_with_cfg,
                                instance_name,
                                fixture_params,
                                request,
                                err_msg):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    try:
        if instance_name == "etcd":
            instance.enable_auth()
        url = f"{instance.endpoint}/prefix?timeout=5"
        demote_cmd = [tt_cmd, "cluster", "rs", "demote", url, "instance-002"]
        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
        assert rc != 0
        assert err_msg in out
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name, err_msg", [
    ("etcd", "failed to connect to etcd: " +
             "etcdserver: authentication failed, invalid user ID or password"),
    ("tcs", "failed to establish a connection to tarantool or etcd:" +
            " failed to connect to tarantool: failed to authenticate:")
])
def test_cluster_demote_bad_auth(tt_cmd,
                                 tmpdir_with_cfg,
                                 instance_name,
                                 err_msg,
                                 fixture_params,
                                 request):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    try:
        if instance_name == "etcd":
            instance.enable_auth()
        url = f"http://invalid_user:invalid_pass@{instance.host}:{instance.port}/prefix?timeout=5"
        demote_cmd = [tt_cmd, "cluster", "rs", "demote", url, "instance-002"]
        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
        assert rc != 0
        # expected = (r"failed to connect to etcd: " +
        #             "etcdserver: authentication failed, invalid user ID or password")
        assert err_msg in out
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name, auth", [
    ("etcd", "url"),
    ("etcd", "flag"),
    ("etcd", "env"),
    ("tcs", "url"),
    ("tcs", "flag"),
    ("tcs", "env"),
])
def test_cluster_demote_auth(tt_cmd, tmpdir_with_cfg, instance_name, auth, fixture_params, request):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    conn = instance.conn()
    key = to_etcd_key("all")
    if instance_name == "etcd":
        conn.put(key, cfg1)
    else:
        conn.call("config.storage.put", key, cfg1)
    try:
        if instance_name == "etcd":
            instance.enable_auth()

        if auth == "url":
            env = None
            url = (
                f"http://{instance.connection_username}:{instance.connection_password}@"
                f"{instance.host}:{instance.port}/prefix?timeout=5"
            )
            demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f", url, "instance-001"]
        elif auth == "flag":
            env = None
            url = f"{instance.endpoint}/prefix?timeout=5"
            demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f",
                          "-u", instance.connection_username,
                          "-p", instance.connection_password,
                          url, "instance-001"]
        elif auth == "env":
            env = {
                (
                    "TT_CLI_ETCD_USERNAME"
                    if instance_name == "etcd"
                    else "TT_CLI_USERNAME"
                ): instance.connection_username,
                (
                    "TT_CLI_ETCD_PASSWORD"
                    if instance_name == "etcd"
                    else "TT_CLI_PASSWORD"
                ): instance.connection_password,
            }
            url = f"{instance.endpoint}/prefix?timeout=5"
            demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f", url, "instance-001"]

        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir, env=env)
        assert rc == 0
        assert f'Patching the config by the key: "{key}"' in out

        if instance_name == "etcd":
            instance.disable_auth()

        conn = instance.conn()
        content = ""
        if instance_name == "etcd":
            content, _ = conn.get(key)
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", key)
            if len(content) > 0:
                content = content[0]["data"][0]["value"]
        assert content == """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            database:
              mode: ro
"""

    finally:
        if instance_name == "etcd":
            instance.disable_auth()
