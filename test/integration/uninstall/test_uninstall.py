import os
import re
import shutil
import stat
import subprocess

import pytest

from utils import config_name, is_valid_tarantool_installed, run_command_and_get_output


def test_uninstall_tt(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(configPath, "w") as f:
        f.write("tt:\n  env:\n    bin_dir:\n    inc_dir:\n")

    for prog in [["tarantool"], ["tarantool", "master"]]:
        # Do not test uninstall through installing tt. Because installed tt will be invoked by
        # current tt. As a result the test will run for the installed tt and not the current.
        # Creating fake tarantool instead.
        os.mkdir(os.path.join(tmp_path, "bin"))
        with open(os.path.join(tmp_path, "bin", "tarantool_master"), "w") as f:
            f.write('''#!/bin/sh
                    echo "hello"''')
        os.chmod(os.path.join(tmp_path, "bin", "tarantool_master"), 0o775)
        os.symlink("./tarantool_master", os.path.join(tmp_path, "bin", "tarantool"))
        os.makedirs(os.path.join(tmp_path, "include", "include", "tarantool_master"))
        os.symlink("./tarantool_master", os.path.join(tmp_path, "include", "include", "tarantool"))

        uninstall_cmd = [tt_cmd, "--cfg", configPath, "uninstall", *prog]
        uninstall_process = subprocess.Popen(
            uninstall_cmd,
            cwd=tmp_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        uninstall_process.wait()
        uninstall_output = uninstall_process.stdout.readlines()
        assert "Removing binary..." in uninstall_output[0]
        assert "Removing headers..." in uninstall_output[1]
        assert "tarantool=master is uninstalled" in uninstall_output[2]

        assert not os.path.exists(os.path.join(tmp_path, "bin", "tarantool_master"))
        assert not os.path.exists(os.path.join(tmp_path, "bin", "tarantool"))
        assert not os.path.exists(os.path.join(tmp_path, "include", "include", "tarantool_master"))
        os.rmdir(os.path.join(tmp_path, "bin"))


def test_uninstall_default_many(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(configPath, "w") as f:
        f.write("tt:\n  env:\n    bin_dir:\n    inc_dir:\n")

    # Do not test uninstall through installing tt. Because installed tt will be invoked by
    # current tt. As a result the test will run for the installed tt and not the current.
    # Creating fake tarantool instead.
    os.mkdir(os.path.join(tmp_path, "bin"))
    with open(os.path.join(tmp_path, "bin", "tarantool_master"), "w") as f:
        f.write('''#!/bin/sh
                echo "hello"''')
    with open(os.path.join(tmp_path, "bin", "tarantool_123"), "w") as f:
        f.write('''#!/bin/sh
                echo "hello"''')
    os.chmod(os.path.join(tmp_path, "bin", "tarantool_master"), 0o775)
    os.symlink("./tarantool_master", os.path.join(tmp_path, "bin", "tarantool"))

    uninstall_cmd = [tt_cmd, "--cfg", configPath, "uninstall", "tarantool"]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    uninstall_process.wait()
    uninstall_output = uninstall_process.stdout.readlines()
    assert "Removing binary..." in uninstall_output[0]
    expected = (
        "tarantool has more than one installed version, "
        + "please specify the version to uninstall"
    )
    assert expected in uninstall_output[1]


def test_uninstall_missing(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(configPath, "w") as f:
        f.write("tt:\n  env:\n    bin_dir:\n    inc_dir:\n")
    # Create bin directory.
    os.mkdir(os.path.join(tmp_path, "bin"))
    os.mkdir(os.path.join(tmp_path, "include"))
    # Remove not installed program.
    uninstall_cmd = [tt_cmd, "uninstall", "tt", "1.2.3"]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    uninstall_process.wait()
    uninstall_output = uninstall_process.stdout.readline()
    assert re.search(r"Removing binary...", uninstall_output)
    uninstall_output = uninstall_process.stdout.readline()
    assert "program is not installed" in uninstall_output


def test_uninstall_foreign_program(tt_cmd, tmpdir_with_cfg):
    # Remove bash.
    for prog in [["bash"], ["bash", "123"]]:
        uninstall_cmd = [tt_cmd, "uninstall", *prog]
        uninstall_process = subprocess.Popen(
            uninstall_cmd,
            cwd=tmpdir_with_cfg,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        uninstall_process.wait()
        uninstall_output = uninstall_process.stdout.readline()
        assert re.search(r"Uninstalls a program", uninstall_output)


@pytest.mark.parametrize("is_symlink_broken", [False, True])
def test_uninstall_tarantool_dev_installed(tt_cmd, tmp_path, is_symlink_broken):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata/tarantool_dev")
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    tt_dir = "installed"
    if is_symlink_broken:
        os.remove(os.path.join(testdata_path, tt_dir, "tarantool"))
        shutil.rmtree(os.path.join(testdata_path, tt_dir, "tarantool_inc"))

    uninstall_cmd = [
        tt_cmd,
        "--cfg",
        os.path.join(testdata_path, tt_dir, config_name),
        "uninstall",
        "tarantool-dev",
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
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
        os.path.join(testdata_path, tt_dir, "inc", "include", "tarantool_2.10.4"),
    )
    if not is_symlink_broken:
        assert os.path.exists(os.path.join(testdata_path, tt_dir, "tarantool"))
        assert os.path.exists(os.path.join(testdata_path, tt_dir, "tarantool_inc"))


def test_uninstall_tarantool_dev_not_installed(tt_cmd, tmp_path):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata/tarantool_dev")
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    tt_dir = "not_installed"
    uninstall_cmd = [
        tt_cmd,
        "--cfg",
        os.path.join(testdata_path, tt_dir, config_name),
        "uninstall",
        "tarantool-dev",
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    rc = uninstall_process.wait()
    output = uninstall_process.stdout.read()
    assert rc == 1
    assert "tarantool-dev is not installed" in output
    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, tt_dir, "bin"),
        os.path.join(testdata_path, tt_dir, "inc", "include"),
        os.path.join(testdata_path, tt_dir, "bin", "tarantool_2.10.8"),
        os.path.join(testdata_path, tt_dir, "inc", "include", "tarantool_2.10.8"),
    )


def test_uninstall_tarantool_switch(tt_cmd, tmp_path):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata/uninstall_switch")
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    uninstall_cmd = [
        tt_cmd,
        "--cfg",
        os.path.join(testdata_path, config_name),
        "uninstall",
        "tarantool",
        "1.10.15",
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    rc = uninstall_process.wait()
    assert rc == 0
    output = uninstall_process.stdout.read()
    assert 'Current "tarantool" is set to "tarantool_2.10.7-entrypoint"' in output
    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, "bin"),
        os.path.join(testdata_path, "inc", "include"),
        os.path.join(testdata_path, "bin", "tarantool_2.10.7-entrypoint"),
        os.path.join(testdata_path, "inc", "include", "tarantool_2.10.7-entrypoint"),
    )


def test_uninstall_tarantool_switch_hash(tt_cmd, tmp_path):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata/uninstall_switch_hash")
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    uninstall_cmd = [
        tt_cmd,
        "--cfg",
        os.path.join(testdata_path, config_name),
        "uninstall",
        "tarantool",
        "1.10.15",
    ]
    uninstall_process = subprocess.Popen(
        uninstall_cmd,
        cwd=testdata_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    rc = uninstall_process.wait()
    assert rc == 0
    output = uninstall_process.stdout.read()
    assert 'Current "tarantool" is set to "tarantool_aaaaaaa"' in output
    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, "bin"),
        os.path.join(testdata_path, "inc", "include"),
        os.path.join(testdata_path, "bin", "tarantool_aaaaaaa"),
        os.path.join(testdata_path, "inc", "include", "tarantool_aaaaaaa"),
    )


# No symlink changes should be made if the symlink was pointing
# to some other version.
def test_uninstall_tarantool_no_switch(tt_cmd, tmp_path):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata/uninstall_switch")
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    binary_path = os.path.join(testdata_path, "bin", "tarantool")
    include_path = os.path.join(testdata_path, "inc", "include", "tarantool")

    # Change symlinks to the version, which will not be uninstalled.
    os.unlink(binary_path)
    os.unlink(include_path)
    os.symlink(os.path.join(testdata_path, "bin", "tarantool_2.10.4"), binary_path)
    os.chmod(binary_path, stat.S_IEXEC)
    os.symlink(os.path.join(testdata_path, "inc", "include", "tarantool_2.10.4"), include_path)

    uninstall_cmd = [
        tt_cmd,
        "--cfg",
        os.path.join(testdata_path, config_name),
        "uninstall",
        "tarantool",
        "1.10.15",
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
        os.path.join(testdata_path, "inc", "include", "tarantool_2.10.4"),
    )


@pytest.mark.parametrize(
    "installed_versions, version_to_uninstall",
    [(["v1.2.3"], "1.2.3"), (["v1.2.3", "1.2.3"], "1.2.3"), (["v1.2.3", "1.2.3"], "v1.2.3")],
)
def test_uninstall_tt_missing_version_character(
    installed_versions,
    version_to_uninstall,
    tt_cmd,
    tmp_path,
):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(configPath, "w") as f:
        f.write(f"env:\n bin_dir: {tmp_path}\n inc_dir:\n")

    # Copying built executable to bin directory,
    # renaming it to version and change symlink
    # to emulate that we have installed it like
    # tt install tt v1.2.3.
    # We don't really install it because bug we test
    # is fixed only for current patch. Due to
    # binary file is changing while running this
    # bug won't appear only for current and future versions.
    for version in installed_versions:
        shutil.copy(tt_cmd, os.path.join(tmp_path, f"tt_{version}"))
    os.symlink(os.path.join(tmp_path, f"tt_{installed_versions[0]}"), os.path.join(tmp_path, "tt"))

    # Remove not installed program.
    uninstall_cmd = [tt_cmd, "--cfg", configPath, "uninstall", "tt", version_to_uninstall]
    uninstall_rc, uninstall_output = run_command_and_get_output(uninstall_cmd, cwd=tmp_path)

    assert uninstall_rc == 0
    assert f"tt={version_to_uninstall} is uninstalled." in uninstall_output

    # Check that the passed version has been removed.
    assert (
        os.path.isfile(
            os.path.join(
                tmp_path,
                "tt_" + "v" if "v" not in version_to_uninstall else "" + version_to_uninstall,
            ),
        )
        is False
    )


def test_uninstall_tt_missing_symlink(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(configPath, "w") as f:
        f.write(f"env:\n bin_dir: {tmp_path}\n inc_dir:\n")

    shutil.copy(tt_cmd, os.path.join(tmp_path, "tt_94ba971"))

    symlink_path = os.path.join(tmp_path, "tt")
    assert not os.path.exists(symlink_path)

    uninstall_cmd = [tt_cmd, "uninstall", "tt", "94ba971"]
    uninstall_rc, uninstall_output = run_command_and_get_output(uninstall_cmd, cwd=tmp_path)

    assert uninstall_rc == 0
    assert "tt=94ba971 is uninstalled." in uninstall_output

    assert os.path.exists(os.path.join(tmp_path, "tt_" + "94ba971") is False)
