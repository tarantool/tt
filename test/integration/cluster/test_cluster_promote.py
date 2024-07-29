import os

import pytest

from utils import (get_fixture_tcs_params, is_tarantool_ee,
                   is_tarantool_less_3, read_kv, run_command_and_get_output)

fixture_tcs_params = get_fixture_tcs_params(os.path.join(os.path.dirname(
                                            os.path.abspath(__file__)), "test_tcs_app"))


def to_etcd_key(key):
    return f"/prefix/config/{key}"


@pytest.mark.parametrize("instance_name, data_dir, err_text", [
    pytest.param(
        "etcd",
        "off_default",
        None,
        id="failover = off; default",
    ),
    pytest.param(
        "etcd",
        "off_multi",
        None,
        id="failover = off; multi",
    ),
    pytest.param(
        "etcd",
        "off_explicit",
        None,
        id="failover = off; explicit",
    ),
    pytest.param(
        "etcd",
        "off_no_diff",
        None,
        id="failover = off; nothing changes",
    ),
    pytest.param(
        "etcd",
        "manual_no_leader",
        None,
        id="failover = maual; no leader",
    ),
    pytest.param(
        "etcd",
        "manual",
        None,
        id="failover = manual; leader is set"
    ),
    pytest.param(
        "etcd",
        "election",
        'unsupported failover: "election", supported: "manual", "off"',
        id="failover = election",
    ),
    pytest.param(
        "etcd",
        "unknown",
        'unknown failover, supported: "manual", "off"',
        id="unknown failover",
    ),
    pytest.param(
        "etcd",
        "no_instance",
        'instance "instance-002" not found in the cluster configuration',
        id="unknown instance",
    ),
    pytest.param(
        "etcd",
        "many_replicasets",
        None,
        id="many replicasets",
    ),
    pytest.param(
        "tcs",
        "off_default",
        None,
        id="failover = off; default",
    ),
    pytest.param(
        "tcs",
        "off_multi",
        None,
        id="failover = off; multi",
    ),
    pytest.param(
        "tcs",
        "off_explicit",
        None,
        id="failover = off; explicit",
    ),
    pytest.param(
        "tcs",
        "off_no_diff",
        None,
        id="failover = off; nothing changes",
    ),
    pytest.param(
        "tcs",
        "manual_no_leader",
        None,
        id="failover = maual; no leader",
    ),
    pytest.param(
        "tcs",
        "manual",
        None,
        id="failover = manual; leader is set"
    ),
    pytest.param(
        "tcs",
        "election",
        'unsupported failover: "election", supported: "manual", "off"',
        id="failover = election",
    ),
    pytest.param(
        "tcs",
        "unknown",
        'unknown failover, supported: "manual", "off"',
        id="unknown failover",
    ),
    pytest.param(
        "tcs",
        "no_instance",
        'instance "instance-002" not found in the cluster configuration',
        id="unknown instance",
    ),
    pytest.param(
        "tcs",
        "many_replicasets",
        None,
        id="many replicasets",
    )
])
def test_cluster_promote_single_key(
    tt_cmd, tmpdir_with_cfg, data_dir, err_text, instance_name, fixture_params, request
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    test_data_dir = os.path.join(os.path.dirname(__file__), "testdata", "promote",
                                 "single_key", data_dir)
    kv = read_kv(test_data_dir)
    init_cfg = kv["init"]
    tmpdir = tmpdir_with_cfg
    conn = instance.conn()
    if instance_name == "etcd":
        conn.put("/prefix/config/all", init_cfg)
    else:
        conn.call("config.storage.put", "/prefix/config/all", init_cfg)
    creds = (
            f"{instance.connection_username}:{instance.connection_password}@"
            if instance_name == "tcs"
            else ""
        )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5"
    promote_cmd = [tt_cmd, "cluster", "rs", "promote", "-f", url, "instance-002"]
    rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)

    if err_text:
        assert rc != 0
        assert err_text in out
        return
    assert rc == 0
    assert 'Patching the config by the key: "/prefix/config/all"' in out

    expected = kv["expected"]
    content = ""
    if instance_name == "etcd":
        content, _ = conn.get("/prefix/config/all")
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", "/prefix/config/all")
        if len(content) > 0:
            content = content[0]["data"][0]["value"]
    assert content == expected


@pytest.mark.parametrize("instance_name, data_dir, exp_key, err_text", [
    pytest.param(
        "etcd",
        "off_lexi_order",
        "a",
        None,
        id="failover = off; lexi order",
    ),
    pytest.param(
        "etcd",
        "off_priority_order",
        "c",
        None,
        id="failover = off; priority order",
    ),
    pytest.param(
        "etcd",
        "manual_priority_order",
        "b",
        None,
        id="failover = manual; priority order",
    ),
    pytest.param(
        "etcd",
        "no_instance",
        None,
        'instance "instance-002" not found in the cluster configuration',
        id="instance not found among keys",
    ),
    pytest.param(
        "tcs",
        "off_lexi_order",
        "a",
        None,
        id="failover = off; lexi order",
    ),
    pytest.param(
        "tcs",
        "off_priority_order",
        "c",
        None,
        id="failover = off; priority order",
    ),
    pytest.param(
        "tcs",
        "manual_priority_order",
        "b",
        None,
        id="failover = manual; priority order",
    ),
    pytest.param(
        "tcs",
        "no_instance",
        None,
        'instance "instance-002" not found in the cluster configuration',
        id="instance not found among keys",
    ),
])
def test_cluster_promote_many_keys(
    tt_cmd,
    tmpdir_with_cfg,
    data_dir,
    exp_key,
    err_text,
    instance_name,
    fixture_params,
    request
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    conn = instance.conn()
    tmpdir = tmpdir_with_cfg
    test_data_dir = os.path.join(os.path.dirname(__file__), "testdata", "promote",
                                 "many_keys", data_dir)
    kvs = read_kv(test_data_dir)
    exp_config = None
    for k, v in kvs.items():
        if k != "expected":
            if instance_name == "etcd":
                conn.put(to_etcd_key(k), v)
            else:
                conn.call("config.storage.put", to_etcd_key(k), v)
        else:
            exp_config = v
    if not err_text:
        assert exp_config
        del kvs["expected"]

    creds = (
            f"{instance.connection_username}:{instance.connection_password}@"
            if instance_name == "tcs"
            else ""
        )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5"
    promote_cmd = [tt_cmd, "cluster", "rs", "promote", "-f", url, "instance-002"]

    rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)

    if err_text:
        assert rc != 0
        assert err_text in out
        return

    assert rc == 0
    assert f'Patching the config by the key: "{to_etcd_key(exp_key)}"' in out

    expected_kv = kvs
    expected_kv[exp_key] = exp_config

    for k, v in expected_kv.items():
        if instance_name == "etcd":
            content, _ = conn.get(to_etcd_key(k))
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", to_etcd_key(k))
            if len(content) > 0:
                content = content[0]["data"][0]["value"]
        assert content == v


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_promote_key_specified(tt_cmd,
                                       tmpdir_with_cfg,
                                       instance_name,
                                       fixture_params,
                                       request):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    conn = instance.conn()
    tmpdir = tmpdir_with_cfg
    test_data_dir = os.path.join(os.path.dirname(__file__), "testdata", "promote",
                                 "many_keys", "key_specified")
    kvs = read_kv(test_data_dir)
    exp_config = None
    for k, v in kvs.items():
        if k != "expected":
            if instance_name == "etcd":
                conn.put(to_etcd_key(k), v)
            else:
                conn.call("config.storage.put", to_etcd_key(k), v)
        else:
            exp_config = v
    assert exp_config
    del kvs["expected"]

    creds = (
            f"{instance.connection_username}:{instance.connection_password}@"
            if instance_name == "tcs"
            else ""
    )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix?key=b&timeout=5"
    promote_cmd = [tt_cmd, "cluster", "rs", "promote", "-f", url, "instance-002"]

    rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)
    assert rc == 0

    # Despite the fact that the config for key a is more prioritized,
    # b will be patched as it is explicitly specified.
    expected_key = to_etcd_key("b")
    assert f'Patching the config by the key: "{expected_key}"' in out

    expected_kv = kvs
    expected_kv["b"] = exp_config

    for k, v in expected_kv.items():
        content = ""
        if instance_name == "etcd":
            content, _ = conn.get(to_etcd_key(k))
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", to_etcd_key(k))
            if len(content) > 0:
                content = content[0]["data"][0]["value"]
        assert content == v


@pytest.mark.parametrize("instance_name, err_msg", [
    ("etcd", "⨯ failed to collect cluster config: " +
             "failed to fetch data from etcd: etcdserver: user name is empty"),
    ("tcs", "⨯ failed to collect cluster config: failed to fetch data from tarantool:" +
            " Execute access to function 'config.storage.get' is denied for user 'guest'")
])
def test_cluster_promote_no_auth(tt_cmd,
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
        url = f"{instance.endpoint}/prefix?timeout=5"
        promote_cmd = [tt_cmd, "cluster", "rs", "promote", url, "instance-002"]
        rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)
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
def test_cluster_promote_bad_auth(tt_cmd,
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
        promote_cmd = [tt_cmd, "cluster", "rs", "promote", url, "instance-002"]
        rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)
        assert rc != 0
        assert err_msg in out
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


test_auth_cfg_init = """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002: {}
"""

test_auth_cfg_expected = """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002:
            database:
              mode: rw
"""


@pytest.mark.parametrize("instance_name, auth", [
    ("etcd", "url"),
    ("etcd", "flag"),
    ("etcd", "env"),
    ("tcs", "url"),
    ("tcs", "flag"),
    ("tcs", "env"),
])
def test_cluster_promote_auth(tt_cmd,
                              tmpdir_with_cfg,
                              instance_name,
                              auth,
                              fixture_params,
                              request):
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
        conn.put(key, test_auth_cfg_init)
    else:
        conn.call("config.storage.put", key, test_auth_cfg_init)
    try:
        if instance_name == "etcd":
            instance.enable_auth()

        if auth == "url":
            env = None
            url = (
                f"http://{instance.connection_username}:{instance.connection_password}@"
                f"{instance.host}:{instance.port}/prefix?timeout=5"
            )
            promote_cmd = [tt_cmd, "cluster", "rs", "promote", "-f", url, "instance-002"]
        elif auth == "flag":
            env = None
            url = f"{instance.endpoint}/prefix?timeout=5"
            promote_cmd = [tt_cmd, "cluster", "rs", "promote", "-f",
                           "-u", instance.connection_username,
                           "-p", instance.connection_password,
                           url, "instance-002"]
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
            promote_cmd = [tt_cmd, "cluster", "rs", "promote", "-f", url, "instance-002"]

        rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir, env=env)
        assert rc == 0
        assert f'Patching the config by the key: "{key}"' in out

        if instance_name == "etcd":
            instance.disable_auth()
        conn = instance.conn()

        content = ""
        if instance_name == "etcd":
            content, _ = conn.get(to_etcd_key("all"))
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", key)
            if len(content) > 0:
                content = content[0]["data"][0]["value"]
        assert test_auth_cfg_expected == content

    finally:
        if instance_name == "etcd":
            instance.disable_auth()
