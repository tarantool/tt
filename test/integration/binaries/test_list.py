import os
import shutil
import subprocess

from utils import config_name, run_command_and_get_output


def test_list(tt_cmd, tmpdir):
    # Copy the test bin_dir to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "bin")
    shutil.copytree(test_app_path, tmpdir + "/bin", True)

    configPath = os.path.join(tmpdir, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('tt:\n  env:\n    bin_dir: "./bin"\n    inc_dir:\n')

    # Print binaries
    binaries_cmd = [tt_cmd, "--cfg", configPath, "binaries", "list"]
    binaries_process = subprocess.Popen(
        binaries_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    binaries_process.wait()
    output = binaries_process.stdout.read()

    assert "tt" in output
    assert "0.1.0" in output
    assert "tarantool" in output
    assert "2.10.3" in output
    assert "2.8.1 [active]" in output


def test_list_no_directory(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('tt:\n  env:\n    bin_dir: "./bin"\n    inc_dir:\n')

    # Print binaries
    binaries_cmd = [tt_cmd, "--cfg", configPath, "binaries", "list"]
    binaries_process = subprocess.Popen(
        binaries_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    binaries_process.wait()
    output = binaries_process.stdout.read()

    assert "there are no binaries installed in this environment of 'tt'" in output


def test_list_empty_directory(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, config_name)
    os.mkdir(tmpdir+"/bin")
    # Create test config
    with open(configPath, 'w') as f:
        f.write('tt:\n  env:\n    bin_dir: "./bin"\n    inc_dir:\n')

    # Print binaries
    binaries_cmd = [tt_cmd, "--cfg", configPath, "binaries", "list"]
    binaries_process = subprocess.Popen(
        binaries_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    binaries_process.wait()
    output = binaries_process.stdout.read()

    assert "there are no binaries installed in this environment of 'tt'" in output


def test_list_tarantool_dev(tt_cmd, tmpdir):
    # Copy the test dir to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "tarantool_dev")
    shutil.copytree(test_app_path, tmpdir + "/tarantool_dev", True)

    config_path = os.path.join(tmpdir, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        f.write(
            'env:\n  bin_dir: "./tarantool_dev/bin"\n  inc_dir:\n')

    binaries_cmd = [tt_cmd, "--cfg", config_path, "binaries", "list"]
    binaries_process = subprocess.Popen(
        binaries_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    binaries_process.wait()
    output = binaries_process.stdout.read()

    assert "2.10.7" in output
    assert "1.10.0" in output
    assert "tarantool-dev" in output


def test_list_tarantool_no_symlink(tt_cmd, tmpdir):
    # Copy the test bin_dir to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "bin")
    shutil.copytree(test_app_path, tmpdir + "/bin", True)

    configPath = os.path.join(tmpdir, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('tt:\n  env:\n    bin_dir: "./bin"\n    inc_dir:\n')

    os.remove(os.path.join(tmpdir, "bin", "tarantool"))
    with open(os.path.join(tmpdir, "bin", "tarantool"), "w") as tnt_file:
        tnt_file.write("""#!/bin/sh
echo 'Tarantool 3.1.0-entrypoint-83-gcb0264c3c'""")
    os.chmod(os.path.join(tmpdir, "bin", "tarantool"), 0o750)

    # Print binaries
    binaries_cmd = [tt_cmd, "--cfg", configPath, "binaries", "list"]
    rc, output = run_command_and_get_output(binaries_cmd, cwd=tmpdir)

    assert rc == 0
    assert "tt" in output
    assert "0.1.0" in output
    assert "tarantool:" in output
    assert "2.10.3" in output
    assert "2.8.1" in output
    assert "3.1.0-entrypoint-83-gcb0264c3c [active]" in output

    # Remove non-versioned tarantool binary.
    os.remove(os.path.join(tmpdir, "bin", "tarantool"))
    binaries_cmd = [tt_cmd, "--cfg", configPath, "binaries", "list"]
    rc, output = run_command_and_get_output(binaries_cmd, cwd=tmpdir)

    assert rc == 0
    assert "tt" in output
    assert "0.1.0" in output
    assert "tarantool:" in output
    assert "2.10.3" in output
    assert "2.8.1" in output
    assert "[active]" not in output  # No active tarantool.
