import os
import re
import shutil
import subprocess
import tempfile
from time import sleep

import pytest
from replicaset_helpers import stop_application
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
def test_upgrade_cluster(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    replicasets = [
        "replicaset-001",
        "replicaset-002",
    ]

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

        _ = run_command_on_instance(
            tt_cmd, tmpdir, "test_ccluster_app:instance-004", "box.cfg{read_only=true}"
        )

        # Instance bootstrap may not have finished yet.
        sleep(5)
        upgrade_cmd = [tt_cmd, "replicaset", "upgrade", app_name, "-t=10"]

        rc, out = run_command_and_get_output(upgrade_cmd, cwd=tmpdir)
        assert rc == 0

        upgrade_out = out.strip().split("\n")
        assert len(upgrade_out) == len(replicasets)

        for i in range(len(replicasets)):
            match = re.search(r"•\s*(.*?):\s*(.*)", upgrade_out[i])
            assert match.group(1) in replicasets
            assert match.group(2) == "ok"

    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


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


@pytest.mark.skipif(
    tarantool_major_version < 3, reason="skip centralized config test for Tarantool < 3"
)
def test_upgrade_t2_app_dummy_replicaset(tt_cmd):
    app_name = "single-t2-app"
    test_app_path_src = os.path.join(os.path.dirname(__file__), app_name)

    # snapshot from tarantool 2.11.4 app
    snapfile = os.path.join(test_app_path_src, "00000000000000000004.snap")

    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, app_name)
        shutil.copytree(test_app_path_src, test_app_path)
        memtx_dir = os.path.join(test_app_path, "var", "lib", app_name)
        os.makedirs(memtx_dir, exist_ok=True)
        shutil.copy(snapfile, memtx_dir)

        try:
            start_cmd = [tt_cmd, "start", app_name]
            rc, out = run_command_and_get_output(start_cmd, cwd=test_app_path)
            assert rc == 0

            file = wait_file(test_app_path, "ready", [])
            assert file != ""

            out = run_command_on_instance(
                tt_cmd,
                test_app_path,
                app_name,
                "return box.space.example_space:select{2}",
            )
            assert "[2, 'Second record']" in out

            upgrade_cmd = [tt_cmd, "replicaset", "upgrade", app_name]
            rc, out = run_command_and_get_output(upgrade_cmd, cwd=test_app_path)
            assert rc == 0
            assert out == "• single-t2-app: ok\n"
        finally:
            stop_application(tt_cmd, app_name, test_app_path, [])


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip test with cluster config for Tarantool < 3")
def test_upgrade_downgraded_cluster_replicasets(tt_cmd, tmp_path):
    app_name = "vshard_app"
    replicasets = {
        "router-001": ["router-001-a"],
        "storage-001": ["storage-001-a", "storage-001-b"],
        "storage-002": ["storage-002-a", "storage-001-a"],
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

        # downgrade cluster
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
            if len(replicaset) == 2:
                sleep(3)
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

        # Can't create data (old scheama)
        out = run_command_on_instance(
            tt_cmd,
            tmp_path,
            f"{app_name}:storage-001-a",
            "box.schema.space.create('example_space')"
        )
        assert "error: Your schema version is 2.11.1" in out

        # For some reason, the storage-002 replica set is having problems with
        # replication after downgrade. For now check only replicaset storage-001.
        upgrade_cmd = [tt_cmd, "replicaset", "upgrade", app_name, "-r=storage-001"]
        rc, out = run_command_and_get_output(upgrade_cmd, cwd=tmp_path)

        assert rc == 0
        assert out == "• storage-001: ok\n"

        # Create data (new schema)
        out = run_command_on_instance(
            tt_cmd,
            tmp_path,
            f"{app_name}:storage-001-a",
            "box.schema.space.create('example_space')"
        )
        assert "error: Your schema version is 2.11.1" not in out

    finally:
        app.stop()
