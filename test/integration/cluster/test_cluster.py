import os
import shutil
import subprocess

import etcd3

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


def etcd_start(host, tmpdir):
    print("1")
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

    if not popen.poll():
        return popen
    return None


def etcd_stop(popen):
    if popen:
        popen.kill()
        popen.wait()


def test_cluster_show_config_not_exist_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    show_cmd = [tt_cmd, "cluster", "show", "unknown"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    expected = (r"   ⨯ unknown: can't find an application init file:")
    assert expected in show_output


def test_cluster_show_config_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    show_cmd = [tt_cmd, "cluster", "show", f"{app_name}"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    assert test_simple_app_cfg == show_output


def test_cluster_show_config_app_validate_no_error(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    show_cmd = [tt_cmd, "cluster", "show", "--validate", f"{app_name}"]
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
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    show_cmd = [tt_cmd, "cluster", "show", "--validate", f"{app_name}"]
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
    expected += r""" failed to validate ["database" "mode" "rs"]: should be one of [ro rw]
failed to validate ["database" "txn_timeout" "asd"]: failed to parse number
"""

    assert expected == show_output


def test_cluster_show_config_app_not_exist_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    show_cmd = [tt_cmd, "cluster", "show", f"{app_name}:unknown"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    show_output = instance_process.stdout.read()

    expected = (r"   ⨯ test_simple_app:unknown: " +
                "can't find an application init file: " +
                "instance(s) not found")
    assert expected in show_output


def test_cluster_show_config_app_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

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


def test_cluster_show_config_etcd_cluster(tt_cmd, tmpdir_with_cfg):
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
                    f"{endpoint}/prefix?timeout=5"]
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

    assert r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            database:
              mode: rw
            iproto:
              listen: 127.0.0.1:3301
""" in show_output


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


def test_cluster_public_no_configuration(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    show_cmd = [tt_cmd, "cluster", "public", "test_simple_app", "not_exist.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    expected = (r'   ⨯ failed to read path "not_exist.yaml": ' +
                'open not_exist.yaml: no such file or directory')
    assert expected in public_output


def test_cluster_public_no_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    show_cmd = [tt_cmd, "cluster", "public", "non_exist", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    assert "⨯ non_exist: can't find an application init file:" in public_output


def test_cluster_public_valid_cluster(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    dst_cfg_path = os.path.join(app_path, "config.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    show_cmd = [tt_cmd, "cluster", "public", "test_simple_app", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    assert "" == public_output

    with open(dst_cfg_path, 'r') as f:
        uploaded = f.read()

    assert valid_cluster_cfg == uploaded


def test_cluster_public_invalid_cluster(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_cluster_cfg)

    show_cmd = [tt_cmd, "cluster", "public", "test_simple_app", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    expected = ('   ⨯ an invalid instance "master" configuration:' +
                ' failed to validate ["database" "mode" "any"]:' +
                ' should be one of [ro rw]\n')
    assert expected == public_output


def test_cluster_public_invalid_cluster_force(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    dst_cfg_path = os.path.join(app_path, "config.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_cluster_cfg)

    show_cmd = [tt_cmd, "cluster", "public", "--force", "test_simple_app", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    assert "" == public_output

    with open(dst_cfg_path, 'r') as f:
        uploaded = f.read()

    assert invalid_cluster_cfg == uploaded


def test_cluster_public_no_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)

    show_cmd = [tt_cmd, "cluster", "public", "test_simple_app:non_exist", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    assert ("⨯ test_simple_app:non_exist: can't find an application init file:" +
            " instance(s) not found") in public_output


def test_cluster_public_valid_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    dst_cfg_path = os.path.join(app_path, "config.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_instance_cfg)

    show_cmd = [tt_cmd, "cluster", "public", "test_simple_app:master", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    assert "" == public_output

    with open(dst_cfg_path, 'r') as f:
        uploaded = f.read()

    print(uploaded)
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


def test_cluster_public_invalid_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_instance_cfg)

    show_cmd = [tt_cmd, "cluster", "public", "test_simple_app:master",
                "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    expected = ('   ⨯ an invalid instance "master" configuration:' +
                ' failed to validate ["database" "mode" "any"]:' +
                ' should be one of [ro rw]\n')
    assert expected == public_output


def test_cluster_public_invalid_instance_force(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_simple_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    dst_cfg_path = os.path.join(app_path, "config.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(invalid_instance_cfg)

    show_cmd = [tt_cmd, "cluster", "public", "--force", "test_simple_app:master",
                "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    assert "" == public_output

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


def test_cluster_public_config_etcd_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, 'w') as f:
        f.write(valid_cluster_cfg)

    show_cmd = [tt_cmd, "cluster", "public",
                "https://localhost:12344/prefix?timeout=0.1", "src.yaml"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    public_output = instance_process.stdout.read()

    expected = (r"   ⨯ failed to fetch data from etcd: context deadline exceeded")
    assert expected in public_output


def test_cluster_public_cluster_etcd(tt_cmd, tmpdir_with_cfg):
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
        etcd = etcd3.client(host=host, port=port)
        show_cmd = [tt_cmd, "cluster", "public",
                    f"{endpoint}/prefix?timeout=5", "src.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        public_output = instance_process.stdout.read()
        etcd_content, _ = etcd.get('/prefix/config/all')
    finally:
        etcd_stop(popen)

    assert "" == public_output
    assert valid_cluster_cfg == etcd_content.decode("utf-8")


def test_cluster_public_instance_etcd(tt_cmd, tmpdir_with_cfg):
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
        show_cmd = [tt_cmd, "cluster", "public",
                    f"{endpoint}/prefix?timeout=5", "cluster.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        public_output = instance_process.stdout.read()
        assert "" == public_output

        show_cmd = [tt_cmd, "cluster", "public",
                    f"{endpoint}/prefix?timeout=5&name=master", "instance.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        public_output = instance_process.stdout.read()
        etcd_content, _ = etcd.get('/prefix/config/all')
    finally:
        etcd_stop(popen)

    assert "" == public_output
    assert valid_cluster_cfg.replace("3301", "3303") == etcd_content.decode("utf-8")


def test_cluster_public_instance_etcd_not_exist(tt_cmd, tmpdir_with_cfg):
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
        show_cmd = [tt_cmd, "cluster", "public",
                    f"{endpoint}/prefix?timeout=5", "cluster.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        public_output = instance_process.stdout.read()
        assert "" == public_output

        show_cmd = [tt_cmd, "cluster", "public",
                    f"{endpoint}/prefix?timeout=5&name=not_exist", "instance.yaml"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        public_output = instance_process.stdout.read()
    finally:
        etcd_stop(popen)

    assert ('   ⨯ failed to replace an instance "not_exist" configuration ' +
            'in a cluster configuration: cluster configuration has not an' +
            ' instance "not_exist"\n') == public_output
