import re
import subprocess
import time

from utils import run_command_and_get_output, wait_file


def test_cartridge_base_functionality(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    cartridge_name = "test_app"
    create_cmd = [tt_cmd, "cartridge", "create", "--name", cartridge_name]
    create_rc, create_out = run_command_and_get_output(create_cmd, cwd=tmpdir)
    assert create_rc == 0
    assert re.search(r'Application "' + cartridge_name + '" created successfully', create_out)

    build_cmd = [tt_cmd, "cartridge", "build", cartridge_name]
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
        run_dir = str(tmpdir) + "/run/" + cartridge_name + "/" + inst + "/"
        log_dir = str(tmpdir) + "/log/" + cartridge_name + "/" + inst + "/"
        file = wait_file(run_dir, inst + '.pid', [], 10)
        assert file != ""
        file = wait_file(log_dir, inst + '.log', [], 10)
        assert file != ""

        started = False
        trying = 0
        while not started:
            if inst == "stateboard":
                started = True
                break
            if trying == 200:
                break
            with open(log_dir + inst + '.log', "r") as fp:
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
                 "--bootstrap-vshard",
                 "--name", cartridge_name,
                 "--run-dir", str(tmpdir) + "/run/" + cartridge_name]
    setup_rc, setup_out = run_command_and_get_output(setup_cmd, cwd=tmpdir)
    assert setup_rc == 0
    assert re.search(r'Vshard is bootstrapped successfully', setup_out)

    admin_cmd = [tt_cmd, "cartridge", "admin", "probe",
                 "--name", cartridge_name,
                 "--uri", "localhost:3301",
                 "--run-dir", str(tmpdir) + "/run/" + cartridge_name]
    admin_rc, admin_out = run_command_and_get_output(admin_cmd, cwd=tmpdir)
    assert admin_rc == 0
    assert re.search(r'Probe "localhost:3301": OK', admin_out)

    stop_cmd = [tt_cmd, "stop", cartridge_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
