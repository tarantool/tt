import os
import shutil
import subprocess

import etcd3
import pytest

test_simple_app_cfg = r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: rw
            iproto:
              listen: 127.0.0.1:3301
          storage:
            database:
              mode: rw
            iproto:
              listen: 127.0.0.1:3302
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
              listen: 127.0.0.1:3301
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
              listen: 127.0.0.1:3301
"""

valid_instance_cfg = r"""database:
  mode: rw
iproto:
  listen: 127.0.0.1:3303
"""

invalid_instance_cfg = r"""database:
  mode: any
iproto:
  listen: 127.0.0.1:3303
"""

# The root user requires the least amount of steps to work.
etcd_username = "root"
etcd_password = "password"


def copy_app(tmpdir, app_name):
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)


def etcd_start(host, tmpdir):
    popen = subprocess.Popen(
        ["etcd"],
        env={"ETCD_LISTEN_CLIENT_URLS": host,
             "ETCD_ADVERTISE_CLIENT_URLS": host,
             "PATH": os.getenv("PATH")},
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    try:
        popen.wait(1)
    except Exception:
        pass

    if popen.poll():
        return None

    return popen


# etcdv3 client have a bug that prevents to establish a connection with
# authentication enabled in latest python versions. So we need a separate steps
# to upload/fetch data to/from etcd via the client.
def etcd_enable_auth(popen, host):
    try:
        subprocess.run(["etcdctl", "user", "add", etcd_username,
                        f"--new-user-password={etcd_password}",
                        f"--endpoints={host}"])
        subprocess.run(["etcdctl", "auth", "enable",
                        f"--user={etcd_username}:{etcd_password}",
                        f"--endpoints={host}"])
    except Exception as ex:
        etcd_stop(popen)
        raise ex


def etcd_disable_auth(host):
    subprocess.run(["etcdctl", "auth", "disable",
                    f"--user={etcd_username}:{etcd_password}",
                    f"--endpoints={host}"])


def etcd_stop(popen):
    if popen:
        popen.terminate()
        popen.wait()


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


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_show_config_app_without_config(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, "config.yaml")
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
              listen: 127.0.0.1:3301
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

    expected = (f"   ⨯ can't collect instance information for {app_name}:unknown: " +
                "instance(s) not found")
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
  listen: 127.0.0.1:3302
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

    expected = (r"   ⨯ failed to collect a configuration from etcd: " +
                "failed to fetch data from etcd: context deadline exceeded")
    assert expected in show_output


def test_cluster_show_config_etcd_no_prefix(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    host = "http://localhost:12388"
    popen = etcd_start(host, tmpdir)
    assert popen

    try:
        show_cmd = [tt_cmd, "cluster", "show",
                    f"{host}/prefix?timeout=5"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        show_output = instance_process.stdout.read()
    finally:
        etcd_stop(popen)

    expected = (r"   ⨯ failed to collect a configuration from etcd: " +
                "a configuration data not found in prefix \"/prefix/config/\"")
    assert expected in show_output


def test_cluster_show_config_etcd_no_auth(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    host = "http://localhost:12388"
    popen = etcd_start(host, tmpdir)
    assert popen
    etcd_enable_auth(popen, host)

    try:
        show_cmd = [tt_cmd, "cluster", "show",
                    f"{host}/prefix?timeout=5"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        show_output = instance_process.stdout.read()
    finally:
        etcd_stop(popen)

    expected = (r"   ⨯ failed to collect a configuration from etcd: " +
                "failed to fetch data from etcd: etcdserver: user name is empty")
    assert expected in show_output


def test_cluster_show_config_etcd_bad_auth(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    host = "localhost:12388"
    popen = etcd_start(f"http://{host}", tmpdir)
    assert popen
    etcd_enable_auth(popen, f"http://{host}")

    try:
        show_cmd = [tt_cmd, "cluster", "show",
                    f"http://invalid_user:invalid_pass@{host}/prefix?timeout=5"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        show_output = instance_process.stdout.read()
    finally:
        etcd_stop(popen)

    expected = (r"   ⨯ failed to connect to etcd: " +
                "etcdserver: authentication failed, invalid user ID or password")
    assert expected in show_output


@pytest.mark.parametrize('auth', [False, "url", "flag", "env"])
def test_cluster_show_config_etcd_cluster(tt_cmd, tmpdir_with_cfg, auth):
    tmpdir = tmpdir_with_cfg
    host = "localhost"
    port = 12388
    endpoint = f"http://{host}:{port}"
    config = r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: rw
            iproto:
              listen: 127.0.0.1:3301
"""
    popen = etcd_start(endpoint, tmpdir)
    assert popen

    try:
        etcd = etcd3.client(host=host, port=port)
        etcd.put('/prefix/config/', config)

        if auth:
            etcd_enable_auth(popen, endpoint)

        if not auth:
            env = None
            url = f"{endpoint}/prefix?timeout=5"
            show_cmd = [tt_cmd, "cluster", "show", url]
        elif auth == "url":
            env = None
            url = f"http://{etcd_username}:{etcd_password}@{host}:{port}/prefix?timeout=5"
            show_cmd = [tt_cmd, "cluster", "show", url]
        elif auth == "flag":
            env = None
            url = f"{endpoint}/prefix?timeout=5"
            show_cmd = [tt_cmd, "cluster", "show", url,
                        "-u", etcd_username, "-p", etcd_password]
        elif auth == "env":
            env = {"TT_CLI_ETCD_USERNAME": etcd_username,
                   "TT_CLI_ETCD_PASSWORD": etcd_password}
            url = f"{endpoint}/prefix?timeout=5"
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
        etcd_stop(popen)


def test_cluster_show_config_etcd_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    host = "localhost"
    port = 12388
    endpoint = f"http://{host}:{port}"
    popen = etcd_start(endpoint, tmpdir)
    assert popen

    try:
        etcd = etcd3.client(host=host, port=port)
        etcd.put('/prefix/config/', r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: rw
            iproto:
              listen: 127.0.0.1:3301
""")
        show_cmd = [tt_cmd, "cluster", "show",
                    f"{endpoint}/prefix?timeout=5&name=master"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        show_output = instance_process.stdout.read()
    finally:
        etcd_stop(popen)

    assert r"""database:
  mode: rw
iproto:
  listen: 127.0.0.1:3301
""" in show_output


def test_cluster_show_config_etcd_no_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    host = "localhost"
    port = 12388
    endpoint = f"http://{host}:{port}"
    popen = etcd_start(endpoint, tmpdir)
    assert popen

    try:
        etcd = etcd3.client(host=host, port=port)
        etcd.put('/prefix/config/', r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
""")
        show_cmd = [tt_cmd, "cluster", "show",
                    f"{endpoint}/prefix?timeout=5&name=master"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        show_output = instance_process.stdout.read()
    finally:
        etcd_stop(popen)

    assert r'   ⨯ instance "master" not found' in show_output


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_no_configuration(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [tt_cmd, "cluster", "publish", app_name, "not_exist.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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

    show_cmd = [tt_cmd, "cluster", "publish", "non_exist", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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

    show_cmd = [tt_cmd, "cluster", "publish", app_name, "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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
    show_cmd = [tt_cmd, "cluster", "publish", app_name, "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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

    show_cmd = [tt_cmd, "cluster", "publish", app_name, "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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
    show_cmd = [tt_cmd, "cluster", "publish", "--force", app_name, "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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

    show_cmd = [tt_cmd, "cluster", "publish", f"{app_name}:non_exist", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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
    show_cmd = [tt_cmd, "cluster", "publish", f"{app_name}:master", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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
    show_cmd = [tt_cmd, "cluster", "publish", f"{app_name}:master", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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
              listen: 127.0.0.1:3303
          storage:
            database:
              mode: rw
            iproto:
              listen: 127.0.0.1:3302\n""" == uploaded


@pytest.mark.parametrize("app_name", ["test_simple_app", "testsimpleapp"])
def test_cluster_publish_invalid_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_instance_cfg)

    show_cmd = [tt_cmd, "cluster", "publish", f"{app_name}:master",
                "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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
    show_cmd = [tt_cmd, "cluster", "publish", "--force", f"{app_name}:master",
                "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
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
              listen: 127.0.0.1:3303
          storage:
            database:
              mode: rw
            iproto:
              listen: 127.0.0.1:3302\n""" == uploaded


def test_cluster_publish_config_etcd_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    show_cmd = [tt_cmd, "cluster", "publish",
                "https://localhost:12344/prefix?timeout=0.1", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    publish_output = instance_process.stdout.read()

    expected = (r"   ⨯ failed to fetch data from etcd: context deadline exceeded")
    assert expected in publish_output


def test_cluster_publish_config_etcd_no_auth(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    host = "http://localhost:12388"
    popen = etcd_start(host, tmpdir)
    assert popen
    etcd_enable_auth(popen, host)

    try:
        show_cmd = [tt_cmd, "cluster", "publish",
                    f"{host}/prefix?timeout=0.1", "src.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        publish_output = instance_process.stdout.read()

    finally:
        etcd_stop(popen)

    expected = (r"   ⨯ failed to fetch data from etcd: etcdserver: user name is empty")
    assert expected in publish_output


def test_cluster_publish_config_etcd_bad_auth(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    host = "localhost:12388"
    popen = etcd_start(f"http://{host}", tmpdir)
    assert popen
    etcd_enable_auth(popen, f"http://{host}")

    try:
        show_cmd = [tt_cmd, "cluster", "publish",
                    f"http://invalid_user:invalid_pass@{host}/prefix?timeout=0.1",
                    "src.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        publish_output = instance_process.stdout.read()

    finally:
        etcd_stop(popen)

    expected = (r"   ⨯ failed to connect to etcd: " +
                "etcdserver: authentication failed, invalid user ID or password")
    assert expected in publish_output


@pytest.mark.parametrize('auth', [False, "url", "flag", "env"])
def test_cluster_publish_cluster_etcd(tt_cmd, tmpdir_with_cfg, auth):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    host = "localhost"
    port = 12388
    endpoint = f"http://{host}:{port}"
    popen = etcd_start(endpoint, tmpdir)
    assert popen

    try:
        if auth:
            etcd_enable_auth(popen, endpoint)

        if not auth:
            env = None
            url = f"{endpoint}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml"]
        elif auth == "url":
            env = None
            url = f"http://{etcd_username}:{etcd_password}@{host}:{port}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml"]
        elif auth == "flag":
            env = None
            url = f"{endpoint}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml",
                           "-u", etcd_username, "-p", etcd_password]
        elif auth == "env":
            env = {"TT_CLI_ETCD_USERNAME": etcd_username,
                   "TT_CLI_ETCD_PASSWORD": etcd_password}
            url = f"{endpoint}/prefix?timeout=5"
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
            etcd_disable_auth(endpoint)
        etcd = etcd3.client(host=host, port=port)
        etcd_content, _ = etcd.get('/prefix/config/all')
        assert "" == publish_output
        assert valid_cluster_cfg == etcd_content.decode("utf-8")
    finally:
        etcd_stop(popen)


def test_cluster_publish_instance_etcd(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    cluster_cfg_path = os.path.join(tmpdir, "cluster.yaml")
    with open(cluster_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    instance_cfg_path = os.path.join(tmpdir, "instance.yaml")
    with open(instance_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)
    host = "localhost"
    port = 12388
    endpoint = f"http://{host}:{port}"
    popen = etcd_start(endpoint, tmpdir)
    assert popen

    try:
        etcd = etcd3.client(host=host, port=port)
        show_cmd = [tt_cmd, "cluster", "publish",
                    f"{endpoint}/prefix?timeout=5", "cluster.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        publish_output = instance_process.stdout.read()
        assert "" == publish_output

        show_cmd = [tt_cmd, "cluster", "publish",
                    f"{endpoint}/prefix?timeout=5&name=master", "instance.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        publish_output = instance_process.stdout.read()
        etcd_content, _ = etcd.get('/prefix/config/all')
    finally:
        etcd_stop(popen)

    assert "" == publish_output
    assert valid_cluster_cfg.replace("3301", "3303") == etcd_content.decode("utf-8")


def test_cluster_publish_instance_etcd_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    cluster_cfg_path = os.path.join(tmpdir, "cluster.yaml")
    with open(cluster_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)
    instance_cfg_path = os.path.join(tmpdir, "instance.yaml")
    with open(instance_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)
    host = "localhost"
    port = 12388
    endpoint = f"http://{host}:{port}"
    popen = etcd_start(endpoint, tmpdir)
    assert popen

    try:
        show_cmd = [tt_cmd, "cluster", "publish",
                    f"{endpoint}/prefix?timeout=5", "cluster.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        publish_output = instance_process.stdout.read()
        assert "" == publish_output

        show_cmd = [tt_cmd, "cluster", "publish",
                    f"{endpoint}/prefix?timeout=5&name=not_exist", "instance.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        publish_output = instance_process.stdout.read()
    finally:
        etcd_stop(popen)

    assert ('   ⨯ failed to replace an instance "not_exist" configuration ' +
            'in a cluster configuration: cluster configuration has not an' +
            ' instance "not_exist"\n') == publish_output
