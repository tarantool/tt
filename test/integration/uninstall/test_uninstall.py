import os
import re
import subprocess


def test_uninstall_tt(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config.
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    # Do not test uninstall through installing tt. Because installed tt will be invoked by current
    # tt. As a result the test will run for the installed tt and not the current. Creating fake
    # tarantool instead.
    os.mkdir(os.path.join(tmpdir, "bin"))
    with open(os.path.join(tmpdir, "bin", "tarantool_master"), 'w') as f:
        f.write('''#!/bin/sh
                echo "hello"''')
    os.chmod(os.path.join(tmpdir, "bin", "tarantool_master"), 0o775)
    os.symlink("./tarantool_master", os.path.join(tmpdir, "bin", "tarantool"))
    os.makedirs(os.path.join(tmpdir, "include", "include", "tarantool_master"))
    os.symlink("./tarantool_master", os.path.join(tmpdir, "include", "include", "tarantool"))

    uninstall_cmd = [tt_cmd,  "--cfg", configPath, "uninstall", "tarantool=master"]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    uninstall_process.wait()
    uninstall_output = uninstall_process.stdout.readlines()
    assert "Removing binary..." in uninstall_output[0]
    assert "Removing headers..." in uninstall_output[1]
    assert "tarantool=master is uninstalled" in uninstall_output[2]

    assert not os.path.exists(os.path.join(tmpdir, "bin", "tarantool_master"))
    assert not os.path.exists(os.path.join(tmpdir, "bin", "tarantool"))
    assert not os.path.exists(os.path.join(tmpdir, "include", "include", "tarantool_master"))


def test_uninstall_missing(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config.
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')
    # Create bin directory.
    os.mkdir(os.path.join(tmpdir, "bin"))
    os.mkdir(os.path.join(tmpdir, "include"))
    # Remove not installed program.
    uninstall_cmd = [tt_cmd, "uninstall", "tt=1.2.3"]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    uninstall_process.wait()
    uninstall_output = uninstall_process.stdout.readline()
    assert re.search(r"Removing binary...", uninstall_output)
    uninstall_output = uninstall_process.stdout.readline()
    assert re.search(r"there is no", uninstall_output)


def test_uninstall_foreign_program(tt_cmd, tmpdir):
    # Remove bash.
    uninstall_cmd = [tt_cmd, "uninstall", "bash=123"]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    uninstall_process.wait()
    uninstall_output = uninstall_process.stdout.readline()
    assert re.search(r"Removing binary...", uninstall_output)
    uninstall_output = uninstall_process.stdout.readline()
    assert re.search(r"unknown program:", uninstall_output)
