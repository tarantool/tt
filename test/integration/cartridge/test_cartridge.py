import os
import re
import subprocess
import time

import pytest
import yaml

import utils
from utils import run_command_and_get_output, wait_file

cartridge_name = "test_app"


@pytest.fixture(autouse=True)
def stop_cartridge_app(tt_cmd, tmpdir):
    # Run test.
    yield
    # Stop instance if it is still running.
    status_cmd = [tt_cmd, "status", cartridge_name]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    if re.search(": RUNNING.", status_out):
        stop_cmd = [tt_cmd, "stop", cartridge_name]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert stop_rc == 0


def test_cartridge_base_functionality(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    create_cmd = [tt_cmd, "create", "cartridge", "--name", cartridge_name]
    create_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    create_process.stdin.writelines(["foo\n"])
    create_process.stdin.close()
    create_process.wait()

    assert create_process.returncode == 0
    create_out = create_process.stdout.read()
    assert re.search(r"Application '" + cartridge_name + "' created successfully", create_out)

    build_cmd = [tt_cmd, "build", cartridge_name]
    build_rc, build_out = run_command_and_get_output(build_cmd, cwd=tmpdir)
    assert build_rc == 0
    assert re.search(r'Application was successfully built', build_out)

    start_cmd = [tt_cmd, "start", cartridge_name]
    subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    instances = ["router", "stateboard", "s1-master", "s1-replica", "s2-master", "s2-replica"]

    # Wait for the full start of the cartridge.
    for inst in instances:
        run_dir = os.path.join(tmpdir, utils.run_path, cartridge_name, inst)
        log_dir = os.path.join(tmpdir, utils.log_path, cartridge_name, inst)

        file = wait_file(run_dir, inst + '.pid', [])
        assert file != ""
        file = wait_file(log_dir, inst + '.log', [])
        assert file != ""

        started = False
        trying = 0
        while not started:
            if inst == "stateboard":
                started = True
                break
            if trying == 200:
                break
            with open(os.path.join(log_dir, inst + '.log'), "r") as fp:
                lines = fp.readlines()
                lines = [line.rstrip() for line in lines]
            for line in lines:
                if re.search("Set default metrics endpoints", line):
                    started = True
                    break
            fp.close()
            time.sleep(0.05)
            trying = trying + 1

        assert started is True

    try:
        setup_cmd = [tt_cmd, "cartridge", "replicasets", "setup",
                     "--bootstrap-vshard",
                     "--name", cartridge_name,
                     "--run-dir", os.path.join(tmpdir, "var", "run", cartridge_name)]
        setup_rc, setup_out = run_command_and_get_output(setup_cmd, cwd=tmpdir)
        assert setup_rc == 0
        assert re.search(r'Bootstrap vshard task completed successfully', setup_out)

        admin_cmd = [tt_cmd, "cartridge", "admin", "probe",
                     "--conn", "admin:foo@localhost:3301",
                     "--uri", "localhost:3301",
                     "--run-dir", os.path.join(tmpdir, utils.run_path, cartridge_name)]
        admin_rc, admin_out = run_command_and_get_output(admin_cmd, cwd=tmpdir)
        assert admin_rc == 0
        assert re.search(r'Probe "localhost:3301": OK', admin_out)

        # Admin call without --run-dir.
        admin_cmd = [tt_cmd, "cartridge", "admin", "probe",
                     "--conn", "admin:foo@localhost:3301",
                     "--uri", "localhost:3301"]
        admin_rc, admin_out = run_command_and_get_output(admin_cmd, cwd=tmpdir)
        assert admin_rc == 0
        assert re.search(r'Probe "localhost:3301": OK', admin_out)

        # Test replicasets list without --run-dir.
        rs_cmd = [tt_cmd, "cartridge", "replicasets", "list", "--name", cartridge_name]
        rs_rc, rs_out = run_command_and_get_output(rs_cmd, cwd=tmpdir)
        assert rs_rc == 0
        assert 'Current replica sets:' in rs_out
        assert 'Role: failover-coordinator | vshard-router | app.roles.custom' in rs_out

    finally:
        stop_cmd = [tt_cmd, "stop", cartridge_name]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert stop_rc == 0


def test_cartridge_base_functionality_in_app_dir(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    create_cmd = [tt_cmd, "create", "cartridge", "--name", cartridge_name]
    create_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    create_process.stdin.writelines(["foo\n"])
    create_process.stdin.close()
    create_process.wait()

    assert create_process.returncode == 0
    create_out = create_process.stdout.read()
    assert re.search(r"Application '" + cartridge_name + "' created successfully", create_out)

    app_dir = os.path.join(tmpdir, cartridge_name)

    # Add cartridge config to simulate existing cartridge app.
    config_path = os.path.join(app_dir, ".cartridge.yml")
    with open(config_path, "w") as f:
        yaml.dump({"stateboard": True}, f)

    # Generate tt env in application directory.
    cmd = [tt_cmd, "init"]
    rc, out = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 0
    assert 'Environment config is written to ' in out

    # Generate tt env in application directory.
    cmd = [tt_cmd, "build"]
    rc, out = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 0
    assert 'Application was successfully built' in out

    cmd = [tt_cmd, "start"]
    subprocess.Popen(
        cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    instances = ["router", "stateboard", "s1-master", "s1-replica", "s2-master", "s2-replica"]

    # Wait for the full start of the cartridge.
    try:
        for inst in instances:
            run_dir = os.path.join(app_dir, utils.run_path, cartridge_name, inst)
            log_dir = os.path.join(app_dir, utils.log_path, cartridge_name, inst)

            file = wait_file(run_dir, inst + '.pid', [])
            assert file != ""
            file = wait_file(log_dir, inst + '.log', [])
            assert file != ""

            started = False
            trying = 0
            while not started:
                if inst == "stateboard":
                    started = True
                    break
                if trying == 200:
                    break
                with open(os.path.join(log_dir, inst + '.log'), "r") as fp:
                    lines = fp.readlines()
                    lines = [line.rstrip() for line in lines]
                for line in lines:
                    if re.search("Set default metrics endpoints", line):
                        started = True
                        break
                fp.close()
                time.sleep(0.05)
                trying = trying + 1

            assert started is True

        setup_cmd = [tt_cmd, "cartridge", "replicasets", "setup",
                     "--bootstrap-vshard"]
        setup_rc, setup_out = run_command_and_get_output(setup_cmd, cwd=app_dir)
        assert setup_rc == 0
        assert 'Bootstrap vshard task completed successfully' in setup_out

        # Test replicasets list without run-dir and app name
        rs_cmd = [tt_cmd, "cartridge", "replicasets", "list"]
        rs_rc, rs_out = run_command_and_get_output(rs_cmd, cwd=app_dir)
        assert rs_rc == 0
        assert 'Current replica sets:' in rs_out
        assert 'Role: failover-coordinator | vshard-router | app.roles.custom' in rs_out

        # Admin call without --run-dir.
        admin_cmd = [tt_cmd, "cartridge", "admin", "probe",
                     "--conn", "admin:foo@localhost:3301",
                     "--uri", "localhost:3301"]
        admin_rc, admin_out = run_command_and_get_output(admin_cmd, cwd=app_dir)
        assert admin_rc == 0
        assert 'Probe "localhost:3301": OK' in admin_out

        # Failover command.
        failover_cmd = [tt_cmd, "cartridge", "failover", "status"]
        failover_rc, failover_out = run_command_and_get_output(failover_cmd, cwd=app_dir)
        assert failover_rc == 0
        assert 'Current failover status:' in failover_out

        stop_cmd = [tt_cmd, "stop"]
        stop_rc, _ = run_command_and_get_output(stop_cmd, cwd=app_dir)
        assert stop_rc == 0

    finally:
        run_command_and_get_output([tt_cmd, "stop"], cwd=app_dir)
