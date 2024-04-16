import os
import shutil
import subprocess

import pytest
from etcd_helper import etcd_password, etcd_username


def copy_app(tmpdir, app_name):
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)


test_simple_app_cfg = r"""groups:
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
          storage:
            database:
              mode: rw
            iproto:
              listen:
                - uri: 127.0.0.1:3302
"""

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


def test_cluster_show_config_not_exist_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg

    show_cmd = [tt_cmd, "cluster", "show", "unknown"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    expected = (r"   ⨯ can't collect instance information for unknown:")
    assert expected in show_output


@pytest.mark.parametrize("app_name, config_file", [
    pytest.param("test_simple_app", "config.yaml"),
    pytest.param("testsimpleapp", "config.yml"),
])
def test_cluster_show_config_app_without_config(tt_cmd, tmpdir_with_cfg, app_name, config_file):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, config_file)
    os.remove(cfg_path)

    show_cmd = [tt_cmd, "cluster", "show", app_name]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    expected = (r"   ⨯ cluster configuration file does not exist for the application")
    assert expected in show_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_show_config_app(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [tt_cmd, "cluster", "show", app_name]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    assert test_simple_app_cfg == show_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_show_config_app_validate_no_error(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [tt_cmd, "cluster", "show", "--validate", app_name]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    assert test_simple_app_cfg == show_output


def test_cluster_show_config_app_validate_error(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_error_app"
    copy_app(tmpdir, app_name)

    show_cmd = [tt_cmd, "cluster", "show", "--validate", app_name]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()
    expected = r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: rs
              txn_timeout: asd
            iproto:
              listen:
                - uri: 127.0.0.1:3301
   ⨯ an invalid instance "master" configuration:"""
    expected += r""" invalid path "database.mode": value "rs" should be one of [ro rw]
invalid path "database.txn_timeout": failed to parse value "asd" to type number
"""

    assert expected == show_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_show_config_app_not_exist_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [tt_cmd, "cluster", "show", f"{app_name}:unknown"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    expected = '   ⨯ instance "unknown" not found'
    assert expected in show_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_show_config_app_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [tt_cmd, "cluster", "show", f"{app_name}:storage"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    assert r"""database:
  mode: rw
iproto:
  listen:
    - uri: 127.0.0.1:3302
""" in show_output


def test_cluster_show_config_etcd_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg

    show_cmd = [tt_cmd, "cluster", "show",
                "https://localhost:12344/prefix?timeout=0.1"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    expected = (r"   ⨯ failed to establish a connection to tarantool or etcd:")
    assert expected in show_output


def test_cluster_show_config_etcd_no_prefix(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    show_cmd = [tt_cmd, "cluster", "show",
                f"{etcd.endpoint}/prefix?timeout=5"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    expected = (r"   ⨯ failed to collect a configuration: " +
                "a configuration data not found in etcd for prefix \"/prefix/config/\"")
    assert expected in show_output


def test_cluster_show_config_etcd_no_key(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    show_cmd = [tt_cmd, "cluster", "show",
                f"{etcd.endpoint}/prefix?key=foo&timeout=5"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    expected = (r"   ⨯ failed to collect a configuration: " +
                "a configuration data not found in etcd for key \"/prefix/config/foo\"")
    assert expected in show_output


def test_cluster_show_config_etcd_no_auth(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    etcd.enable_auth()
    try:
        show_cmd = [tt_cmd, "cluster", "show",
                    f"{etcd.endpoint}/prefix?timeout=5"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        show_output = instance_process.stdout.read()
    finally:
        etcd.disable_auth()

    expected = (r"   ⨯ failed to collect a configuration: " +
                "failed to fetch data from etcd: etcdserver: user name is empty")
    assert expected in show_output


def test_cluster_show_config_etcd_bad_auth(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg
    etcd.enable_auth()

    try:
        show_cmd = [tt_cmd, "cluster", "show",
                    f"http://invalid_user:invalid_pass@{etcd.endpoint}/prefix?timeout=5"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        show_output = instance_process.stdout.read()
    finally:
        etcd.disable_auth()

    expected = (r"   ⨯ failed to establish a connection to tarantool or etcd: ")
    assert expected in show_output


@pytest.mark.parametrize('auth', [False, "url", "flag", "env"])
def test_cluster_show_config_etcd_cluster(tt_cmd, tmpdir_with_cfg, auth, etcd):
    tmpdir = tmpdir_with_cfg
    config = r"""groups:
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
    try:
        conn = etcd.conn()
        conn.put('/prefix/config/', config)

        if auth:
            etcd.enable_auth()

        if not auth:
            env = None
            url = f"{etcd.endpoint}/prefix?timeout=5"
            show_cmd = [tt_cmd, "cluster", "show", url]
        elif auth == "url":
            env = None
            url = f"http://{etcd_username}:{etcd_password}@{etcd.host}:{etcd.port}/prefix?timeout=5"
            show_cmd = [tt_cmd, "cluster", "show", url]
        elif auth == "flag":
            env = None
            url = f"{etcd.endpoint}/prefix?timeout=5"
            show_cmd = [tt_cmd, "cluster", "show", url,
                        "-u", etcd_username, "-p", etcd_password]
        elif auth == "env":
            env = {"TT_CLI_ETCD_USERNAME": etcd_username,
                   "TT_CLI_ETCD_PASSWORD": etcd_password}
            url = f"{etcd.endpoint}/prefix?timeout=5"
            show_cmd = [tt_cmd, "cluster", "show", url]
        instance_process = subprocess.Popen(
            show_cmd,
            env=env,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        show_output = instance_process.stdout.read()
        assert config in show_output
    finally:
        etcd.disable_auth()


def test_cluster_show_config_etcd_instance(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    conn = etcd.conn()
    conn.put('/prefix/config/', r"""groups:
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
""")
    show_cmd = [tt_cmd, "cluster", "show",
                f"{etcd.endpoint}/prefix?timeout=5&name=master"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    assert r"""database:
  mode: rw
iproto:
  listen:
    - uri: 127.0.0.1:3301
""" in show_output


def test_cluster_show_config_etcd_key(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    conn = etcd.conn()
    conn.put('/prefix/config/anykey', valid_cluster_cfg)
    show_cmd = [tt_cmd, "cluster", "show",
                f"{etcd.endpoint}/prefix?key=anykey&timeout=5"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    assert valid_cluster_cfg in show_output


def test_cluster_show_config_etcd_key_instance(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    conn = etcd.conn()
    conn.put('/prefix/config/anykey', valid_cluster_cfg)
    show_cmd = [tt_cmd, "cluster", "show",
                f"{etcd.endpoint}/prefix?key=anykey&timeout=5&name=master"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    assert r"""database:
  mode: rw
iproto:
  listen:
    - uri: 127.0.0.1:3301
""" in show_output


def test_cluster_show_config_etcd_no_instance(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    conn = etcd.conn()
    conn.put('/prefix/config/', r"""groups:
group-001:
    replicasets:
    replicaset-001:
        instances:
""")
    show_cmd = [tt_cmd, "cluster", "show",
                f"{etcd.endpoint}/prefix?timeout=5&name=master"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    assert r'   ⨯ instance "master" not found' in show_output
