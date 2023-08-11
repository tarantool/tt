import os
import re
import shutil
import stat
import subprocess

import pytest

from utils import config_name, is_valid_tarantool_installed


def test_uninstall_tt(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, config_name)
    # Create test config.
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    for prog in [["tarantool"], ["tarantool", "master"]]:
        # Do not test uninstall through installing tt. Because installed tt will be invoked by
        # current tt. As a result the test will run for the installed tt and not the current.
        # Creating fake tarantool instead.
        os.mkdir(os.path.join(tmpdir, "bin"))
        with open(os.path.join(tmpdir, "bin", "tarantool_master"), 'w') as f:
            f.write('''#!/bin/sh
                    echo "hello"''')
        os.chmod(os.path.join(tmpdir, "bin", "tarantool_master"), 0o775)
        os.symlink("./tarantool_master", os.path.join(tmpdir, "bin", "tarantool"))
        os.makedirs(os.path.join(tmpdir, "include", "include", "tarantool_master"))
        os.symlink("./tarantool_master", os.path.join(tmpdir, "include", "include", "tarantool"))

        uninstall_cmd = [tt_cmd,  "--cfg", configPath, "uninstall", *prog]
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
        os.rmdir(os.path.join(tmpdir, "bin"))


def test_uninstall_default_many(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, config_name)
    # Create test config.
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    # Do not test uninstall through installing tt. Because installed tt will be invoked by
    # current tt. As a result the test will run for the installed tt and not the current.
    # Creating fake tarantool instead.
    os.mkdir(os.path.join(tmpdir, "bin"))
    with open(os.path.join(tmpdir, "bin", "tarantool_master"), 'w') as f:
        f.write('''#!/bin/sh
                echo "hello"''')
    with open(os.path.join(tmpdir, "bin", "tarantool_123"), 'w') as f:
        f.write('''#!/bin/sh
                echo "hello"''')
    os.chmod(os.path.join(tmpdir, "bin", "tarantool_master"), 0o775)
    os.symlink("./tarantool_master", os.path.join(tmpdir, "bin", "tarantool"))

    uninstall_cmd = [tt_cmd,  "--cfg", configPath, "uninstall", "tarantool"]
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
    expected = "tarantool has more than one installed version, " \
               + "please specify the version to uninstall"
    assert expected in uninstall_output[1]


def test_uninstall_missing(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, config_name)
    # Create test config.
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')
    # Create bin directory.
    os.mkdir(os.path.join(tmpdir, "bin"))
    os.mkdir(os.path.join(tmpdir, "include"))
    # Remove not installed program.
    uninstall_cmd = [tt_cmd, "uninstall", "tt", "1.2.3"]
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


def test_uninstall_foreign_program(tt_cmd, tmpdir_with_cfg):
    # Remove bash.
    for prog in [["bash"], ["bash", "123"]]:
        uninstall_cmd = [tt_cmd, "uninstall", *prog]
        uninstall_process = subprocess.Popen(
            uninstall_cmd,
            cwd=tmpdir_with_cfg,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        uninstall_process.wait()
        uninstall_output = uninstall_process.stdout.readline()
        assert re.search(r"Uninstalls a program", uninstall_output)


@pytest.mark.parametrize("is_symlink_broken", [False, True])
def test_uninstall_tarantool_dev_installed(tt_cmd, tmpdir, is_symlink_broken):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__),
                                 "testdata/tarantool_dev")
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    tt_dir = "installed"
    if is_symlink_broken:
        os.remove(os.path.join(testdata_path, tt_dir, "tarantool"))
        shutil.rmtree(os.path.join(testdata_path, tt_dir, "tarantool_inc"))

    uninstall_cmd = [
        tt_cmd,
        "--cfg", os.path.join(testdata_path, tt_dir, config_name),
        "uninstall", "tarantool-dev"
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    rc = uninstall_process.wait()
    assert rc == 0
    output = uninstall_process.stdout.read()
    # Check that the tarantool version has been switched correctly.
    assert 'Current "tarantool" is set to "tarantool_2.10.4"' in output
    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, tt_dir, "bin"),
        os.path.join(testdata_path, tt_dir, "inc", "include"),
        os.path.join(testdata_path, tt_dir, "bin", "tarantool_2.10.4"),
        os.path.join(testdata_path, tt_dir, "inc", "include",
                     "tarantool_2.10.4")
    )
    if not is_symlink_broken:
        assert os.path.exists(os.path.join(testdata_path, tt_dir, "tarantool"))
        assert os.path.exists(os.path.join(testdata_path, tt_dir, "tarantool_inc"))


def test_uninstall_tarantool_dev_not_installed(tt_cmd, tmpdir):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__),
                                 "testdata/tarantool_dev")
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    tt_dir = "not_installed"
    uninstall_cmd = [
        tt_cmd,
        "--cfg", os.path.join(testdata_path, tt_dir, config_name),
        "uninstall", "tarantool-dev"
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    rc = uninstall_process.wait()
    output = uninstall_process.stdout.read()
    assert rc == 1
    assert "tarantool-dev is not installed" in output
    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, tt_dir, "bin"),
        os.path.join(testdata_path, tt_dir, "inc", "include"),
        os.path.join(testdata_path, tt_dir, "bin", "tarantool_2.10.8"),
        os.path.join(testdata_path, tt_dir, "inc", "include",
                     "tarantool_2.10.8")
    )


def test_uninstall_tarantool_switch(tt_cmd, tmpdir):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__),
                                 "testdata/uninstall_switch")
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    uninstall_cmd = [
        tt_cmd,
        "--cfg", os.path.join(testdata_path, config_name),
        "uninstall", "tarantool", "1.10.15"
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    rc = uninstall_process.wait()
    assert rc == 0
    output = uninstall_process.stdout.read()
    assert 'Current "tarantool" is set to "tarantool_2.10.7-entrypoint"' in output
    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, "bin"),
        os.path.join(testdata_path, "inc", "include"),
        os.path.join(testdata_path, "bin", "tarantool_2.10.7-entrypoint"),
        os.path.join(testdata_path, "inc", "include",
                     "tarantool_2.10.7-entrypoint")
    )


def test_uninstall_tarantool_switch_hash(tt_cmd, tmpdir):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__),
                                 "testdata/uninstall_switch_hash")
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    uninstall_cmd = [
        tt_cmd,
        "--cfg", os.path.join(testdata_path, config_name),
        "uninstall", "tarantool", "1.10.15"
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    rc = uninstall_process.wait()
    assert rc == 0
    output = uninstall_process.stdout.read()
    assert 'Current "tarantool" is set to "tarantool_aaaaaaa"' in output
    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, "bin"),
        os.path.join(testdata_path, "inc", "include"),
        os.path.join(testdata_path, "bin", "tarantool_aaaaaaa"),
        os.path.join(testdata_path, "inc", "include",
                     "tarantool_aaaaaaa")
    )


# No symlink changes should be made if the symlink was pointing
# to some other version.
def test_uninstall_tarantool_no_switch(tt_cmd, tmpdir):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__),
                                 "testdata/uninstall_switch")
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    binary_path = os.path.join(testdata_path, "bin", "tarantool")
    include_path = os.path.join(testdata_path, "inc", "include", "tarantool")

    # Change symlinks to the version, which will not be uninstalled.
    os.unlink(binary_path)
    os.unlink(include_path)
    os.symlink(
        os.path.join(testdata_path, "bin", "tarantool_2.10.4"),
        binary_path
    )
    os.chmod(binary_path, stat.S_IEXEC)
    os.symlink(
        os.path.join(testdata_path, "inc", "include", "tarantool_2.10.4"),
        include_path
    )

    uninstall_cmd = [
        tt_cmd,
        "--cfg", os.path.join(testdata_path, config_name),
        "uninstall", "tarantool", "1.10.15"
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.DEVNULL,
    )
    rc = uninstall_process.wait()
    assert rc == 0
    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, "bin"),
        os.path.join(testdata_path, "inc", "include"),
        os.path.join(testdata_path, "bin", "tarantool_2.10.4"),
        os.path.join(testdata_path, "inc", "include", "tarantool_2.10.4")
    )
