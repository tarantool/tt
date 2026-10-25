import os
import shutil
import subprocess

import pytest

from utils import cluster_config_file


def copy_app(tmpdir, app_name):
    """Copy the project app_name into tmpdir."""
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)


def app_cmd(tt_cmd, _tmpdir, _app_name):
    """Return the start of a command acting on the project app_name: it
    runs in the project directory, where the application is found."""
    return [tt_cmd]


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


def test_cluster_publish_in_place(tt_cmd, tmp_path):
    # Publishing to the application of the project replaces the cluster
    # config of its pair.
    env_path = tmp_path / "app"
    pair_dir = env_path / os.path.dirname(cluster_config_file)
    pair_dir.mkdir(0o755, parents=True)

    with open(pair_dir / "instances.yaml", "w") as f:
        f.write("master:\n")
    with open(env_path / cluster_config_file, "w") as f:
        f.write(valid_cluster_cfg)

    cfg = valid_cluster_cfg.replace("3301", "3311")
    with open(env_path / "cfg.yml", "w") as f:
        f.write(cfg)

    publish_cmd = [tt_cmd, "cluster", "publish", "app", "cfg.yml"]
    publish_process = subprocess.Popen(
        publish_cmd,
        cwd=env_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = publish_process.stdout.read()
    assert "" == publish_output

    with open(env_path / cluster_config_file) as f:
        assert f.read() == cfg


@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_publish_no_configuration(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        app_name,
        "not_exist.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    expected = (
        r'   ⨯ failed to read path "not_exist.yaml": '
        + "open not_exist.yaml: no such file or directory"
    )
    assert expected in publish_output


def test_cluster_publish_no_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)

    publish_cmd = [tt_cmd, "cluster", "publish", "non_exist", "src.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    assert 'unable to resolve the application "non_exist": application not found' in (
        publish_output
    )


@pytest.mark.parametrize(
    "app_name",
    ["test_simple_app"],
)
def test_cluster_publish_valid_cluster(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)

    app_path = os.path.join(tmpdir, app_name)
    dst_cfg_path = os.path.join(app_path, cluster_config_file)

    publish_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "publish", app_name, src_cfg_path]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(dst_cfg_path, "r") as f:
        uploaded = f.read()

    assert valid_cluster_cfg == uploaded


@pytest.mark.parametrize(
    "app_name",
    ["test_simple_app"],
)
def test_cluster_publish_valid_cluster_without_app_config(
    tt_cmd,
    tmpdir_with_cfg,
    app_name,
):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)

    app_path = os.path.join(tmpdir, app_name)
    dst_cfg_path = os.path.join(app_path, cluster_config_file)
    os.remove(dst_cfg_path)
    publish_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "publish", app_name, src_cfg_path]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    # An application whose pair lost its cluster config is no application
    # to publish to: the description alone is an incomplete pair.
    assert "incomplete pair" in publish_output
    assert not os.path.exists(dst_cfg_path)


@pytest.mark.skip(
    reason="jsonschema error messages have unsubstituted {property}/{received}/{expected} "
    "placeholders; awaiting go-config release with "
    "https://github.com/tarantool/go-config/pull/60",
)
@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_publish_invalid_cluster(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(invalid_cluster_cfg)

    publish_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "publish", app_name, src_cfg_path]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    expected = (
        '   ⨯ an invalid instance "master" configuration:'
        + ' invalid path "database.mode":'
        + ' value "any" should be one of [ro rw]\n'
    )
    assert expected == publish_output


@pytest.mark.parametrize(
    "app_name",
    ["test_simple_app"],
)
def test_cluster_publish_invalid_cluster_force(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(invalid_cluster_cfg)

    app_path = os.path.join(tmpdir, app_name)
    dst_cfg_path = os.path.join(app_path, cluster_config_file)
    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        "--force",
        app_name,
        src_cfg_path,
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(dst_cfg_path, "r") as f:
        uploaded = f.read()

    assert invalid_cluster_cfg == uploaded


@pytest.mark.parametrize(
    "app_name",
    ["test_simple_app"],
)
def test_cluster_publish_instance_without_app_config(
    tt_cmd,
    tmpdir_with_cfg,
    app_name,
):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, cluster_config_file)
    os.remove(cfg_path)
    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        f"{app_name}:master",
        src_cfg_path,
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    # The description alone is an incomplete pair.
    assert "incomplete pair" in publish_output


@pytest.mark.parametrize(
    "app_name",
    ["test_simple_app"],
)
def test_cluster_publish_valid_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, cluster_config_file)
    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        f"{app_name}:master",
        src_cfg_path,
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(cfg_path, "r") as f:
        uploaded = f.read()

    assert (
        """groups:
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
                - uri: 127.0.0.1:3302\n"""
        == uploaded
    )


@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_publish_wrong_replicaset_name(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        "--replicaset",
        "noexist",
        f"{app_name}:master",
        src_cfg_path,
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    expected = ' ⨯ wrong replicaset name, expected "replicaset-001", have "noexist"'
    assert expected in publish_output


@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_publish_wrong_group_name(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        "--group",
        "noexist",
        f"{app_name}:master",
        src_cfg_path,
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    expected = ' ⨯ wrong group name, expected "group-001", have "noexist"'
    assert expected in publish_output


@pytest.mark.parametrize(
    "app_name",
    ["test_simple_app"],
)
@pytest.mark.parametrize("specify_replicaset", [True, False])
def test_cluster_publish_new_instance_config(
    tt_cmd,
    tmpdir_with_cfg,
    specify_replicaset,
    app_name,
):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, cluster_config_file)
    publish_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "publish"]
    if specify_replicaset:
        publish_cmd.extend(["--replicaset", "replicaset-001"])
    publish_cmd.extend([f"{app_name}:router", src_cfg_path])
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    if not specify_replicaset:
        expected = '   ⨯ replicaset name is not specified for "router" instance configuration'
        assert expected in publish_output
        return

    assert "" == publish_output

    with open(cfg_path, "r") as f:
        uploaded = f.read()

    assert (
        """groups:
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
          router:
            database:
              mode: rw
            iproto:
              listen:
                - uri: 127.0.0.1:3303\n"""
        == uploaded
    )


@pytest.mark.parametrize("specify_group", [True, False])
def test_cluster_publish_valid_new_instance_config_new_replicaset(
    tt_cmd,
    tmpdir_with_cfg,
    specify_group,
):
    app_name = "test_simple_app"
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, cluster_config_file)
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        "--replicaset",
        "replicaset-002",
    ]
    if specify_group:
        publish_cmd.extend(["--group", "group-002"])
    publish_cmd.extend([f"{app_name}:router", src_cfg_path])
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    if not specify_group:
        expected = '   ⨯ failed to determine the group of the "replicaset-002" replicaset'
        assert expected in publish_output
        return

    assert "" == publish_output

    with open(cfg_path, "r") as f:
        uploaded = f.read()

    assert (
        """groups:
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
  group-002:
    replicasets:
      replicaset-002:
        instances:
          router:
            database:
              mode: rw
            iproto:
              listen:
                - uri: 127.0.0.1:3303\n"""
        == uploaded
    )


@pytest.mark.skip(
    reason="jsonschema error messages have unsubstituted {property}/{received}/{expected} "
    "placeholders; awaiting go-config release with "
    "https://github.com/tarantool/go-config/pull/60",
)
@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_publish_invalid_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(invalid_instance_cfg)

    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        f"{app_name}:master",
        src_cfg_path,
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    expected = (
        '   ⨯ an invalid instance "master" configuration:'
        + ' invalid path "database.mode":'
        + ' value "any" should be one of [ro rw]\n'
    )
    assert expected == publish_output


@pytest.mark.parametrize(
    "app_name",
    ["test_simple_app"],
)
def test_cluster_publish_invalid_instance_force(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(invalid_instance_cfg)

    app_path = os.path.join(tmpdir, app_name)
    dst_cfg_path = os.path.join(app_path, cluster_config_file)
    publish_cmd = [
        *app_cmd(tt_cmd, tmpdir, app_name),
        "cluster",
        "publish",
        "--force",
        f"{app_name}:master",
        src_cfg_path,
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    with open(dst_cfg_path, "r") as f:
        uploaded = f.read()

    assert (
        """groups:
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
                - uri: 127.0.0.1:3302\n"""
        == uploaded
    )


def test_cluster_publish_config_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)

    publish_cmd = [
        tt_cmd,
        "cluster",
        "publish",
        "https://localhost:12344/prefix?timeout=0.1",
        "src.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    expected = r"   ⨯ failed to establish a connection to tarantool or etcd:"
    assert expected in publish_output


def test_cluster_publish_config_key_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)

    publish_cmd = [
        tt_cmd,
        "cluster",
        "publish",
        "https://localhost:12344/prefix?key=foo&timeout=0.1",
        "src.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    expected = r"   ⨯ failed to establish a connection to tarantool or etcd:"
    assert expected in publish_output


@pytest.mark.parametrize(
    "instance_name, expected_err_msg",
    [
        pytest.param(
            "etcd",
            "etcdserver: user name is empty",
        ),
        pytest.param(
            "tcs",
            "denied for user 'guest' (AccessDeniedError, code 0x2a)",
        ),
    ],
)
def test_cluster_publish_config_no_auth(
    instance_name,
    tt_cmd,
    tmpdir_with_cfg,
    request,
    expected_err_msg,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        publish_cmd = [
            tt_cmd,
            "cluster",
            "publish",
            f"{instance.endpoint}/prefix?timeout=5",
            "src.yaml",
        ]
        instance_process = subprocess.Popen(
            publish_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        publish_output = instance_process.stdout.read()
    finally:
        if instance_name == "etcd":
            instance.disable_auth()

    assert expected_err_msg in publish_output


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_publish_config_bad_auth(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)

    publish_cmd = [
        tt_cmd,
        "cluster",
        "publish",
        f"http://invalid_user:invalid_pass@{instance.endpoint}/prefix?timeout=5",
        "src.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    expected = r"   ⨯ failed to establish a connection to tarantool or etcd:"
    assert expected in publish_output


@pytest.mark.parametrize(
    "auth, instance_name",
    [
        pytest.param(False, "etcd"),
        pytest.param("url", "etcd"),
        pytest.param("flag", "etcd"),
        pytest.param("env", "etcd"),
        pytest.param("url", "tcs"),
        pytest.param("flag", "tcs"),
        pytest.param("env", "tcs"),
    ],
)
def test_cluster_publish_cluster(
    tt_cmd,
    tmpdir_with_cfg,
    auth,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    src_cfg_path = os.path.join(tmpdir, "src.yaml")
    with open(src_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)

    try:
        if auth and instance_name == "etcd":
            instance.enable_auth()

        if not auth:
            env = None
            url = f"{instance.endpoint}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml"]
        elif auth == "url":
            env = None
            url = (
                f"http://{instance.connection_username}:{instance.connection_password}@"
                f"{instance.host}:{instance.port}/prefix?timeout=5"
            )
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml"]
        elif auth == "flag":
            env = None
            url = f"{instance.endpoint}/prefix?timeout=5"
            publish_cmd = [
                tt_cmd,
                "cluster",
                "publish",
                url,
                "src.yaml",
                "-u",
                instance.connection_username,
                "-p",
                instance.connection_password,
            ]
        elif auth == "env":
            env = {
                (
                    "TT_CLI_ETCD_USERNAME" if instance_name == "etcd" else "TT_CLI_USERNAME"
                ): instance.connection_username,
                (
                    "TT_CLI_ETCD_PASSWORD" if instance_name == "etcd" else "TT_CLI_PASSWORD"
                ): instance.connection_password,
            }
            url = f"{instance.endpoint}/prefix?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "publish", url, "src.yaml"]

        instance_process = subprocess.Popen(
            publish_cmd,
            env=env,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        publish_output = instance_process.stdout.read()

        if auth and instance_name == "etcd":
            instance.disable_auth()
        conn = instance.conn()
        content = ""
        if instance_name == "etcd":
            content, _ = conn.get("/prefix/config/all")
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", "/prefix/config/all")
            # We need first selected value with field 'value' which is 1st index.
            if len(content) > 0:
                content = content[0]["data"][0]["value"]
        assert "" == publish_output
        assert valid_cluster_cfg == content
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_publish_instance(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    cluster_cfg_path = os.path.join(tmpdir, "cluster.yaml")
    with open(cluster_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)
    instance_cfg_path = os.path.join(tmpdir, "instance.yaml")
    with open(instance_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    conn = instance.conn()
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    publish_cmd = [
        tt_cmd,
        "cluster",
        "publish",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5",
        "cluster.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    publish_cmd = [
        tt_cmd,
        "cluster",
        "publish",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5&name=master",
        "instance.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    content = ""
    if instance_name == "etcd":
        content, _ = conn.get("/prefix/config/all")
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", "/prefix/config/all")
        if len(content) > 0:
            content = content[0]["data"][0]["value"]

    assert "" == publish_output
    assert valid_cluster_cfg.replace("3301", "3303") == content


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_publish_key(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    cluster_cfg_path = os.path.join(tmpdir, "cluster.yaml")
    with open(cluster_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)
    instance_cfg_path = os.path.join(tmpdir, "instance.yaml")
    with open(instance_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    conn = instance.conn()
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    publish_cmd = [
        tt_cmd,
        "cluster",
        "publish",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?key=anykey&timeout=5",
        "cluster.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    publish_cmd = [
        tt_cmd,
        "cluster",
        "publish",
        "http://"
        + creds
        + f"{instance.host}:{instance.port}/prefix?key=anykey&timeout=5&name=master",
        "instance.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    content = ""
    if instance_name == "etcd":
        content, _ = conn.get("/prefix/config/anykey")
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", "/prefix/config/anykey")
        if len(content) > 0:
            content = content[0]["data"][0]["value"]

    assert "" == publish_output
    assert valid_cluster_cfg.replace("3301", "3303") == content


@pytest.mark.parametrize(
    "specify_replicaset, instance_name",
    [pytest.param(True, "etcd"), pytest.param(False, "etcd")],
)
def test_cluster_publish_instance_not_exist(
    tt_cmd,
    tmpdir_with_cfg,
    specify_replicaset,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    cluster_cfg_path = os.path.join(tmpdir, "cluster.yaml")
    with open(cluster_cfg_path, "w") as f:
        f.write(valid_cluster_cfg)
    instance_cfg_path = os.path.join(tmpdir, "instance.yaml")
    with open(instance_cfg_path, "w") as f:
        f.write(valid_instance_cfg)

    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    publish_cmd = [
        tt_cmd,
        "cluster",
        "publish",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5",
        "cluster.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    publish_cmd = [tt_cmd, "cluster", "publish"]
    if specify_replicaset:
        publish_cmd.extend(["--replicaset", "replicaset-001"])
    publish_cmd.extend(
        [
            "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5&name=router",
            "instance.yaml",
        ],
    )

    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    if not specify_replicaset:
        expected = '   ⨯ replicaset name is not specified for "router" instance configuration'
        assert expected in publish_output
        return

    assert "" == publish_output
    conn = instance.conn()
    content = ""
    if instance_name == "etcd":
        content, _ = conn.get("/prefix/config/all")
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", "/prefix/config/all")
        if len(content) > 0:
            content = content[0]["data"][0]["value"]
    assert (
        """groups:
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
          router:
            database:
              mode: rw
            iproto:
              listen:
                - uri: 127.0.0.1:3303\n"""
        == content
    )
