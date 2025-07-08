import os
import shutil
import subprocess

import pytest

from utils import get_fixture_tcs_params, is_tarantool_ee, is_tarantool_less_3

fixture_tcs_params = get_fixture_tcs_params(
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "test_tcs_app"),
)


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
        text=True,
    )
    show_output = instance_process.stdout.read()

    expected = r"   ⨯ can't collect instance information for unknown:"
    assert expected in show_output


# spell-checker:ignore testsimpleapp


@pytest.mark.parametrize(
    "app_name, config_file",
    [
        pytest.param("test_simple_app", "config.yaml"),
        pytest.param("testsimpleapp", "config.yml"),
    ],
)
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
        text=True,
    )
    show_output = instance_process.stdout.read()

    expected = r"   ⨯ cluster configuration file does not exist for the application"
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
        text=True,
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
        text=True,
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
        text=True,
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
        text=True,
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
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert (
        r"""database:
  mode: rw
iproto:
  listen:
    - uri: 127.0.0.1:3302
"""
        in show_output
    )


def test_cluster_show_config_not_exist(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg

    show_cmd = [tt_cmd, "cluster", "show", "https://localhost:12344/prefix?timeout=0.1"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    expected = r"   ⨯ failed to establish a connection to tarantool or etcd:"
    assert expected in show_output


@pytest.mark.parametrize(
    "instance_name, storage_name",
    [pytest.param("etcd", "etcd"), pytest.param("tcs", "tarantool")],
)
def test_cluster_show_config_no_prefix(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
    storage_name,
    fixture_params,
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "show",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    expected = (
        r"   ⨯ failed to collect a configuration: "
        + f'a configuration data not found in {storage_name} for prefix "/prefix/config/"'
    )
    assert expected in show_output


@pytest.mark.parametrize(
    "instance_name, storage_name",
    [pytest.param("etcd", "etcd"), pytest.param("tcs", "tarantool")],
)
def test_cluster_show_config_no_key(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
    storage_name,
    fixture_params,
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "show",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?key=foo&timeout=5",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    expected = (
        r"   ⨯ failed to collect a configuration: "
        + f'a configuration data not found in {storage_name} for key "/prefix/config/foo"'
    )
    assert expected in show_output


@pytest.mark.parametrize(
    "instance_name, err_msg",
    [
        pytest.param(
            "etcd",
            "   ⨯ failed to collect a configuration: "
            + "failed to fetch data from etcd: etcdserver: user name is empty",
        ),
        pytest.param(
            "tcs",
            "   ⨯ failed to collect a configuration: "
            + "failed to fetch data from tarantool: Execute access to function "
            + "'config.storage.get' is denied for user",
        ),
    ],
)
def test_cluster_show_config_no_auth(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
    err_msg,
    fixture_params,
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    if instance_name == "etcd":
        instance.enable_auth()
    try:
        show_cmd = [tt_cmd, "cluster", "show", f"{instance.endpoint}/prefix?timeout=5"]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        show_output = instance_process.stdout.read()
    finally:
        if instance_name == "etcd":
            instance.disable_auth()

    assert err_msg in show_output


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_show_config_bad_auth(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
    fixture_params,
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    if instance_name == "etcd":
        instance.enable_auth()

    try:
        show_cmd = [
            tt_cmd,
            "cluster",
            "show",
            f"http://invalid_user:invalid_pass@{instance.endpoint}/prefix?timeout=5",
        ]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        show_output = instance_process.stdout.read()
    finally:
        if instance_name == "etcd":
            instance.disable_auth()

    expected = r"   ⨯ failed to establish a connection to tarantool or etcd: "
    assert expected in show_output


@pytest.mark.parametrize(
    "instance_name, auth",
    [
        pytest.param("etcd", False),
        pytest.param("etcd", "url"),
        pytest.param("etcd", "flag"),
        pytest.param("etcd", "env"),
        pytest.param("tcs", "url"),
        pytest.param("tcs", "flag"),
        pytest.param("tcs", "env"),
    ],
)
def test_cluster_show_config_cluster(
    tt_cmd,
    tmpdir_with_cfg,
    auth,
    instance_name,
    request,
    fixture_params,
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
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
        conn = instance.conn()
        if instance_name == "etcd":
            conn.put("/prefix/config/all", config)
        else:
            conn.call("config.storage.put", "/prefix/config/all", config)

        if auth and instance_name == "etcd":
            instance.enable_auth()

        if not auth:
            env = None
            url = f"{instance.endpoint}/prefix?timeout=5"
            show_cmd = [tt_cmd, "cluster", "show", url]
        elif auth == "url":
            env = None
            url = (
                f"http://{instance.connection_username}:{instance.connection_password}@"
                f"{instance.host}:{instance.port}/prefix?timeout=5"
            )
            show_cmd = [tt_cmd, "cluster", "show", url]
        elif auth == "flag":
            env = None
            url = f"{instance.endpoint}/prefix?timeout=5"
            show_cmd = [
                tt_cmd,
                "cluster",
                "show",
                url,
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
            show_cmd = [tt_cmd, "cluster", "show", url]
        instance_process = subprocess.Popen(
            show_cmd,
            env=env,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        show_output = instance_process.stdout.read()
        assert config in show_output
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_show_config_instance(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
    fixture_params,
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
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
    if instance_name == "etcd":
        conn.put("/prefix/config/", config)
    else:
        conn.call("config.storage.put", "/prefix/config/all", config)
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "show",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5&name=master",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert (
        r"""database:
  mode: rw
iproto:
  listen:
    - uri: 127.0.0.1:3301
"""
        in show_output
    )


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_show_config_key(tt_cmd, tmpdir_with_cfg, instance_name, request, fixture_params):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    if instance_name == "etcd":
        conn.put("/prefix/config/anykey", valid_cluster_cfg)
    else:
        conn.call("config.storage.put", "/prefix/config/anykey", valid_cluster_cfg)
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "show",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?key=anykey&timeout=5",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert valid_cluster_cfg in show_output


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_show_config_key_instance(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
    fixture_params,
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    if instance_name == "etcd":
        conn.put("/prefix/config/anykey", valid_cluster_cfg)
    else:
        conn.call("config.storage.put", "/prefix/config/anykey", valid_cluster_cfg)
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "show",
        "http://"
        + creds
        + f"{instance.host}:{instance.port}/prefix?key=anykey&timeout=5&name=master",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert (
        r"""database:
  mode: rw
iproto:
  listen:
    - uri: 127.0.0.1:3301
"""
        in show_output
    )


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_show_config_no_instance(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
    fixture_params,
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    config = r"""groups:
group-001:
    replicasets:
    replicaset-001:
        instances:
"""
    if instance_name == "etcd":
        conn.put("/prefix/config/all", config)
    else:
        conn.call("config.storage.put", "/prefix/config/all", config)
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "show",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix?timeout=5&name=master",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert r'   ⨯ instance "master" not found' in show_output
