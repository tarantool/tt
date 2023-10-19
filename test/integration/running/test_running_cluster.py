import os
import re
import shutil
import subprocess

import pytest
import yaml

from utils import (control_socket, extract_status, get_tarantool_version,
                   run_command_and_get_output, run_path, wait_file)

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


def stop_application(tt_cmd, app_name, workdir, instances):
    stop_cmd = [tt_cmd, "stop", app_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=workdir)
    assert stop_rc == 0

    for inst in instances:
        assert re.search(rf"The Instance {app_name}:{inst} \(PID = \d+\) has been terminated.",
                         stop_out)
        assert not os.path.exists(os.path.join(workdir, run_path, app_name, inst, "tarantool.pid"))


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_running_base_functionality(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "small_cluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    run_dir = os.path.join(tmpdir, app_name, run_path)
    try:
        # Start an instance.
        start_cmd = [tt_cmd, "start", app_name]
        start_application(start_cmd, tmpdir, app_name, ['master', 'storage'])

        # Check status.
        pidByInstanceName = {}
        for inst in ['master', 'storage']:
            file = wait_file(os.path.join(run_dir, inst), 'tarantool.pid', [])
            assert file != ""
            file = wait_file(os.path.join(run_dir, inst), control_socket, [])
            assert file != ""
            with open(os.path.join(run_dir, inst, 'tarantool.pid')) as f:
                pidByInstanceName[inst] = f.readline()

        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in ['master', 'storage']:
            assert status_info[app_name+":"+inst]["STATUS"] == "RUNNING"
            assert os.path.exists(os.path.join(tmpdir, app_name, "var", "lib", inst))
            assert os.path.exists(os.path.join(tmpdir, app_name, "var", "log", inst, "tt.log"))
            assert os.path.exists(os.path.join(tmpdir, app_name, "var", "log", inst,
                                               "tarantool.log"))

        # Test connection.
        for inst in ['master', 'storage']:
            start_cmd = [tt_cmd, "connect", app_name + ":" + inst, "-f", "-"]
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
        assert f"Starting an instance [{app_name}:master]" in start_output
        assert f"Starting an instance [{app_name}:storage]" in start_output

        # Check status.
        for inst in ['master', 'storage']:
            file = wait_file(os.path.join(run_dir, inst), 'tarantool.pid', [])
            assert file != ""
            file = wait_file(os.path.join(run_dir, inst), control_socket, [])
            assert file != ""

            with open(os.path.join(run_dir, inst, 'tarantool.pid')) as f:
                assert pidByInstanceName[inst] != f.readline()

        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in ['master', 'storage']:
            assert status_info[app_name+":"+inst]["STATUS"] == "RUNNING"

    finally:
        stop_application(tt_cmd, app_name, tmpdir, ['master', 'storage'])


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

    run_dir = os.path.join(tmpdir, app_name, run_path)

    try:
        # Start an application.
        start_cmd = [tt_cmd, "start", app_name]
        start_application(start_cmd, tmpdir, app_name, ['router', 'storage1', 'storage2'])

        # Check status.
        for inst in ['router', 'storage1', 'storage2']:
            file = wait_file(os.path.join(run_dir, inst), 'tarantool.pid', [])
            assert file != ""
            file = wait_file(os.path.join(run_dir, inst), control_socket, [])
            assert file != ""

        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in ['router', 'storage1', 'storage2']:
            assert status_info["cluster_crud_app:"+inst]["STATUS"] == "RUNNING"

    finally:
        stop_application(tt_cmd, app_name, tmpdir, ['router', 'storage1', 'storage2'])


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
    assert r"can't find an application init file: instance(s) not found" in start_output
    rc = instance_process.wait(5)
    assert rc != 0

    # Remove instances.yml.
    os.remove(os.path.join(app_path, "instances.yml"))
    # Start master instance.
    start_cmd = [tt_cmd, "start", f"{app_name}:master"]
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

    # Start unknown instance.
    start_cmd = [tt_cmd, "start", f"{app_name}:master"]
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
    tmpdir = tmpdir_with_cfg
    app_name = "cluster_app_changed_defaults"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    try:
        # Start an instance.
        start_cmd = [tt_cmd, "start", app_name]
        start_application(start_cmd, tmpdir, app_name, ['master'])

        # Check status.
        pidByInstanceName = {}
        for inst in ['master']:
            file = wait_file(app_path, "run_" + inst + '.pid', [])
            assert file != ""
            file = wait_file(app_path, "run_" + inst + ".control", [])
            assert file != ""
            with open(os.path.join(app_path, "run_" + inst + '.pid')) as f:
                pidByInstanceName[inst] = f.readline()

        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in ['master']:
            assert status_info[app_name+":"+inst]["STATUS"] == "RUNNING"
            assert os.path.exists(os.path.join(app_path, "var", "lib", inst + "_wal"))
            assert os.path.exists(os.path.join(app_path, "var", "lib", inst + "_snapshot"))
            assert os.path.exists(os.path.join(app_path, "var", "log", inst, "tt.log"))
            assert os.path.exists(os.path.join(app_path, "tnt_" + inst + ".log"))

        # Test connection.
        for inst in ['master']:
            start_cmd = [tt_cmd, "connect", app_name + ":" + inst, "-f", "-"]
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
        assert f"Starting an instance [{app_name}:master]" in start_output

        # Check status.
        for inst in ['master']:
            file = wait_file(app_path, "run_" + inst + '.pid', [])
            assert file != ""
            file = wait_file(app_path, "run_" + inst + ".control", [])
            assert file != ""
            with open(os.path.join(app_path, "run_" + inst + '.pid')) as f:
                assert pidByInstanceName[inst] != f.readline()

        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in ['master']:
            assert status_info[app_name+":"+inst]["STATUS"] == "RUNNING"

    finally:
        stop_application(tt_cmd, app_name, tmpdir, ['master'])


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_cluster_error_cases(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
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
