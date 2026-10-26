import glob
import os
from pathlib import Path

import pytest
from retry import retry

from utils import (
    get_tarantool_version,
    inst_lib_dir,
    inst_log_dir,
    log_file,
    run_command_and_get_output,
    wait_string_in_file,
)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


def test_rebootstrap_custom_replicaset(tt_cmd, tmp_path):
    # A replicaset of instances that configure themselves in their scripts:
    # tt replicaset rebootstraps a replica of it with the custom orchestrator,
    # which an application without a cluster config gets (decision 36).
    app_path = tmp_path / "app"
    template = app_path / ".templates" / "default"
    template.mkdir(parents=True)

    lua = """box.cfg{
    listen='localhost:%d',
    replication={
    'replicator:password@localhost:4401',
    'replicator:password@localhost:4402',
    'replicator:password@localhost:4403'},
    read_only=%s,
}
box.once("schema", function()
  box.schema.user.create('replicator', {password = 'password'})
  box.schema.user.grant('replicator', 'replication')
end)
"""
    for data in [
        ["leader.init.lua", 4401, "false"],
        ["s1.init.lua", 4402, "true"],
        ["s2.init.lua", 4403, "true"],
    ]:
        with open(app_path.joinpath(data[0]), "w") as f:
            f.write(lua % (data[1], data[2]))

    with open(template / "instances.yaml", "w") as f:
        f.write("""leader:
  script: leader.init.lua
s1:
  script: s1.init.lua
s2:
  script: s2.init.lua
""")

    try:
        cmd = [tt_cmd, "instance", "start"]
        rc, _ = run_command_and_get_output(cmd, cwd=app_path)
        assert rc == 0

        for inst in ["leader", "s1", "s2"]:
            wait_string_in_file(
                Path(inst_log_dir(app_path, "app", inst)) / log_file,
                "entering the event loop",
            )

        lib_dir = Path(inst_lib_dir(app_path, "app", "s1"))

        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0

        oldSnapName = os.path.basename(snaps[0])

        inst_log = Path(inst_log_dir(app_path, "app", "s1")) / log_file
        os.remove(inst_log)
        rs_cmd = [tt_cmd, "-V", "replicaset", "rebootstrap", "app:s1"]
        rs_rc, _ = run_command_and_get_output(rs_cmd, cwd=app_path, input="y\n")
        assert rs_rc == 0

        # The replica joins the replicaset anew and gets its data from the
        # leader.
        wait_string_in_file(inst_log, "final data received")

        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0
        for snap in snaps:
            assert oldSnapName != os.path.basename(snaps[0])

    finally:
        run_command_and_get_output([tt_cmd, "instance", "stop", "-y"], cwd=app_path)


@pytest.mark.skipif(tarantool_major_version < 3, reason="skip cconfig test for Tarantool < 3")
@pytest.mark.parametrize(
    "flags,input",
    [
        pytest.param([], "y\n"),
        pytest.param(["-y"], None),
    ],
)
def test_rebootstrap_cconfig_replicaset(tt_cmd, vshard_app, flags, input):
    try:
        vshard_app.start()

        inst_name = "storage-002-b"

        lib_dir = Path(inst_lib_dir(vshard_app.app_dir, vshard_app.app_name, inst_name))
        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0

        oldSnapName = os.path.basename(snaps[0])

        inst_log = Path(inst_log_dir(vshard_app.app_dir, vshard_app.app_name, inst_name)) / log_file
        os.remove(inst_log)
        rs_cmd = [tt_cmd, "-V", "replicaset", "rebootstrap", f"{vshard_app.app_name}:{inst_name}"]
        rs_cmd.extend(flags)
        rs_rc, _ = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir, input=input)
        assert rs_rc == 0

        wait_string_in_file(inst_log, "subscribed replica")

        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0
        for snap in snaps:
            assert oldSnapName != os.path.basename(snaps[0])

        @retry(Exception, tries=20, delay=0.5)
        def wait_for_buckets_count():
            assert "1500" in vshard_app.eval(
                inst_name,
                "require('vshard').storage.info().bucket.active",
            )

        wait_for_buckets_count()

    finally:
        vshard_app.stop()


@pytest.mark.skipif(tarantool_major_version < 3, reason="skip cconfig test for Tarantool < 3")
def test_rebootstrap_not_confirmed(tt_cmd, vshard_app):
    try:
        vshard_app.start()

        inst_name = "storage-002-b"

        lib_dir = Path(inst_lib_dir(vshard_app.app_dir, vshard_app.app_name, inst_name))
        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0

        oldSnapName = os.path.basename(snaps[0])

        rs_cmd = [tt_cmd, "-V", "replicaset", "rebootstrap", f"{vshard_app.app_name}:{inst_name}"]
        rs_rc, _ = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir, input="n\n")
        assert rs_rc == 0

        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0
        for snap in snaps:
            assert oldSnapName == os.path.basename(snaps[0])

    finally:
        vshard_app.stop()


@pytest.mark.skipif(tarantool_major_version < 3, reason="skip cconfig test for Tarantool < 3")
def test_rebootstrap_already_stopped(tt_cmd, vshard_app):
    try:
        vshard_app.start()

        inst_name = "storage-002-b"

        lib_dir = Path(inst_lib_dir(vshard_app.app_dir, vshard_app.app_name, inst_name))
        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0

        oldSnapName = os.path.basename(snaps[0])

        vshard_app.stop(inst_name)

        inst_log = Path(inst_log_dir(vshard_app.app_dir, vshard_app.app_name, inst_name)) / log_file
        os.remove(inst_log)
        rs_cmd = [tt_cmd, "-V", "replicaset", "rebootstrap", f"{vshard_app.app_name}:{inst_name}"]
        rs_rc, _ = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir, input="y\n")
        assert rs_rc == 0

        wait_string_in_file(inst_log, "subscribed replica")

        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0
        for snap in snaps:
            assert oldSnapName != os.path.basename(snaps[0])

    finally:
        vshard_app.stop()


def test_rebootstrap_bad_cli_args(tt_cmd, vshard_app):
    # No inst name.
    rs_cmd = [tt_cmd, "replicaset", "rebootstrap", f"{vshard_app.app_name}"]
    rs_rc, out = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir)
    assert rs_rc != 0
    assert "instance name is not specified" in out

    # Non-existing instance.
    rs_cmd = [tt_cmd, "replicaset", "rebootstrap", f"{vshard_app.app_name}:inst"]
    rs_rc, out = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir)
    assert rs_rc != 0
    assert f'instance not found: "inst" of "{vshard_app.app_name}"' in out

    # Non-existing app.
    rs_cmd = [tt_cmd, "replicaset", "rebootstrap", "app:inst"]
    rs_rc, out = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir)
    assert rs_rc != 0
    assert "application not found" in out

    # No args.
    rs_cmd = [tt_cmd, "replicaset", "rebootstrap"]
    rs_rc, out = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir)
    assert rs_rc != 0
    assert "instance for rebootstrap is not specified" in out

    # Multiple instances.
    rs_cmd = [tt_cmd, "replicaset", "rebootstrap", "app:inst1", "app:inst2"]
    rs_rc, out = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir)
    assert rs_rc != 0
    assert "only one instance supported for re-bootstrap" in out
