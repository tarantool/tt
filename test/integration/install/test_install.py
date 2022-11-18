import os
import re
import subprocess
import tempfile

import pytest
import yaml


@pytest.mark.slow
def test_install_tt(tt_cmd, tmpdir):

    configPath = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    # Install latest tt.
    install_cmd = [tt_cmd, "install", "tt=0.1.0", "--cfg", configPath]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0

    installed_cmd = [tmpdir + "/bin/tt", "version"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmpdir + "/bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = installed_program_process.stdout.readline()
    assert re.search(r"commit: ccd9c0c", start_output)


@pytest.mark.slow
def test_install_tarantool(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "", "inc_dir": "./my_inc"}}}, f)

    tmpdir_without_config = tempfile.mkdtemp()

    # Install latest tarantool.
    install_cmd = [tt_cmd, "install", "tarantool", "-f", "--cfg", config_path]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmpdir_without_config,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process was shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    installed_cmd = [tmpdir + "/bin/tarantool", "-v"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=os.path.join(tmpdir, "/bin"),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)
    assert os.path.exists(os.path.join(tmpdir, "my_inc", "include", "tarantool"))


@pytest.mark.slow
def test_install_tarantool_in_docker(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "", "inc_dir": "./my_inc"}}}, f)

    tmpdir_without_config = tempfile.mkdtemp()

    # Install latest tarantool.
    install_cmd = [tt_cmd, "install", "tarantool", "-f", "--cfg", config_path, "--use-docker"]
    tt_process = subprocess.Popen(
        install_cmd,
        cwd=tmpdir_without_config,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    instance_process_rc = tt_process.wait()
    assert instance_process_rc == 0
    installed_cmd = [tmpdir + "/bin/tarantool", "-v"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=os.path.join(tmpdir, "/bin"),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)

    # Check tarantool glibc version.
    out = subprocess.getoutput("objdump -T " + os.path.join(tmpdir, "bin", "tarantool") +
                               " | grep -o -E 'GLIBC_[.0-9]+' | sort -V | tail -n1")
    assert out == "GLIBC_2.18"

    assert os.path.exists(os.path.join(tmpdir, "my_inc", "include", "tarantool"))


def test_install_tt_in_docker(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "", "inc_dir": "./my_inc"}}}, f)

    tmpdir_without_config = tempfile.mkdtemp()

    install_cmd = [tt_cmd, "install", "tt", "--cfg", config_path, "--use-docker"]
    tt_process = subprocess.Popen(
        install_cmd,
        cwd=tmpdir_without_config,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    tt_process_rc = tt_process.wait()
    assert tt_process_rc == 1
    output = tt_process.stdout.readline()
    assert re.search(r"--use-docker can be used only for 'tarantool' program", output)
