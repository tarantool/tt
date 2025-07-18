import io
import os
import shutil

import pytest
from cartridge_helper import cartridge_name, cartridge_password, cartridge_username
from integration.replicaset.replicaset_helpers import (
    get_group_by_replicaset_name,
    get_group_replicaset_by_instance_name,
    parse_status,
    parse_yml,
    start_application,
    stop_application,
)

from utils import get_tarantool_version, read_kv, run_command_and_get_output

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


TEST_ROLES_REMOVE_PARAMS_CCONFIG = (
    "role_name, inst, inst_flg, group, rs, is_uri, is_global,"
    " err_msg, stop_instance, is_force, patch_cfg_content"
)
TEST_ROLES_REMOVE_PARAMS_CARTRIDGE = (
    "role_name, inst_flg, group, rs, is_uri, is_global, is_custom, is_empty, err_msg"
)


def make_test_roles_remove_param(
    is_cartridge_orchestrator,
    role_name,
    inst=None,
    inst_flg=None,
    group=None,
    rs=None,
    is_global=False,
    err_msg="",
    stop_instance=None,
    is_uri=False,
    is_force=False,
    is_custom=False,
    is_empty=True,
    patch_cfg_content=None,
):
    if is_cartridge_orchestrator:
        return pytest.param(
            role_name,
            inst_flg,
            group,
            rs,
            is_uri,
            is_global,
            is_custom,
            is_empty,
            err_msg,
        )
    return pytest.param(
        role_name,
        inst,
        inst_flg,
        group,
        rs,
        is_uri,
        is_global,
        err_msg,
        stop_instance,
        is_force,
        patch_cfg_content,
    )


@pytest.mark.parametrize(
    "args, err_msg",
    [
        pytest.param(["some_role"], "Error: accepts 2 arg(s), received 1"),
        pytest.param(["some_app", "some_role"], "can't collect instance information for some_app"),
    ],
)
def test_roles_add_missing_args(tt_cmd, tmpdir_with_cfg, args, err_msg):
    cmd = [tt_cmd, "rs", "roles", "remove"]
    cmd.extend(args)
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir_with_cfg)
    assert rc != 0
    assert err_msg in out


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize(
    TEST_ROLES_REMOVE_PARAMS_CCONFIG,
    [
        make_test_roles_remove_param(
            False,
            inst="instance-005",
            role_name="greeter",
            patch_cfg_content="""\
            roles:
              - greeter
""",
        ),
        make_test_roles_remove_param(
            False,
            is_global=True,
            role_name="greeter",
            patch_cfg_content="""\
roles:
  - greeter
""",
        ),
        make_test_roles_remove_param(
            False,
            group="group-002",
            role_name="greeter",
            patch_cfg_content="""\
    roles:
      - greeter
""",
        ),
        make_test_roles_remove_param(
            False,
            rs="replicaset-002",
            role_name="greeter",
            patch_cfg_content="""\
        roles:
          - greeter
""",
        ),
        make_test_roles_remove_param(
            False,
            inst_flg="instance-005",
            role_name="greeter",
            patch_cfg_content="""\
            roles:
              - greeter
""",
        ),
        make_test_roles_remove_param(
            False,
            is_global=True,
            group="group-002",
            rs="replicaset-002",
            inst_flg="instance-005",
            role_name="greeter",
            patch_cfg_content="""\
            roles:
              - greeter
        roles:
          - greeter
    roles:
      - greeter
roles:
  - greeter
""",
        ),
        make_test_roles_remove_param(
            False,
            is_global=True,
            inst="instance-001",
            inst_flg="instance-002",
            role_name="greeter",
            err_msg="there are different instance names passed after app name and in flag arg",
        ),
        make_test_roles_remove_param(
            False,
            role_name="greeter",
            err_msg="there is no destination provided where to remove role",
        ),
        make_test_roles_remove_param(
            False,
            inst="instance-005",
            role_name="greeter",
            stop_instance="instance-001",
            is_force=True,
            patch_cfg_content="""\
            roles:
              - greeter
""",
        ),
        make_test_roles_remove_param(
            False,
            inst="instance-002",
            role_name="greeter",
            rs="replicaset-001",
            stop_instance="instance-001",
            err_msg="all instances in the target replicaset should be online,"
            + " could not connect to: instance-001",
        ),
        make_test_roles_remove_param(
            False,
            inst="instance-001",
            role_name="greeter",
            err_msg='role "greeter" not found',
        ),
        make_test_roles_remove_param(
            False,
            role_name="greeter",
            is_uri=True,
            err_msg="roles remove is not supported for a single instance by"
            + ' "centralized config" orchestrator',
        ),
    ],
)
def test_replicaset_cconfig_roles_remove(
    role_name,
    inst,
    inst_flg,
    group,
    rs,
    is_global,
    err_msg,
    stop_instance,
    tt_cmd,
    tmpdir_with_cfg,
    is_uri,
    is_force,
    patch_cfg_content,
):
    app_name = "test_ccluster_app"
    app_path = os.path.join(tmpdir_with_cfg, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    if patch_cfg_content:
        with open(os.path.join(app_path, "config.yaml"), "a") as f:
            f.write(patch_cfg_content)

    kv = read_kv(app_path)
    instances = parse_yml(kv["instances"]).keys()

    try:
        start_application(tt_cmd, tmpdir_with_cfg, app_name, instances)

        if stop_instance:
            stop_cmd = [tt_cmd, "stop", "-y", f"{app_name}:{stop_instance}"]
            rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)
            assert rc == 0

        flags = []
        if is_force:
            flags.extend(["-f"])
        if is_global:
            flags.extend(["-G"])
        if group:
            flags.extend(["-g", group])
        if rs:
            flags.extend(["-r", rs])
        if inst_flg:
            flags.extend(["-i", inst_flg])

        uri = None
        if is_uri:
            uri = f"client:secret@{tmpdir_with_cfg}/{app_name}/{next(iter(instances))}.iproto"

        roles_add_cmd = [
            tt_cmd,
            "rs",
            "roles",
            "remove",
            (f"{app_name}:{inst}" if inst else app_name if not is_uri else uri),
            role_name,
        ]
        if len(flags) != 0:
            roles_add_cmd.extend(flags)
        rc, out = run_command_and_get_output(roles_add_cmd, cwd=tmpdir_with_cfg)
        if err_msg == "":
            assert rc == 0
            kv = read_kv(app_path)
            cluster_cfg = parse_yml(kv["config"])
            if is_global:
                assert "Remove role from global scope"
                assert role_name not in cluster_cfg["roles"]
            if group:
                assert f"Remove role from group: {group}"
                assert role_name not in cluster_cfg["groups"][group]["roles"]
            if rs:
                assert f"Remove role from replicaset: {rs}"
                gr = get_group_by_replicaset_name(cluster_cfg, rs)
                assert role_name not in cluster_cfg["groups"][gr]["replicasets"][rs]["roles"]
            if inst_flg or inst:
                i = inst if inst else inst_flg
                assert f"Remove role from instance: {i}"
                g, r = get_group_replicaset_by_instance_name(cluster_cfg, i)
                assert (
                    role_name
                    not in cluster_cfg["groups"][g]["replicasets"][r]["instances"][i]["roles"]
                )
        else:
            assert rc == 1
            assert err_msg in out
    finally:
        stop_application(
            tt_cmd,
            app_name,
            tmpdir_with_cfg,
            instances,
            force=True if stop_instance else False,
        )


@pytest.mark.skipif(tarantool_major_version >= 3, reason="skip cartridge tests for Tarantool 3.0")
@pytest.mark.parametrize(
    TEST_ROLES_REMOVE_PARAMS_CARTRIDGE,
    [
        make_test_roles_remove_param(
            True,
            rs="router",
            role_name="app.roles.custom",
            is_empty=False,
        ),
        make_test_roles_remove_param(
            True,
            rs="s-3",
            role_name="app.roles.custom",
        ),
        make_test_roles_remove_param(
            True,
            rs="s-3",
            role_name="app.roles.custom",
            group="g",
            err_msg="cannot provide vshard-group by removing role",
        ),
        make_test_roles_remove_param(
            True,
            rs="s-1",
            role_name="failover-coordinator",
            inst_flg="s1-master",
            err_msg=(
                "cannot pass the instance or --instance (-i) flag due to cluster"
                " with cartridge orchestrator can't add/remove role for instance scope"
            ),
        ),
        make_test_roles_remove_param(
            True,
            rs="s-1",
            group="default",
            role_name="failover-coordinator",
            is_global=True,
            err_msg="cannot pass --global (-G) flag due to cluster with cartridge",
        ),
        make_test_roles_remove_param(
            True,
            inst_flg="s-1.master",
            role_name="failover-coordinator",
            err_msg="in cartridge replicaset name must be specified via --replicaset flag",
        ),
        make_test_roles_remove_param(
            True,
            role_name="my_role",
            rs="s-1",
            err_msg='failed to change role: role "my_role" not found',
        ),
        make_test_roles_remove_param(
            True,
            rs="unknown",
            role_name="some_role",
            err_msg='failed to find replicaset "unknown"',
        ),
        make_test_roles_remove_param(
            True,
            rs="r",
            role_name="role",
            is_custom=True,
            err_msg='roles remove is not supported for an application by "custom" orchestrator',
        ),
        make_test_roles_remove_param(
            True,
            rs="r",
            role_name="role",
            is_custom=True,
            err_msg='roles remove is not supported for an application by "custom" orchestrator',
        ),
        make_test_roles_remove_param(
            True,
            rs="r",
            role_name="role",
            is_uri=True,
            err_msg=(
                'roles remove is not supported for a single instance by "cartridge" orchestrator'
            ),
        ),
    ],
)
def test_replicaset_cartridge_roles_remove(
    tt_cmd,
    cartridge_app,
    role_name,
    rs,
    inst_flg,
    group,
    is_uri,
    is_global,
    is_custom,
    is_empty,
    err_msg,
):
    flags = []
    if is_global:
        flags.extend(["-G"])
    if group:
        flags.extend(["-g", group])
    if rs:
        flags.extend(["-r", rs])
    if inst_flg:
        flags.extend(["-i", inst_flg])

    uri = None
    if is_uri:
        uri = cartridge_app.instances_cfg[f"{cartridge_name}.s1-master"]["advertise_uri"]
        uri = f"{cartridge_username}:{cartridge_password}@{uri}"

    roles_add_cmd = [tt_cmd, "rs", "roles", "remove"]
    if is_custom:
        roles_add_cmd.append("--custom")
    roles_add_cmd.extend([cartridge_name if not is_uri else uri, role_name])
    roles_add_cmd.extend(flags)
    rc, out = run_command_and_get_output(roles_add_cmd, cwd=cartridge_app.workdir)

    if err_msg == "":
        assert rc == 0

        buf = io.StringIO(out)
        assert "• Discovery application..." in buf.readline()
        buf.readline()
        # Skip init status in the output.
        parse_status(buf)
        assert f"Remove role {role_name} from replicaset: {rs}" in buf.readline()
        if is_empty:
            assert f"Now replicaset {rs} has no roles enabled" in buf.readline()
        else:
            assert f"Replicaset {rs} now has these roles enabled:" in buf.readline()
            assert "failover-coordinator" in buf.readline()
            assert "vshard-router" in buf.readline()
        assert "Done." in buf.readline()

        status_cmd = [tt_cmd, "rs", "status", cartridge_name]
        rc, out = run_command_and_get_output(status_cmd, cwd=cartridge_app.workdir)
        assert rc == 0

        # Check status if there are roles left.
        if not is_empty:
            actual_roles = parse_status(io.StringIO(out))["replicasets"][rs]["roles"]
            assert role_name not in actual_roles
    else:
        assert rc == 1
        assert err_msg in out
