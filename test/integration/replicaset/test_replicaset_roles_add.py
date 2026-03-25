import os
import shutil

import pytest
from integration.replicaset.replicaset_helpers import (
    get_group_by_replicaset_name,
    get_group_replicaset_by_instance_name,
    parse_yml,
    start_application,
    stop_application,
)

from utils import get_tarantool_version, read_kv, run_command_and_get_output

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


TEST_ROLES_ADD_PARAMS_CCONFIG = (
    "role_name, inst, inst_flg, group, rs, is_uri, is_global, err_msg,"
    " stop_instance, is_force, is_add_role"
)


def make_test_roles_add_param(
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
    is_add_role=False,
):
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
        is_add_role,
    )


@pytest.mark.parametrize(
    "args, err_msg",
    [
        pytest.param(["some_role"], "Error: accepts 2 arg(s), received 1"),
        pytest.param(["some_app", "some_role"], "can't collect instance information for some_app"),
    ],
)
def test_roles_add_missing_args(tt_cmd, tmpdir_with_cfg, args, err_msg):
    cmd = [tt_cmd, "rs", "roles", "add"]
    cmd.extend(args)
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir_with_cfg)
    assert rc != 0
    assert err_msg in out


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize(
    TEST_ROLES_ADD_PARAMS_CCONFIG,
    [
        make_test_roles_add_param(
            inst="instance-001",
            role_name="greeter",
        ),
        make_test_roles_add_param(
            is_global=True,
            role_name="greeter",
        ),
        make_test_roles_add_param(
            group="group-001",
            role_name="greeter",
        ),
        make_test_roles_add_param(
            rs="replicaset-001",
            role_name="greeter",
        ),
        make_test_roles_add_param(
            inst_flg="instance-002",
            role_name="greeter",
        ),
        make_test_roles_add_param(
            is_global=True,
            group="group-001",
            rs="replicaset-001",
            inst_flg="instance-002",
            role_name="greeter",
        ),
        make_test_roles_add_param(
            is_global=True,
            inst="instance-001",
            group="group-001",
            rs="replicaset-001",
            inst_flg="instance-002",
            role_name="greeter",
            err_msg="there are different instance names passed after app name and in flag arg",
        ),
        make_test_roles_add_param(
            role_name="greeter",
            err_msg="there is no destination provided in which to add role",
        ),
        make_test_roles_add_param(
            inst="instance-002",
            role_name="greeter",
            stop_instance="instance-001",
            is_force=True,
        ),
        make_test_roles_add_param(
            inst="instance-002",
            role_name="greeter",
            rs="replicaset-001",
            stop_instance="instance-001",
            err_msg="all instances in the target replicaset should be online,"
            + " could not connect to: instance-001",
        ),
        make_test_roles_add_param(
            inst="instance-001",
            role_name="greeter",
            is_add_role=True,
            err_msg='role "greeter" already exists',
        ),
        make_test_roles_add_param(
            role_name="greeter",
            is_uri=True,
            err_msg="roles add is not supported for a single instance by"
            + ' "centralized config" orchestrator',
        ),
    ],
)
def test_replicaset_cconfig_roles_add(
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
    is_add_role,
):
    app_name = "test_ccluster_app"
    app_path = os.path.join(tmpdir_with_cfg, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    kv = read_kv(app_path)
    instances = parse_yml(kv["instances"]).keys()

    try:
        start_application(tt_cmd, tmpdir_with_cfg, app_name, instances)

        if stop_instance:
            stop_cmd = [tt_cmd, "stop", "-y", f"{app_name}:{stop_instance}"]
            rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)
            assert rc == 0
        if is_add_role:
            add_first_role_cmd = [
                tt_cmd,
                "rs",
                "roles",
                "add",
                f"{app_name}:{inst}" if inst else app_name,
                role_name,
            ]
            rc, _ = run_command_and_get_output(add_first_role_cmd, cwd=tmpdir_with_cfg)
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
            "add",
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
                assert "Add role to global scope"
                assert role_name in cluster_cfg["roles"]
            if group:
                assert f"Add role to group: {group}"
                assert role_name in cluster_cfg["groups"][group]["roles"]
            if rs:
                assert f"Add role to replicaset: {rs}"
                gr = get_group_by_replicaset_name(cluster_cfg, rs)
                assert role_name in cluster_cfg["groups"][gr]["replicasets"][rs]["roles"]
            if inst_flg or inst:
                i = inst if inst else inst_flg
                assert f"Add role to instance: {i}"
                g, r = get_group_replicaset_by_instance_name(cluster_cfg, i)
                assert (
                    role_name in cluster_cfg["groups"][g]["replicasets"][r]["instances"][i]["roles"]
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
