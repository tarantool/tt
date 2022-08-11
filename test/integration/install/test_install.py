import re
import subprocess
import os


def test_install_tt(tt_cmd, tmpdir):

    configPath = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    # Install latest tt.
    install_cmd = [tt_cmd, "install", "tt=master", "--cfg", configPath]
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

    installed_cmd = [tmpdir + "/bin/tt", "-h"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmpdir + "/bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = installed_program_process.stdout.readline()
    assert re.search(r"Utility for managing Tarantool", start_output)


def test_install_tarantool(tt_cmd, tmpdir):

    configPath = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config.
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    # Install latest tarantool.
    install_cmd = [tt_cmd, "install", "tarantool", "-f", "--cfg", configPath]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmpdir,
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
        cwd=tmpdir + "/bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)
