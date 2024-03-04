import os
import shutil
import subprocess

import pytest
from etcd_helper import etcd_password, etcd_username


def copy_app(tmpdir, app_name):
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)


valid_cluster_cfg = r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: rw
            iproto:
              listen:
                - uri: 127.0.0.1:3301
"""

invalid_cluster_cfg = r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: any
            iproto:
              listen:
                - uri: 127.0.0.1:3301
"""

valid_instance_cfg = r"""database:
  mode: rw
iproto:
  listen:
    - uri: 127.0.0.1:3303
"""

invalid_instance_cfg = r"""database:
  mode: any
iproto:
  listen:
    - uri: 127.0.0.1:3303
"""


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_no_configuration(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    publish_cmd = [tt_cmd, "cluster", "publish", app_name, "not_exist.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    expected = (r'   ⨯ failed to read path "not_exist.yaml": ' +
                'open not_exist.yaml: no such file or directory')
    assert expected in publish_output


def test_cluster_publish_no_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish", "non_exist", "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert "⨯ can\'t collect instance information for non_exist:" in publish_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_valid_cluster(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    app_path = os.path.join(tmpdir, app_name)
    dst_cfg_path = os.path.join(app_path, "config.yaml")

    publish_cmd = [tt_cmd, "cluster", "publish", app_name, "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(dst_cfg_path, 'r') as f:
        uploaded = f.read()

    assert valid_cluster_cfg == uploaded


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_valid_cluster_without_app_config(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    app_path = os.path.join(tmpdir, app_name)
    dst_cfg_path = os.path.join(app_path, "config.yaml")
    os.remove(dst_cfg_path)
    publish_cmd = [tt_cmd, "cluster", "publish", app_name, "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(dst_cfg_path, 'r') as f:
        uploaded = f.read()

    assert valid_cluster_cfg == uploaded


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_invalid_cluster(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_cluster_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish", app_name, "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    expected = ('   ⨯ an invalid instance "master" configuration:' +
                ' invalid path "database.mode":' +
                ' value "any" should be one of [ro rw]\n')
    assert expected == publish_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_invalid_cluster_force(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_cluster_cfg)

    app_path = os.path.join(tmpdir, app_name)
    dst_cfg_path = os.path.join(app_path, "config.yaml")
    publish_cmd = [tt_cmd, "cluster", "publish", "--force", app_name, "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(dst_cfg_path, 'r') as f:
        uploaded = f.read()

    assert invalid_cluster_cfg == uploaded


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_no_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish", f"{app_name}:non_exist", "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert (f"⨯ can't collect instance information for {app_name}:non_exist:" +
            " instance(s) not found") in publish_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_instance_without_app_config(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, "config.yaml")
    os.remove(cfg_path)
    publish_cmd = [tt_cmd, "cluster", "publish", f"{app_name}:master", "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert ("⨯ can not to update an instance configuration if " +
            "a cluster configuration file does not exist for" +
            " the application") in publish_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_valid_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, "config.yaml")
    publish_cmd = [tt_cmd, "cluster", "publish", f"{app_name}:master", "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(cfg_path, 'r') as f:
        uploaded = f.read()

    assert """groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: rw
            iproto:
              listen:
                - uri: 127.0.0.1:3303
          storage:
            database:
              mode: rw
            iproto:
              listen:
                - uri: 127.0.0.1:3302\n""" == uploaded


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_invalid_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_instance_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish", f"{app_name}:master",
                   "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    expected = ('   ⨯ an invalid instance "master" configuration:' +
                ' invalid path "database.mode":' +
                ' value "any" should be one of [ro rw]\n')
    assert expected == publish_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_invalid_instance_force(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_instance_cfg)

    app_path = os.path.join(tmpdir, app_name)
    dst_cfg_path = os.path.join(app_path, "config.yaml")
    publish_cmd = [tt_cmd, "cluster", "publish", "--force", f"{app_name}:master",
                   "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(dst_cfg_path, 'r') as f:
        uploaded = f.read()

    assert """groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: any
            iproto:
              listen:
                - uri: 127.0.0.1:3303
          storage:
            database:
              mode: rw
            iproto:
              listen:
                - uri: 127.0.0.1:3302\n""" == uploaded


def test_cluster_publish_config_etcd_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish",
                   "https://localhost:12344/prefix?timeout=0.1", "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    expected = (r"   ⨯ failed to establish a connection to tarantool or etcd:")
    assert expected in publish_output


def test_cluster_publish_config_etcd_key_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish",
                   "https://localhost:12344/prefix?key=foo&timeout=0.1", "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    expected = (r"   ⨯ failed to establish a connection to tarantool or etcd:")
    assert expected in publish_output


def test_cluster_publish_config_etcd_no_auth(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    etcd.enable_auth()

    try:
        publish_cmd = [tt_cmd, "cluster", "publish",
                       f"{etcd.endpoint}/prefix?timeout=0.1", "src.yaml"]
        instance_process = subprocess.Popen(
            publish_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        publish_output = instance_process.stdout.read()
    finally:
        etcd.disable_auth()

    expected = (r"   ⨯ failed to fetch data from etcd: etcdserver: user name is empty")
    assert expected in publish_output


def test_cluster_publish_config_etcd_bad_auth(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish",
                   f"http://invalid_user:invalid_pass@{etcd.endpoint}/prefix?timeout=0.1",
                   "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    expected = (r"   ⨯ failed to establish a connection to tarantool or etcd:")
    assert expected in publish_output


@pytest.mark.parametrize('auth', [False, "url", "flag", "env"])
def test_cluster_publish_cluster_etcd(tt_cmd, tmpdir_with_cfg, auth, etcd):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    try:
        if auth:
            etcd.enable_auth()

        if not auth:
            env = None
            url = f"{etcd.endpoint}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml"]
        elif auth == "url":
            env = None
            url = f"http://{etcd_username}:{etcd_password}@{etcd.host}:{etcd.port}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml"]
        elif auth == "flag":
            env = None
            url = f"{etcd.endpoint}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml",
                           "-u", etcd_username, "-p", etcd_password]
        elif auth == "env":
            env = {"TT_CLI_ETCD_USERNAME": etcd_username,
                   "TT_CLI_ETCD_PASSWORD": etcd_password}
            url = f"{etcd.endpoint}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml"]

        instance_process = subprocess.Popen(
            publish_cmd,
            env=env,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        publish_output = instance_process.stdout.read()

        if auth:
            etcd.disable_auth()
        conn = etcd.conn()
        etcd_content, _ = conn.get('/prefix/config/all')
        assert "" == publish_output
        assert valid_cluster_cfg == etcd_content.decode("utf-8")
    finally:
        etcd.disable_auth()


def test_cluster_publish_instance_etcd(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    cluster_cfg_path = os.path.join(tmpdir, "cluster.yaml")
    with open(cluster_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    instance_cfg_path = os.path.join(tmpdir, "instance.yaml")
    with open(instance_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)

    conn = etcd.conn()
    publish_cmd = [tt_cmd, "cluster", "publish",
                   f"{etcd.endpoint}/prefix?timeout=5", "cluster.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    publish_cmd = [tt_cmd, "cluster", "publish",
                   f"{etcd.endpoint}/prefix?timeout=5&name=master", "instance.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()
    etcd_content, _ = conn.get('/prefix/config/all')

    assert "" == publish_output
    assert valid_cluster_cfg.replace("3301", "3303") == etcd_content.decode("utf-8")


def test_cluster_publish_key_etcd(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    cluster_cfg_path = os.path.join(tmpdir, "cluster.yaml")
    with open(cluster_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    instance_cfg_path = os.path.join(tmpdir, "instance.yaml")
    with open(instance_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)

    conn = etcd.conn()
    publish_cmd = [tt_cmd, "cluster", "publish",
                   f"{etcd.endpoint}/prefix?key=anykey&timeout=5", "cluster.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    publish_cmd = [tt_cmd, "cluster", "publish",
                   f"{etcd.endpoint}/prefix?key=anykey&timeout=5&name=master", "instance.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()
    etcd_content, _ = conn.get('/prefix/config/anykey')

    assert "" == publish_output
    assert valid_cluster_cfg.replace("3301", "3303") == etcd_content.decode("utf-8")


def test_cluster_publish_instance_etcd_not_exist(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    cluster_cfg_path = os.path.join(tmpdir, "cluster.yaml")
    with open(cluster_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    instance_cfg_path = os.path.join(tmpdir, "instance.yaml")
    with open(instance_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish",
                   f"{etcd.endpoint}/prefix?timeout=5", "cluster.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    publish_cmd = [tt_cmd, "cluster", "publish",
                   f"{etcd.endpoint}/prefix?timeout=5&name=not_exist", "instance.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    assert ('   ⨯ failed to replace an instance "not_exist" configuration ' +
            'in a cluster configuration: cluster configuration has not an' +
            ' instance "not_exist"\n') == publish_output