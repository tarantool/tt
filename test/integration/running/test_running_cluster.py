import os
import re
import shutil
import subprocess

import pytest
import yaml

from utils import (control_socket, extract_status, get_tarantool_version,
                   lib_path, log_path, run_command_and_get_output, run_path,
                   wait_event, wait_files, wait_pid_disappear)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


def start_application(cmd, workdir, app_name, instances):
    instance_process = subprocess.Popen(
        cmd,
        cwd=workdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.read()
    for inst in instances:
        assert f"Starting an instance [{app_name}:{inst}]" in start_output


def stop_application(tt_cmd, workdir, app_name, instances):
    stop_cmd = [tt_cmd, "stop", "-y", app_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=workdir)
    assert stop_rc == 0

    for inst in instances:
        assert re.search(rf"The Instance {app_name}:{inst} \(PID = \d+\) has been terminated.",
                         stop_out)
        assert not os.path.exists(os.path.join(workdir, run_path, app_name, inst, "tarantool.pid"))


def wait_cluster_started(tt_cmd, workdir, app_name, instances, inst_conf):
    files = []
    for inst in instances:
        conf = inst_conf(workdir, app_name, inst)
        files.append(conf['pid_file'])
        files.append(conf['console_socket'])
    assert wait_files(5, files)

    def are_all_box_statuses_running():
        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=workdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in instances:
            inst_id = app_name + ":" + inst
            if status_info[inst_id].get("BOX") != "running":
                return False
        return True
    assert wait_event(5, are_all_box_statuses_running)


def default_inst_conf(workdir, app_name, inst):
    app_path = os.path.join(workdir, app_name)
    return {
        'pid_file': os.path.join(app_path, run_path, inst, 'tarantool.pid'),
        'log_file': os.path.join(app_path, log_path, inst, 'tt.log'),
        'console_socket': os.path.join(app_path, run_path, inst, control_socket),
        'wal_dir': os.path.join(app_path, lib_path),
        'snapshot_dir': os.path.join(app_path, lib_path),
    }


def check_base_functionality(tt_cmd, tmpdir, app_name, instances, inst_conf):
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    try:
        # Start an instance.
        start_cmd = [tt_cmd, "start", app_name]
        start_application(start_cmd, tmpdir, app_name, instances)
        wait_cluster_started(tt_cmd, tmpdir, app_name, instances, inst_conf)

        # Check status.
        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        pidByInstanceName = {}
        for inst in instances:
            assert status_info[f"{app_name}:{inst}"]["STATUS"] == "RUNNING"
            conf = inst_conf(tmpdir, app_name, inst)
            with open(conf['pid_file']) as f:
                pidByInstanceName[inst] = f.readline()
            assert os.path.exists(conf['wal_dir'])
            assert os.path.exists(conf['snapshot_dir'])
            assert os.path.exists(conf['log_file'])
            if inst_conf != default_inst_conf:
                assert os.path.exists(default_inst_conf(tmpdir, app_name, inst)['log_file'])
            assert not os.path.exists(
                os.path.join(os.path.dirname(conf['log_file']), "tarantool.log"))

        # Test connection.
        for inst in instances:
            start_cmd = [tt_cmd, "connect", f"{app_name}:{inst}", "-f", "-"]
            instance_process = subprocess.Popen(
                start_cmd,
                cwd=tmpdir,
                stderr=subprocess.STDOUT,
                stdout=subprocess.PIPE,
                stdin=subprocess.PIPE,
                text=True
            )
            instance_process.stdin.writelines(["6*7"])
            instance_process.stdin.close()
            output = instance_process.stdout.read()
            assert "42" in output

        # Restart an application.
        restart_cmd = [tt_cmd, "restart", app_name, "-y"]
        instance_process = subprocess.Popen(
            restart_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        start_output = instance_process.stdout.read()

        # Need to wait for all instances to stop before start.
        for inst in instances:
            # Wait when PID that was fetched on start disappears.
            wait_pid_disappear(inst_conf(tmpdir, app_name, inst)['pid_file'],
                               pidByInstanceName.get(inst))

        for inst in instances:
            assert f"Starting an instance [{app_name}:{inst}]" in start_output

        wait_cluster_started(tt_cmd, tmpdir, app_name, instances, inst_conf)

        # Check status.
        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in instances:
            assert status_info[f"{app_name}:{inst}"]["STATUS"] == "RUNNING"
            with open(inst_conf(tmpdir, app_name, inst)['pid_file']) as f:
                assert pidByInstanceName[inst] != f.readline()

    finally:
        stop_application(tt_cmd, tmpdir, app_name, instances)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_running_base_functionality(tt_cmd, tmpdir_with_cfg):
    app_name = "small_cluster_app"
    instances = ['storage-master', 'storage-replica']
    inst_conf = default_inst_conf
    check_base_functionality(tt_cmd, tmpdir_with_cfg, app_name, instances, inst_conf)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
@pytest.mark.slow
def test_running_base_functionality_crud_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = 'cluster_crud_app'
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    # Build app.
    build_cmd = [tt_cmd, "build", app_name]
    instance_process = subprocess.Popen(
        build_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    rc = instance_process.wait()
    assert rc == 0
    assert "Application was successfully built" in instance_process.stdout.read()

    instances = ['router', 'storage1', 'storage2']
    inst_conf = default_inst_conf

    try:
        # Start an application.
        start_cmd = [tt_cmd, "start", app_name]
        start_application(start_cmd, tmpdir, app_name, instances)
        wait_cluster_started(tt_cmd, tmpdir, app_name, instances, inst_conf)

        # Check status.
        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in instances:
            assert status_info[f"{app_name}:{inst}"]["STATUS"] == "RUNNING"

    finally:
        stop_application(tt_cmd, tmpdir, app_name, instances)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_running_base_functionality_error_cases(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "small_cluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    # Start unknown instance.
    start_cmd = [tt_cmd, "start", f"{app_name}:unknown"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.read()
    assert f"can't collect instance information for {app_name}:unknown: "
    "instance(s) not found" in start_output
    rc = instance_process.wait(5)
    assert rc != 0

    # Remove instances.yml.
    os.remove(os.path.join(app_path, "instances.yml"))
    # Start storage-master instance.
    start_cmd = [tt_cmd, "start", f"{app_name}:storage-master"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.read()
    assert r'instances config (instances.yml) is missing' in start_output
    rc = instance_process.wait(5)
    assert rc != 0


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="test is for tnt version < 3")
def test_cluster_config_not_supported(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "small_cluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    # Start storage-master instance.
    start_cmd = [tt_cmd, "start", f"{app_name}:storage-master"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.read()
    print(start_output)
    assert r"cluster config is supported by Tarantool starting from version 3.0." in start_output
    rc = instance_process.wait(5)
    assert rc != 0


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_cluster_cfg_changes_defaults(tt_cmd, tmpdir_with_cfg):
    app_name = "cluster_app_changed_defaults"
    instances = ['master']

    def inst_conf(workdir, app_name, inst):
        app_path = os.path.join(workdir, app_name)
        return {
            'pid_file': os.path.join(app_path, f'run_{inst}.pid'),
            'console_socket': os.path.join(app_path, f'run_{inst}.control'),
            'wal_dir': os.path.join(app_path, lib_path, f"{inst}_wal"),
            'snapshot_dir': os.path.join(app_path, lib_path, f"{inst}_snapshot"),
            'log_file': os.path.join(app_path, "tnt_" + inst + ".log"),
        }
    check_base_functionality(tt_cmd, tmpdir_with_cfg, app_name, instances, inst_conf)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_cluster_error_cases(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "cluster_app_changed_defaults"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    cluster_config = os.path.join(app_path, "instances.yml")
    with open(cluster_config, "w") as f:
        yaml.dump({"master": {}, "storage": {}}, f)  # No storage instance in cluster config.

    start_cmd = [tt_cmd, "start", app_name]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    rc = instance_process.wait()
    assert rc != 0
    start_output = instance_process.stdout.read()
    assert 'an instance "storage" not found' in start_output
