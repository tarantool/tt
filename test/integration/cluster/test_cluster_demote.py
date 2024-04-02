import pytest
from etcd_helper import etcd_password, etcd_username

from utils import run_command_and_get_output


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


def test_cluster_demote_single_key(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    etcdcli = etcd.conn()
    key = to_etcd_key("all")
    etcdcli.put(key, cfg1)
    url = f"{etcd.endpoint}/prefix?timeout=5"
    demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f", url, "instance-001"]
    rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
    assert rc == 0
    assert f'Patching the config by the key: "{key}"' in out

    actual, _ = etcdcli.get(key)
    actual = actual.decode("utf-8")
    assert actual == """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            database:
              mode: ro
"""


@pytest.mark.parametrize("key", [None, "b"])
def test_cluster_demote_many_keys(tt_cmd, tmpdir_with_cfg, etcd, key):
    tmpdir = tmpdir_with_cfg
    etcdcli = etcd.conn()
    a_key = to_etcd_key("a")
    b_key = to_etcd_key("b")
    etcdcli.put(a_key, cfg1)
    etcdcli.put(b_key, cfg2)
    url = f"{etcd.endpoint}/prefix?timeout=5"
    if key:
        url = f"{url}&key={key}"
    demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f", url, "instance-002"]
    rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
    assert rc == 0
    assert f'Patching the config by the key: "{b_key}"' in out

    actual, _ = etcdcli.get(a_key)
    actual = actual.decode("utf-8")
    assert actual == cfg1  # Nothing was changed.

    actual, _ = etcdcli.get(b_key)
    actual = actual.decode("utf-8")
    assert actual == """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-002:
            database:
              mode: ro
"""


def test_cluster_demote_no_auth(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    try:
        etcd.enable_auth()
        url = f"{etcd.endpoint}/prefix?timeout=5"
        demote_cmd = [tt_cmd, "cluster", "rs", "demote", url, "instance-002"]
        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
        assert rc != 0
        expected = (r"   ⨯ failed to collect cluster config: " +
                    "failed to fetch data from etcd: etcdserver: user name is empty")
        assert expected in out
    finally:
        etcd.disable_auth()


def test_cluster_demote_bad_auth(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    try:
        etcd.enable_auth()
        url = f"http://invalid_user:invalid_pass@{etcd.host}:{etcd.port}/prefix?timeout=5"
        demote_cmd = [tt_cmd, "cluster", "rs", "demote", url, "instance-002"]
        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
        assert rc != 0
        expected = (r"failed to connect to etcd: " +
                    "etcdserver: authentication failed, invalid user ID or password")
        assert expected in out
    finally:
        etcd.disable_auth()


@pytest.mark.parametrize("auth", ["url", "flag", "env"])
def test_cluster_demote_auth(tt_cmd, tmpdir_with_cfg, etcd, auth):
    tmpdir = tmpdir_with_cfg
    etcdcli = etcd.conn()
    key = to_etcd_key("all")
    etcdcli.put(key, cfg1)
    try:
        etcd.enable_auth()

        if auth == "url":
            env = None
            url = f"http://{etcd_username}:{etcd_password}@{etcd.host}:{etcd.port}/prefix?timeout=5"
            demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f", url, "instance-001"]
        elif auth == "flag":
            env = None
            url = f"{etcd.endpoint}/prefix?timeout=5"
            demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f",
                          "-u", etcd_username,
                          "-p", etcd_password,
                          url, "instance-001"]
        elif auth == "env":
            env = {"TT_CLI_ETCD_USERNAME": etcd_username,
                   "TT_CLI_ETCD_PASSWORD": etcd_password}
            url = f"{etcd.endpoint}/prefix?timeout=5"
            demote_cmd = [tt_cmd, "cluster", "rs", "demote", "-f", url, "instance-001"]

        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir, env=env)
        assert rc == 0
        assert f'Patching the config by the key: "{key}"' in out

        etcd.disable_auth()
        etcdcli = etcd.conn()
        actual, _ = etcdcli.get(to_etcd_key("all"))
        actual = actual.decode("utf-8")
        assert actual == """\
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
        etcd.disable_auth()
