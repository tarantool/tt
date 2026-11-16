import os
import shutil
import subprocess

import pytest
import yaml

from utils import cluster_config_file, run_command_and_get_output


def copy_app(tmpdir, app_name):
    """Copy the project app_name into tmpdir."""
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)


def app_cmd(tt_cmd, _tmpdir, _app_name):
    """Return the start of a command acting on the project app_name: it
    runs in the project directory, where the application is found."""
    return [tt_cmd]


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

    expected = r'   ⨯ unable to resolve the application "unknown": application not found'
    assert expected in show_output


@pytest.mark.parametrize(
    "app_name",
    ["test_simple_app"],
)
def test_cluster_show_config_app_without_config(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    app_path = os.path.join(tmpdir, app_name)
    cfg_path = os.path.join(app_path, cluster_config_file)
    os.remove(cfg_path)

    show_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "show", app_name]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    # The description without its cluster config is an incomplete pair.
    expected = r"incomplete pair"
    assert expected in show_output


@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_show_config_app(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "show", app_name]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert test_simple_app_cfg == show_output


def test_cluster_show_config_app_template(tt_cmd, tmp_path):
    # --template selects the pair whose config tt cluster show reads, as it
    # selects the pair tt instance start runs; the default template without
    # it. The template of another topology is a copy of the default one with
    # one instance.
    app_name = "test_simple_app"
    copy_app(tmp_path, app_name)
    app_path = tmp_path / app_name
    other = app_path / ".templates" / "other"
    other.mkdir()
    (other / "instances.yaml").write_text("master:\n")
    (other / "config.yaml").write_text(valid_cluster_cfg)

    for args, expected in [
        (["--template", "other"], valid_cluster_cfg),
        ([], test_simple_app_cfg),
        (["--template", "default"], test_simple_app_cfg),
    ]:
        rc, out = run_command_and_get_output(
            [tt_cmd, "cluster", "show", *args, app_name],
            cwd=app_path,
        )
        assert rc == 0, out
        assert out == expected

    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "show", "--template", "nosuch", app_name],
        cwd=app_path,
    )
    assert rc == 1
    assert 'has no template "nosuch"' in out
    assert "its templates are default, other" in out


@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_show_config_app_validate_no_error(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "show", "--validate", app_name]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert test_simple_app_cfg == show_output


@pytest.mark.skip(
    reason="jsonschema error messages have unsubstituted {property}/{received}/{expected} "
    "placeholders; awaiting go-config release with "
    "https://github.com/tarantool/go-config/pull/60",
)
def test_cluster_show_config_app_validate_error(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_error_app"
    copy_app(tmpdir, app_name)

    show_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "show", "--validate", app_name]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=os.path.join(tmpdir, app_name),
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


@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_show_config_app_not_exist_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "show", f"{app_name}:unknown"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=os.path.join(tmpdir, app_name),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    expected = '   ⨯ instance "unknown" not found'
    assert expected in show_output


@pytest.mark.parametrize("app_name", ["test_simple_app"])
def test_cluster_show_config_app_instance(tt_cmd, tmpdir_with_cfg, app_name):
    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    show_cmd = [*app_cmd(tt_cmd, tmpdir, app_name), "cluster", "show", f"{app_name}:storage"]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=os.path.join(tmpdir, app_name),
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
):
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

    expected = f"a configuration data not found in {storage_name} for prefix"

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
):
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

    expected = f"failed to fetch data from {storage_name}: integrity: not found"
    assert expected in show_output


@pytest.mark.parametrize(
    "instance_name, err_msg",
    [
        pytest.param(
            "etcd",
            "etcdserver: user name is empty",
        ),
        pytest.param(
            "tcs",
            "for user 'guest' (AccessDeniedError, code 0x2a)",
        ),
    ],
)
def test_cluster_show_config_no_auth(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
    err_msg,
):
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
):
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
):
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
):
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
def test_cluster_show_config_key(tt_cmd, tmpdir_with_cfg, instance_name, request):
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
):
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
):
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


def test_cluster_show_file(tt_cmd, tmp_path):
    # A cluster config file is shown as the config of an application: merged
    # with the environment, an instance of it on its own, and validated.
    (tmp_path / "config.yaml").write_text(test_simple_app_cfg)
    (tmp_path / "invalid.yml").write_text(invalid_cluster_cfg)
    sub = tmp_path / "sub"
    sub.mkdir()

    for target, cwd in [
        ("config.yaml", tmp_path),
        ("./config.yaml", tmp_path),
        (str(tmp_path / "config.yaml"), sub),
        ("../config.yaml", sub),
    ]:
        rc, out = run_command_and_get_output(
            [tt_cmd, "cluster", "show", "--validate", target],
            cwd=cwd,
        )
        assert rc == 0, out
        assert out == test_simple_app_cfg

    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "show", "config.yaml:storage"],
        cwd=tmp_path,
        env={**os.environ, "TT_REPLICATION_FAILOVER": "manual"},
    )
    assert rc == 0, out
    assert out == (
        "database:\n  mode: rw\n"
        "iproto:\n  listen:\n    - uri: 127.0.0.1:3302\n"
        "replication:\n  failover: manual\n"
    )

    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "show", "./config.yaml:nosuch"],
        cwd=tmp_path,
    )
    assert rc == 1
    assert 'instance "nosuch" not found' in out

    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "show", "--validate", "invalid.yml"],
        cwd=tmp_path,
    )
    assert rc == 1
    assert "mode" in out and "invalid" in out

    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "show", "./missing.yaml"],
        cwd=tmp_path,
    )
    assert rc == 1
    assert "missing.yaml" in out
    assert "no such file or directory" in out


def make_etcd_app(tmp_path, etcd, app_name="etcd_app"):
    """Make the project app_name in tmp_path whose config.yaml points to
    etcd, with the cluster config valid_cluster_cfg published there, and
    return the path of the project."""
    app_path = tmp_path / app_name
    template = app_path / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text("master:\n")
    (template / "config.yaml").write_text(
        f"config:\n  etcd:\n    endpoints:\n      - {etcd.endpoint}\n    prefix: /prefix\n"
        "labels:\n  zone: file\n",
    )
    etcd.conn().put("/prefix/config/all", valid_cluster_cfg)
    return app_path


@pytest.mark.parametrize(
    "target",
    [
        pytest.param("etcd_app", id="app"),
        pytest.param("./.templates/default/config.yaml", id="file"),
    ],
)
def test_cluster_show_etcd_arrays(tt_cmd, tmp_path, etcd, target):
    # The arrays of a config in etcd come out as arrays, not as maps keyed
    # by "0", "1", in the cluster config and in that of an instance, and
    # --validate takes them for arrays.
    app_path = make_etcd_app(tmp_path, etcd)
    listen = "listen:\n                - uri: 127.0.0.1:3301\n"

    for args in [[], ["--validate"]]:
        rc, out = run_command_and_get_output(
            [tt_cmd, "cluster", "show", *args, target],
            cwd=app_path,
        )
        assert rc == 0, out
        assert listen in out
        assert '"0"' not in out
        assert "zone: file" in out

        rc, out = run_command_and_get_output(
            [tt_cmd, "cluster", "show", *args, target + ":master"],
            cwd=app_path,
        )
        assert rc == 0, out
        assert "iproto:\n  listen:\n    - uri: 127.0.0.1:3301\n" in out
        assert "database:\n  mode: rw\n" in out
        assert '"0"' not in out


def test_cluster_show_file_env_empty_map(tt_cmd, tmp_path):
    # An empty map from the environment leaves the map of the file as it is,
    # as Tarantool merges it: TT_LABELS='{}' drops no label.
    (tmp_path / "config.yaml").write_text("labels:\n  a: x\n" + test_simple_app_cfg)

    for name in ["TT_LABELS", "TT_LABELS_DEFAULT"]:
        env = {**os.environ, name: "{}"}
        rc, out = run_command_and_get_output(
            [tt_cmd, "cluster", "show", "./config.yaml"],
            cwd=tmp_path,
            env=env,
        )
        assert rc == 0, out
        assert out.startswith("labels:\n  a: x\n"), out

        rc, out = run_command_and_get_output(
            [tt_cmd, "cluster", "show", "./config.yaml:master"],
            cwd=tmp_path,
            env=env,
        )
        assert rc == 0, out
        assert "labels:\n  a: x\n" in out, out


def make_precedence_app(tmp_path, etcd, file_cfg, storage_cfg):
    """Make the project prec_app in tmp_path whose config.yaml is file_cfg
    pointing to etcd, with storage_cfg published there, and return the path
    of the project."""
    app_path = tmp_path / "prec_app"
    template = app_path / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text("master:\n")
    (template / "config.yaml").write_text(
        f"config:\n  etcd:\n    endpoints:\n      - {etcd.endpoint}\n    prefix: /prefix\n"
        + file_cfg,
    )
    etcd.conn().put("/prefix/config/all", storage_cfg)
    return app_path


def show_instance(tt_cmd, app_path, env):
    """Return the config tt cluster show prints for prec_app:master."""
    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "show", "prec_app:master"],
        cwd=app_path,
        env={**os.environ, **env},
    )
    assert rc == 0, out
    return yaml.safe_load(out)


def test_cluster_show_sources_over_scopes(tt_cmd, tmp_path, etcd):
    # The config of an instance is that of Tarantool 3.9, run with the same
    # file and environment and a cluster config source put where etcd goes:
    # it makes the config of the instance of each source, its scopes merged,
    # and merges these by the priority TT_* > file > storage > TT_*_DEFAULT.
    # A global key of the file wins over an instance key of the storage, an
    # instance key of the file over a global one of the storage and over a
    # default, and TT_* over an instance key of the storage.
    app_path = make_precedence_app(
        tmp_path,
        etcd,
        """labels:
  b: file-global
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            labels:
              a: file-instance
            snapshot:
              count: 9
""",
        """labels:
  a: storage-global
  k: storage-global
replication:
  failover: manual
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            labels:
              b: storage-instance
              d: storage-instance
            memtx:
              memory: 300000000
""",
    )

    got = show_instance(
        tt_cmd,
        app_path,
        {
            "TT_MEMTX_MEMORY": "268435456",
            "TT_SNAPSHOT_COUNT_DEFAULT": "3",
            "TT_REPLICATION_FAILOVER_DEFAULT": "off",
        },
    )
    assert got["labels"] == {
        "a": "file-instance",
        "b": "file-global",
        "k": "storage-global",
        "d": "storage-instance",
    }
    assert got["memtx"] == {"memory": 268435456}
    assert got["snapshot"] == {"count": 9}
    assert got["replication"] == {"failover": "manual"}


def test_cluster_show_null_is_unset(tt_cmd, tmp_path, etcd):
    # Null is no value, as Tarantool 3.9 reads it: the scopes and the
    # sources under it fill it in.
    app_path = make_precedence_app(
        tmp_path,
        etcd,
        """memtx:
  memory: null
snapshot:
  count: 5
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          master:
            replication:
              failover: null
            snapshot:
              count: null
""",
        "replication:\n  failover: manual\n",
    )

    got = show_instance(tt_cmd, app_path, {"TT_MEMTX_MEMORY_DEFAULT": "268435456"})
    assert got["replication"] == {"failover": "manual"}
    assert got["memtx"] == {"memory": 268435456}
    assert got["snapshot"] == {"count": 5}
