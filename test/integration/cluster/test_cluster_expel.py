from utils import run_command_and_get_output


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


def test_cluster_expel_no_instance(tt_cmd, etcd, tmpdir_with_cfg):
    etcdcli = etcd.conn()
    tmpdir = tmpdir_with_cfg
    etcdcli.put(to_etcd_key("all"), cfg)
    url = f"{etcd.endpoint}/prefix?timeout=5"
    cmd = [tt_cmd, "cluster", "rs", "expel", "-f", url, "instance-003"]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc != 0
    assert 'instance "instance-003" not found in the cluster configuration' in out


def test_cluster_expel_single_key(tt_cmd, etcd, tmpdir_with_cfg):
    etcdcli = etcd.conn()
    tmpdir = tmpdir_with_cfg
    key = to_etcd_key("all")
    etcdcli.put(key, cfg)
    url = f"{etcd.endpoint}/prefix?timeout=5"
    cmd = [tt_cmd, "cluster", "rs", "expel", "-f", url, "instance-002"]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert f'Patching the config by the key: "{key}"' in out

    actual, _ = etcdcli.get(key)
    assert actual.decode("utf-8") == """\
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
