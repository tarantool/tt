import os
import shutil
import subprocess
import tempfile

import pytest
from replicaset_helpers import start_application, stop_application
from vshard_cluster import VshardCluster

from utils import get_tarantool_version, run_command_and_get_output, wait_file

tarantool_major_version, _ = get_tarantool_version()


def run_command_on_instance(tt_cmd, tmpdir, full_inst_name, cmd):
    con_cmd = [tt_cmd, "connect", full_inst_name, "-f", "-"]
    instance_process = subprocess.Popen(
        con_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    instance_process.stdin.writelines([cmd])
    instance_process.stdin.close()
    output = instance_process.stdout.read()
    return output


@pytest.mark.skipif(
    tarantool_major_version < 3, reason="skip centralized config test for Tarantool < 3"
)
def test_upgrade_multi_master(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "start", app_name]
        rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir)
        assert rc == 0

        for i in range(1, 6):
            file = wait_file(
                os.path.join(tmpdir, app_name), f"ready-instance-00{i}", []
            )
            assert file != ""

        status_cmd = [tt_cmd, "replicaset", "upgrade", app_name]

        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 1
        assert "replicaset-002: error" in out and "are both masters" in out

    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


def test_upgrade_t2_app_dummy_replicaset(tt_cmd):
    app_name = "single-t2-app"
    test_app_path_src = os.path.join(os.path.dirname(__file__), app_name)

    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, app_name)
        shutil.copytree(test_app_path_src, test_app_path)
        memtx_dir = os.path.join(test_app_path, "var", "lib", app_name)
        os.makedirs(memtx_dir, exist_ok=True)

        try:
            start_cmd = [tt_cmd, "start", app_name]
            rc, out = run_command_and_get_output(start_cmd, cwd=test_app_path)
            assert rc == 0

            file = wait_file(test_app_path, "ready", [])
            assert file != ""

            # Downgrade schema.
            out = run_command_on_instance(
                tt_cmd,
                test_app_path,
                app_name,
                "box.schema.downgrade('2.8.2') box.snapshot()",
            )

            upgrade_cmd = [tt_cmd, "replicaset", "upgrade", app_name, "--custom"]
            rc, out = run_command_and_get_output(upgrade_cmd, cwd=test_app_path)
            assert rc == 0
            # Out is `• <uuid>: ok` because the instance has no name.
            assert "ok" in out
        finally:
            stop_application(tt_cmd, app_name, test_app_path, [])


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip test with cluster config for Tarantool < 3")
def test_upgrade_downgraded_cluster_replicasets(tt_cmd, tmp_path):
    app_name = "vshard_app"
    replicasets = {
        "router-001": ["router-001-a"],
        "storage-001": ["storage-001-a", "storage-001-b"],
        "storage-002": ["storage-002-a", "storage-002-b"],
    }
    app = VshardCluster(tt_cmd, tmp_path, app_name)
    try:
        app.build()
        app.start()
        cmd_master = '''box.space._schema:run_triggers(false)
box.space._schema:delete('replicaset_name')
box.space._schema:run_triggers(true)

box.space._cluster:run_triggers(false)
box.atomic(function()
    for _, tuple in box.space._cluster:pairs() do
        pcall(box.space._cluster.update, box.space._cluster, {tuple.id}, {{'#', 'name', 1}})
    end
end)
box.space._cluster:run_triggers(true)
box.schema.downgrade('2.11.1')
box.snapshot()
        '''

        # Downgrade cluster.
        for _, replicaset in replicasets.items():
            for replica in replicaset:
                out = run_command_on_instance(
                    tt_cmd,
                    tmp_path,
                    f"{app_name}:{replica}",
                    "box.cfg{force_recovery=true} return box.cfg.force_recovery"
                )
                assert "true" in out

        for _, replicaset in replicasets.items():
            _ = run_command_on_instance(
                tt_cmd,
                tmp_path,
                f"{app_name}:{replicaset[0]}",
                cmd_master
            )

        for _, replicaset in replicasets.items():
            if len(replicaset) == 2:
                _ = run_command_on_instance(
                    tt_cmd,
                    tmp_path,
                    f"{app_name}:{replicaset[1]}",
                    "box.snapshot()"
                )

        for _, replicaset in replicasets.items():
            for replica in replicaset:
                out = run_command_on_instance(
                    tt_cmd,
                    tmp_path,
                    f"{app_name}:{replica}",
                    "box.cfg{force_recovery=false} return box.cfg.force_recovery"
                )
                assert "false" in out

        # Can't create data (old schema).
        out = run_command_on_instance(
            tt_cmd,
            tmp_path,
            f"{app_name}:storage-001-a",
            "box.schema.space.create('example_space')"
        )
        assert "error: Your schema version is 2.11.1" in out

        # For some reason, the storage-002 replica set is having problems with
        # replication after downgrade. For now check only replicaset storage-001.
        upgrade_cmd = [tt_cmd, "replicaset", "upgrade", app_name, "-t=15"]
        rc, out = run_command_and_get_output(upgrade_cmd, cwd=tmp_path)

        assert rc == 0

        upgrade_out = out.strip().split("\n")
        assert len(upgrade_out) == len(replicasets)

        for i in range(len(replicasets)):
            assert "ok" in upgrade_out[i]

        # Create data (new schema).
        out = run_command_on_instance(
            tt_cmd,
            tmp_path,
            f"{app_name}:storage-001-a",
            "box.schema.space.create('example_space')"
        )
        assert "name: example_space" in out

    finally:
        app.stop()


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_upgrade_remote_replicasets(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "small_cluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    instances = ['storage-master', 'storage-replica']

    try:
        start_application(tt_cmd, tmpdir, app_name, instances)
        uri = "tcp://client:secret@127.0.0.1:3301"
        upgrade_cmd = [tt_cmd, "replicaset", "upgrade", uri, "-t=15"]
        rc, out = run_command_and_get_output(upgrade_cmd, cwd=tmpdir)
        assert rc == 0
        assert "ok" in out

    finally:
        stop_cmd = [tt_cmd, "stop", app_name, "-y"]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert stop_rc == 0
