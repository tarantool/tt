import os
import platform
import re
import subprocess
import tempfile

import pytest
import yaml

from utils import config_name


@pytest.mark.slow
def test_install_tt(tt_cmd, tmpdir):

    configPath = os.path.join(tmpdir, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    # Install latest tt.
    install_cmd = [tt_cmd, "--cfg", configPath, "install", "tt"]
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
    assert re.search(r"Tarantool CLI version \d+.\d+.\d+", start_output)


@pytest.mark.slow
def test_install_tt_specific_version(tt_cmd, tmpdir):

    configPath = os.path.join(tmpdir, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    # Install latest tt.
    install_cmd = [tt_cmd, "--cfg", configPath, "install", "tt", "1.0.0"]
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
    assert re.search(r"Tarantool CLI version 1.0.0", start_output)


@pytest.mark.slow
def test_install_tarantool(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "", "inc_dir": "./my_inc"}}}, f)

    tmpdir_without_config = tempfile.mkdtemp()

    # Install latest tarantool.
    install_cmd = [tt_cmd, "--cfg", config_path, "install", "-f", "tarantool", "2.10.4"]
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
    assert os.path.exists(os.path.join(tmpdir, "bin", "tarantool_2.10.4"))


@pytest.mark.slow
def test_install_tarantool_in_docker(tt_cmd, tmpdir):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    config_path = os.path.join(tmpdir, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "", "inc_dir": "./my_inc"}}}, f)

    tmpdir_without_config = tempfile.mkdtemp()

    # Install latest tarantool.
    install_cmd = [tt_cmd, "--cfg", config_path, "install", "-f", "tarantool", "--use-docker"]
    tt_process = subprocess.Popen(
        install_cmd,
        cwd=tmpdir_without_config,
        stderr=subprocess.STDOUT,
        # Do not use pipe for stdout, if you are not going to read from it.
        # In case of build failure, docker logs are printed to stdout. It fills pipe buffer and
        # blocks all subsequent stdout write calls in tt, because there is no pipe reader in test.
        stdout=subprocess.DEVNULL,
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
    assert out == "GLIBC_2.27"

    assert os.path.exists(os.path.join(tmpdir, "my_inc", "include", "tarantool"))
