import glob
import os
from pathlib import Path

import pytest
from cartridge_helper import cartridge_name
from replicaset_helpers import eval_on_instance
from retry import retry

from utils import (get_tarantool_version, lib_path, log_file, log_path,
                   run_command_and_get_output, wait_string_in_file)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip cartridge test for Tarantool > 2")
def test_rebootstrap_cartridge_instance(tt_cmd, cartridge_app):
    inst_name = "s2-replica-1"
    lib_dir = Path(cartridge_app.workdir).joinpath(cartridge_name, lib_path, inst_name)
    inst_log = os.path.join(cartridge_app.workdir, cartridge_name, log_path,
                            inst_name, log_file)
    wait_string_in_file(inst_log, "Backup of active config created")
    snaps = glob.glob((lib_dir / "*.snap").as_posix())
    assert len(snaps) > 0

    @retry(Exception, tries=10, delay=1.)
    def wait_for_buckets_count():
        assert "true" in eval_on_instance(tt_cmd, cartridge_name, inst_name,
                                          cartridge_app.workdir,
                                          "require('vshard').storage.info().bucket.active > 0")
    wait_for_buckets_count()

    oldSnapName = os.path.basename(snaps[0])

    os.remove(inst_log)
    rs_cmd = [tt_cmd, "-V", "replicaset", "rebootstrap", f"{cartridge_name}:{inst_name}"]
    rs_rc, _ = run_command_and_get_output(rs_cmd, cwd=cartridge_app.workdir, input='y\n')
    assert rs_rc == 0

    wait_string_in_file(inst_log, "ConfiguringRoles -> RolesConfigured")

    snaps = glob.glob((lib_dir / "*.snap").as_posix())
    assert len(snaps) > 0
    for snap in snaps:
        assert oldSnapName != os.path.basename(snaps[0])

    wait_for_buckets_count()


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom replicaset test for Tarantool > 2")
def test_rebootstrap_custom_replicaset(tt_cmd, tmp_path):
    tmp_path = tmp_path.joinpath("app")
    tmp_path.mkdir(0o755)

    lua = '''box.cfg{
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
'''
    for data in [["leader.init.lua", 4401, "false"],
                 ["s1.init.lua", 4402, "true"],
                 ["s2.init.lua", 4403, "true"]]:
        with open(tmp_path.joinpath(data[0]), "w") as f:
            f.write(lua % (data[1], data[2]))

    with open(tmp_path.joinpath("instances.yml"), "w") as f:
        f.write('''leader:
s1:
s2:''')
    with open(tmp_path.joinpath("tt.yml"), "w") as f:
        f.write('''env:''')

    try:
        cmd = [tt_cmd, "start"]
        rc, _ = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 0

        for inst in ["leader", "s1", "s2"]:
            wait_string_in_file(tmp_path / log_path / inst / log_file, "entering the event loop")

        lib_dir = tmp_path / lib_path / "s1"

        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0

        oldSnapName = os.path.basename(snaps[0])

        inst_log = tmp_path / log_path / "s1" / log_file
        os.remove(inst_log)
        rs_cmd = [tt_cmd, "-V", "replicaset", "rebootstrap", "app:s1"]
        rs_rc, _ = run_command_and_get_output(rs_cmd, cwd=tmp_path, input="y\n")
        assert rs_rc == 0

        wait_string_in_file(inst_log, "replica set sync complete")

        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0
        for snap in snaps:
            assert oldSnapName != os.path.basename(snaps[0])

    finally:
        run_command_and_get_output([tt_cmd, "stop", "-y"], cwd=tmp_path)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cconfig test for Tarantool < 3")
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

        lib_dir = vshard_app.app_dir / lib_path / inst_name
        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0

        oldSnapName = os.path.basename(snaps[0])

        inst_log = vshard_app.app_dir / log_path / inst_name / log_file
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
            assert "1500" in vshard_app.eval(inst_name,
                                             "require('vshard').storage.info().bucket.active")
        wait_for_buckets_count()

    finally:
        vshard_app.stop()


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cconfig test for Tarantool < 3")
def test_rebootstrap_not_confirmed(tt_cmd, vshard_app):
    try:
        vshard_app.start()

        inst_name = "storage-002-b"

        lib_dir = vshard_app.app_dir / lib_path / inst_name
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


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cconfig test for Tarantool < 3")
def test_rebootstrap_already_stopped(tt_cmd, vshard_app):
    try:
        vshard_app.start()

        inst_name = "storage-002-b"

        lib_dir = vshard_app.app_dir / lib_path / inst_name
        snaps = glob.glob((lib_dir / "*.snap").as_posix())
        assert len(snaps) > 0

        oldSnapName = os.path.basename(snaps[0])

        vshard_app.stop(inst_name)

        inst_log = vshard_app.app_dir / log_path / inst_name / log_file
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
    assert "instance \"inst\" is not found" in out

    # Non-existing app.
    rs_cmd = [tt_cmd, "replicaset", "rebootstrap", "app:inst"]
    rs_rc, out = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir)
    assert rs_rc != 0
    assert "can't collect instance information for app:" in out

    # No args.
    rs_cmd = [tt_cmd, "replicaset", "rebootstrap"]
    rs_rc, out = run_command_and_get_output(rs_cmd, cwd=vshard_app.env_dir)
    assert rs_rc != 0
    assert "accepts 1 arg(s), received 0" in out
