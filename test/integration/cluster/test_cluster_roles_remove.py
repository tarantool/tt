import pytest
from tarantool.connection import os

from utils import (get_fixture_tcs_params, is_tarantool_ee,
                   is_tarantool_less_3, run_command_and_get_output)

fixture_tcs_params = get_fixture_tcs_params(os.path.join(os.path.dirname(
                                            os.path.abspath(__file__)), "test_tcs_app"))


def to_etcd_key(key):
    return f"/prefix/config/{key}"


valid_cluster_cfg = r"""groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            iproto:
              listen: {}
"""


@pytest.mark.parametrize("role_args, err_msg", [
    (["http://localhost:3303"], "Error: accepts 2 arg(s), received 1"),
    (["http://localhost:3303", "role"], "need to provide flag(s) with scope roles will removed")
])
def test_cluster_rs_roles_remove_missing_args(tt_cmd, tmpdir_with_cfg, role_args, err_msg):
    tmpdir = tmpdir_with_cfg
    cmd = [tt_cmd, "cluster", "rs", "roles", "remove"]
    cmd.extend(role_args)
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc != 0
    assert err_msg in out


@pytest.mark.parametrize(
    "instance_name, expected_err_msg",
    [
        pytest.param(
            "etcd",
            r"   ⨯ failed to collect cluster config: failed to fetch" +
            " data from etcd: etcdserver: user name is empty",
        ),
        pytest.param(
            "tcs",
            r"⨯ failed to collect cluster config: failed to fetch data" +
            " from tarantool: Execute access to function 'config.storage.get'" +
            " is denied for user 'guest'"
        ),
    ],
)
def test_cluster_rs_roles_remove_no_auth(
    instance_name, tt_cmd, tmpdir_with_cfg, request, expected_err_msg, fixture_params
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        roles_remove_cmd = [tt_cmd, "cluster", "rs", "roles", "remove", "-f",
                            f"{instance.endpoint}/prefix?timeout=0.1", "role", "-G"]
        rc, output = run_command_and_get_output(roles_remove_cmd, cwd=tmpdir_with_cfg)
        assert rc == 1
    finally:
        if instance_name == "etcd":
            instance.disable_auth()

    assert expected_err_msg in output


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_rs_roles_remove_bad_auth(
    tt_cmd, tmpdir_with_cfg, instance_name, request, fixture_params
):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)

    roles_remove_cmd = [tt_cmd, "cluster", "rs", "roles", "remove",
                        f"http://invalid_user:invalid:pass@{instance.endpoint}/prefix?timeout=0.1",
                        "role", "-G"]
    rc, output = run_command_and_get_output(roles_remove_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1

    expected = (r"   ⨯ failed to establish a connection to tarantool or etcd:")
    assert expected in output


@pytest.mark.parametrize("auth, instance_name", [
    (None, "etcd"),
    ("url", "etcd"),
    ("flag", "etcd"),
    ("env", "etcd"),
    ("url", "tcs"),
    ("flag", "tcs"),
    ("env", "tcs"),
])
def test_cluster_rs_roles_add_auth(tt_cmd,
                                   tmpdir_with_cfg,
                                   auth,
                                   instance_name,
                                   request,
                                   fixture_params):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)

    conn = instance.conn()
    key = to_etcd_key("all")
    cfg_to_publish = valid_cluster_cfg + """\
roles:
  - config.storage
"""
    print(cfg_to_publish)
    if instance_name == "etcd":
        conn.put(key, cfg_to_publish)
    else:
        conn.call("config.storage.put", key, cfg_to_publish)
    try:
        if instance_name == "etcd" and auth:
            instance.enable_auth()

        if not auth:
            env = None
            url = f"{instance.endpoint}/prefix?timeout=5"
            roles_remove_cmd = [tt_cmd, "cluster", "rs", "roles", "remove", "-f",
                                url, "config.storage", "-G"]
        if auth == "url":
            env = None
            url = (
                f"http://{instance.connection_username}:{instance.connection_password}@"
                f"{instance.host}:{instance.port}/prefix?timeout=5"
            )
            roles_remove_cmd = [tt_cmd, "cluster", "rs", "roles", "remove", "-f",
                                url, "config.storage", "-G"]
        elif auth == "flag":
            env = None
            url = f"{instance.endpoint}/prefix?timeout=5"
            roles_remove_cmd = [tt_cmd, "cluster", "rs", "roles", "remove", "-f",
                                "-u", instance.connection_username,
                                "-p", instance.connection_password, url, "config.storage", "-G"]
        elif auth == "env":
            env = {
                (
                    "TT_CLI_ETCD_USERNAME"
                    if instance_name == "etcd"
                    else "TT_CLI_USERNAME"
                ): instance.connection_username,
                (
                    "TT_CLI_ETCD_PASSWORD"
                    if instance_name == "etcd"
                    else "TT_CLI_PASSWORD"
                ): instance.connection_password,
            }
            url = f"{instance.endpoint}/prefix?timeout=5"
            roles_remove_cmd = [tt_cmd, "cluster", "rs", "roles", "remove", "-f",
                                url, "config.storage", "-G"]
        rc, out = run_command_and_get_output(roles_remove_cmd, cwd=tmpdir_with_cfg, env=env)
        assert rc == 0
        assert f'Patching the config by the key: "{key}"' in out

        if instance_name == "etcd":
            instance.disable_auth()

        conn = instance.conn()
        content = ""
        if instance_name == "etcd":
            content, _ = conn.get(key)
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", key)
            if len(content) > 0:
                content = content[0]["data"][0]["value"]
        assert content == """groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            iproto:
              listen: {}
roles: []
"""
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name, flags, cfg, expected_cfg, err_msg", [
    ("etcd", ["-G"], valid_cluster_cfg + """\
roles:
  - config.storage
""", valid_cluster_cfg + """\
roles: []
""", None),
    ("etcd", ["-g", "group-001"], valid_cluster_cfg + """\
    roles:
      - config.storage
""", valid_cluster_cfg + """\
    roles: []
""", None),
    ("etcd", ["-r", "replicaset-001"], valid_cluster_cfg + """\
        roles:
          - config.storage
""", valid_cluster_cfg + """\
        roles: []
""", None),
    ("etcd", ["-i", "instance-001"], valid_cluster_cfg + """\
            roles:
              - config.storage
""", valid_cluster_cfg + """\
            roles: []
""", None),
    ("etcd", ["-G", "-g", "group-001", "-r", "replicaset-001", "-i", "instance-001"],
     valid_cluster_cfg + """\
            roles:
              - config.storage
        roles:
          - config.storage
    roles:
      - config.storage
roles:
  - config.storage
""", valid_cluster_cfg + """\
            roles: []
        roles: []
    roles: []
roles: []
""", None),
    ("etcd", ["-G"], valid_cluster_cfg + """\
roles:
  - config.storage
  - other_role""", valid_cluster_cfg + """\
roles:
  - other_role
""", None),
    ("etcd", ["-g", "group-001"], valid_cluster_cfg + """\
    roles:
      - role
""", "", "cannot update roles by path [groups group-001 roles]: role \"config.storage\" not found"),
    ("etcd", ["-g", "invalid_group"], valid_cluster_cfg, "",
     "cannot find group \"invalid_group\""),
    ("etcd", ["-r", "invalid_replicaset"], valid_cluster_cfg, "",
     "cannot find replicaset \"invalid_replicaset\" above group"),
    ("etcd", ["-i", "invalid_instance"], valid_cluster_cfg, "",
     "cannot find instance \"invalid_instance\" above group and/or replicaset"),
    ("tcs", ["-G"], valid_cluster_cfg + """\
roles:
  - config.storage
""", valid_cluster_cfg + """\
roles: []
""", None),
    ("tcs", ["-g", "group-001"], valid_cluster_cfg + """\
    roles:
      - config.storage
""", valid_cluster_cfg + """\
    roles: []
""", None),
    ("tcs", ["-r", "replicaset-001"], valid_cluster_cfg + """\
        roles:
          - config.storage
""", valid_cluster_cfg + """\
        roles: []
""", None),
    ("tcs", ["-i", "instance-001"], valid_cluster_cfg + """\
            roles:
              - config.storage
""", valid_cluster_cfg + """\
            roles: []
""", None),
    ("tcs", ["-G", "-g", "group-001", "-r", "replicaset-001", "-i", "instance-001"],
     valid_cluster_cfg + """\
            roles:
              - config.storage
        roles:
          - config.storage
    roles:
      - config.storage
roles:
  - config.storage
""", valid_cluster_cfg + """\
            roles: []
        roles: []
    roles: []
roles: []
""", None),
    ("tcs", ["-G"], valid_cluster_cfg + """\
roles:
  - config.storage
  - other_role""", valid_cluster_cfg + """\
roles:
  - other_role
""", None),
    ("tcs", ["-g", "group-001"], valid_cluster_cfg + """\
    roles:
      - role
""", "", "cannot update roles by path [groups group-001 roles]: role \"config.storage\" not found"),
    ("tcs", ["-g", "invalid_group"], valid_cluster_cfg, "",
     "cannot find group \"invalid_group\""),
    ("tcs", ["-r", "invalid_replicaset"], valid_cluster_cfg, "",
     "cannot find replicaset \"invalid_replicaset\" above group"),
    ("tcs", ["-i", "invalid_instance"], valid_cluster_cfg, "",
     "cannot find instance \"invalid_instance\" above group and/or replicaset"),
])
def test_cluster_rs_roles_add(tt_cmd,
                              tmpdir_with_cfg,
                              instance_name,
                              flags,
                              cfg,
                              expected_cfg,
                              err_msg,
                              request,
                              fixture_params):
    if instance_name == "tcs":
        if is_tarantool_less_3() or not is_tarantool_ee():
            pytest.skip()
        for k, v in fixture_tcs_params.items():
            fixture_params[k] = v
    instance = request.getfixturevalue(instance_name)

    conn = instance.conn()
    key = to_etcd_key("all")
    if instance_name == "etcd":
        conn.put(key, cfg)
    else:
        conn.call("config.storage.put", key, cfg)

    url = (
        f"http://{instance.connection_username}:{instance.connection_password}@"
        f"{instance.host}:{instance.port}/prefix?timeout=5"
    )
    roles_remove_cmd = [tt_cmd, "cluster", "rs", "roles", "remove", url, "config.storage"]
    roles_remove_cmd.extend(flags)
    rc, output = run_command_and_get_output(roles_remove_cmd, cwd=tmpdir_with_cfg)

    if not err_msg:
        assert rc == 0
        assert f"Patching the config by the key: \"{key}\"" in output

        conn = instance.conn()
        content = ""
        if instance_name == "etcd":
            content, _ = conn.get(key)
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", key)
            if len(content) > 0:
                content = content[0]["data"][0]["value"]
        assert content == expected_cfg
    else:
        assert rc == 1
        assert err_msg in output
