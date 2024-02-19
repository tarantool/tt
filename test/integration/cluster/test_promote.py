import os

import pytest
from helpers import etcd_password, etcd_username, read_kv, to_etcd_key

from utils import run_command_and_get_output


@pytest.mark.parametrize("data_dir, err_text", [
    pytest.param(
        "off_default",
        None,
        id="failover = off; default",
    ),
    pytest.param(
        "off_multi",
        None,
        id="failover = off; multi",
    ),
    pytest.param(
        "off_explicit",
        None,
        id="failover = off; explicit",
    ),
    pytest.param(
        "off_no_diff",
        None,
        id="failover = off; nothing changes",
    ),
    pytest.param(
        "manual_no_leader",
        None,
        id="failover = maual; no leader",
    ),
    pytest.param(
        "manual",
        None,
        id="failover = manual; leader is set"
    ),
    pytest.param(
        "election",
        'unsupported failover: "election", supported: "manual", "off"',
        id="failover = election",
    ),
    pytest.param(
        "unknown",
        'unknown failover, supported: "manual", "off"',
        id="unknown failover",
    ),
    pytest.param(
        "no_instance",
        'instance "instance-002" not found in the cluster configuration',
        id="unknown instance",
    ),
    pytest.param(
        "many_replicasets",
        None,
        id="many replicasets",
    )
])
def test_promote_single_key(
    tt_cmd, etcd, tmpdir_with_cfg, data_dir, err_text,
):
    test_data_dir = os.path.join(os.path.dirname(__file__), "testdata", "promote",
                                 "single_key", data_dir)
    kv = read_kv(test_data_dir)
    init_cfg = kv["init"]
    tmpdir = tmpdir_with_cfg
    etcdcli = etcd.conn()
    etcdcli.put("/prefix/config/all", init_cfg)
    url = f"{etcd.endpoint}/prefix?timeout=5"
    promote_cmd = [tt_cmd, "cluster", "rs", "promote", "-f", url, "instance-002"]
    rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)

    if err_text:
        assert rc != 0
        assert err_text in out
        return
    assert rc == 0
    assert 'Patch the config by the key: "/prefix/config/all"' in out

    expected = kv["expected"]
    actual, _ = etcdcli.get("/prefix/config/all")
    assert expected == actual.decode("utf-8")


@pytest.mark.parametrize("data_dir, exp_key, err_text", [
    pytest.param(
        "off_lexi_order",
        "a",
        None,
        id="failover = off; lexi order",
    ),
    pytest.param(
        "off_priority_order",
        "c",
        None,
        id="failover = off; priority order",
    ),
    pytest.param(
        "manual_priority_order",
        "b",
        None,
        id="failover = manual; priority order",
    ),
    pytest.param(
        "no_instance",
        None,
        'instance "instance-002" not found in the cluster configuration',
        id="instance not found among keys",
    )
])
def test_promote_many_keys(
    tt_cmd, etcd,
    tmpdir_with_cfg,
    data_dir,
    exp_key,
    err_text,
):
    etcdcli = etcd.conn()
    tmpdir = tmpdir_with_cfg
    test_data_dir = os.path.join(os.path.dirname(__file__), "testdata", "promote",
                                 "many_keys", data_dir)
    kvs = read_kv(test_data_dir)
    exp_config = None
    for k, v in kvs.items():
        if k != "expected":
            etcdcli.put(to_etcd_key(k), v)
        else:
            exp_config = v
    if not err_text:
        assert exp_config
        del kvs["expected"]

    url = f"{etcd.endpoint}/prefix?timeout=5"
    promote_cmd = [tt_cmd, "cluster", "rs", "promote", "-f", url, "instance-002"]

    rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)

    if err_text:
        assert rc != 0
        assert err_text in out
        return

    assert rc == 0
    assert f'Patch the config by the key: "{to_etcd_key(exp_key)}"' in out

    expected_kv = kvs
    expected_kv[exp_key] = exp_config

    for k, v in expected_kv.items():
        actual, _ = etcdcli.get(to_etcd_key(k))
        assert v == actual.decode("utf-8")


def test_promote_key_specified(tt_cmd, etcd, tmpdir_with_cfg):
    etcdcli = etcd.conn()
    tmpdir = tmpdir_with_cfg
    test_data_dir = os.path.join(os.path.dirname(__file__), "testdata", "promote",
                                 "many_keys", "key_specified")
    kvs = read_kv(test_data_dir)
    exp_config = None
    for k, v in kvs.items():
        if k != "expected":
            etcdcli.put(to_etcd_key(k), v)
        else:
            exp_config = v
    assert exp_config
    del kvs["expected"]

    url = f"{etcd.endpoint}/prefix?key=b&timeout=5"
    promote_cmd = [tt_cmd, "rs", "cs", "promote", "-f", url, "instance-002"]

    rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)
    assert rc == 0

    # Despite the fact that the config for key a is more prioritized,
    # b will be patched as it is explicitly specified.
    expected_key = to_etcd_key("b")
    assert f'Patch the config by the key: "{expected_key}"' in out

    expected_kv = kvs
    expected_kv["b"] = exp_config

    for k, v in expected_kv.items():
        actual, _ = etcdcli.get(to_etcd_key(k))
        assert v == actual.decode("utf-8")


def test_promote_cconfig_source_no_auth(tt_cmd, etcd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg

    try:
        etcd.enable_auth()
        url = f"{etcd.endpoint}/prefix?timeout=5"
        promote_cmd = [tt_cmd, "rs", "cs", "promote", url, "instance-002"]
        rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)
        assert rc != 0
        expected = (r"   ⨯ failed to collect cluster config: " +
                    "failed to fetch data from etcd: etcdserver: user name is empty")
        assert expected in out
    finally:
        etcd.disable_auth()


def test_promote_cconfig_source_bad_auth(tt_cmd, etcd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg

    try:
        etcd.enable_auth()
        url = f"http://invalid_user:invalid_pass@{etcd.host}:{etcd.port}/prefix?timeout=5"
        promote_cmd = [tt_cmd, "rs", "cs", "promote", url, "instance-002"]
        rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)
        assert rc != 0
        expected = (r"failed to connect to etcd: " +
                    "etcdserver: authentication failed, invalid user ID or password")
        assert expected in out
    finally:
        etcd.disable_auth()


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


@pytest.mark.parametrize('auth', ["url", "flag", "env"])
def test_promote_cconfig_source_auth(tt_cmd, etcd, tmpdir_with_cfg, auth):
    tmpdir = tmpdir_with_cfg
    etcdcli = etcd.conn()
    key = to_etcd_key("all")
    etcdcli.put(key, test_auth_cfg_init)
    try:
        etcd.enable_auth()

        if auth == "url":
            env = None
            url = f"http://{etcd_username}:{etcd_password}@{etcd.host}:{etcd.port}/prefix?timeout=5"
            promote_cmd = [tt_cmd, "rs", "cs", "promote", "-f", url, "instance-002"]
        elif auth == "flag":
            env = None
            url = f"{etcd.endpoint}/prefix?timeout=5"
            promote_cmd = [tt_cmd, "rs", "cs", "promote", "-f",
                           "-u", etcd_username,
                           "-p", etcd_password,
                           url, "instance-002"]
        elif auth == "env":
            env = {"TT_CLI_ETCD_USERNAME": etcd_username,
                   "TT_CLI_ETCD_PASSWORD": etcd_password}
            url = f"{etcd.endpoint}/prefix?timeout=5"
            promote_cmd = [tt_cmd, "rs", "cs", "promote", "-f", url, "instance-002"]

        rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir, env=env)
        assert rc == 0
        assert f'Patch the config by the key: "{key}"' in out

        etcd.disable_auth()
        etcdcli = etcd.conn()
        actual, _ = etcdcli.get(to_etcd_key("all"))
        assert test_auth_cfg_expected == actual.decode("utf-8")

    finally:
        etcd.disable_auth()
