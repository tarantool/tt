import os
import platform
import re
import shutil
import subprocess
import tempfile

import pytest
import yaml

from utils import (config_name, is_valid_tarantool_installed,
                   run_command_and_get_output)


@pytest.mark.slow
def test_install_tt_unexisted_commit(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)

    # Create test config
    tmp_dir = tempfile.mkdtemp(dir=tmp_path)
    tmp_name = tmp_dir.rpartition('/')[2]
    with open(configPath, 'w') as f:
        f.write('env:\n  bin_dir:\n  inc_dir:\nrepo:\n  distfiles: "%s"' % tmp_name)

    os.makedirs(tmp_dir + "/tt")

    install_cmd = ["git", "init"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_dir + "/tt",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Install tt.
    install_cmd = [tt_cmd, "--cfg", configPath, "install", "--local-repo", "tt", "2df3077"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc != 0

    first_output = instance_process.stdout.readline()
    assert re.search(r"Searching in commits", first_output)
    second_output = instance_process.stdout.readline()
    assert re.search(r"2df3077: unable to get hash info", second_output)


@pytest.mark.slow
def test_install_tt(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('env:\n  bin_dir:\n  inc_dir:\n')

    # Install latest tt.
    install_cmd = [tt_cmd, "--cfg", configPath, "install", "tt"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    os.remove(configPath)

    installed_cmd = [tmp_path / "bin" / "tt", "version"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmp_path / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool CLI version \d+.\d+.\d+", start_output)


@pytest.mark.slow
def test_install_uninstall_tt_specific_commit(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('env:\n  bin_dir:\n  inc_dir:\n')

    # Install specific tt's commit.
    install_cmd = [tt_cmd, "--cfg", configPath, "install", "tt", "97c7b73"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0

    installed_cmd = [tmp_path / "bin" / "tt", "version"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmp_path / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool CLI version 2.1.1", start_output)
    assert re.search(r"commit: 97c7b73", start_output)

    # Uninstall specific tt's commit.
    uninstall_cmd = [tt_cmd, "--cfg", configPath, "uninstall", "tt", "97c7b73"]
    uninstall_instance_process = subprocess.Popen(
        uninstall_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    first_output = uninstall_instance_process.stdout.readline()
    assert re.search(r"Removing binary...", first_output)
    second_output = uninstall_instance_process.stdout.readline()
    assert re.search(r"tt=97c7b73 is uninstalled", second_output)
    assert not os.path.exists(os.path.join(tmp_path, "bin", "tt_97c7b73"))


@pytest.mark.slow
def test_wrong_format_hash(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('env:\n  bin_dir:\n  inc_dir:\n')

    # Install specific tt's commit.
    install_cmd = [tt_cmd, "--cfg", configPath, "install", "tt", "111"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc != 0
    first_output = instance_process.stdout.readline()
    assert re.search(r"Searching in commits...", first_output)
    second_output = instance_process.stdout.readline()
    assert re.search(r"the hash must contain at least 7 characters", second_output)

    # Install specific tt's commit.
    install_cmd_second = [tt_cmd, "--cfg", configPath, "install", "tt", "zzzzzzz"]
    instance_process_second = subprocess.Popen(
        install_cmd_second,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process_second.wait()
    assert instance_process_rc != 0
    first_output = instance_process_second.stdout.readline()
    assert re.search(r"Searching in commits...", first_output)
    second_output = instance_process_second.stdout.readline()
    assert re.search(r"hash has a wrong format", second_output)


@pytest.mark.slow
def test_install_tt_specific_version(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('env:\n  bin_dir:\n  inc_dir:\n')

    # Install latest tt.
    install_cmd = [tt_cmd, "--cfg", configPath, "install", "tt", "2.1.2"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    os.remove(configPath)

    installed_cmd = [tmp_path / "bin" / "tt", "version"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmp_path / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool CLI version 2.1.2", start_output)


@pytest.mark.slow
def test_install_tarantool_commit(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "", "inc_dir": "./my_inc"}}, f)

    tmp_path_without_config = tempfile.mkdtemp()

    # Install specific tarantool's commit.
    install_cmd = [tt_cmd, "--cfg", config_path, "install", "-f", "tarantool", "00a9e59"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path_without_config,
        stderr=subprocess.STDOUT,
        # Do not use pipe for stdout, if you are not going to read from it.
        # In case of build failure, logs are printed to stdout. It fills pipe buffer and
        # blocks all subsequent stdout write calls in tt, because there is no pipe reader in test.
        stdout=subprocess.DEVNULL,
        text=True
    )

    # Check that the process was shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    installed_cmd = [tmp_path / "bin" / "tarantool", "-v"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=os.path.join(tmp_path, "/bin"),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)
    assert os.path.exists(os.path.join(tmp_path, "my_inc", "include", "tarantool"))
    assert os.path.exists(os.path.join(tmp_path, "bin", "tarantool_00a9e59"))

    assert is_valid_tarantool_installed(
        os.path.join(tmp_path, "bin"),
        os.path.join(tmp_path, "my_inc", "include"),
        os.path.join(tmp_path, "bin", "tarantool_00a9e59"),
        os.path.join(tmp_path, "my_inc", "include", "tarantool_00a9e59"),
    )


@pytest.mark.slow
def test_install_tarantool(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "", "inc_dir": "./my_inc"}}, f)

    tmp_path_without_config = tempfile.mkdtemp()

    # Install latest tarantool.
    install_cmd = [tt_cmd, "--cfg", config_path, "install", "-f", "tarantool", "2.10.7"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path_without_config,
        stderr=subprocess.STDOUT,
        # Do not use pipe for stdout, if you are not going to read from it.
        # In case of build failure, logs are printed to stdout. It fills pipe buffer and
        # blocks all subsequent stdout write calls in tt, because there is no pipe reader in test.
        stdout=subprocess.DEVNULL,
        text=True
    )

    # Check that the process was shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    installed_cmd = [tmp_path / "bin" / "tarantool", "-v"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=os.path.join(tmp_path, "/bin"),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)
    assert os.path.exists(os.path.join(tmp_path, "my_inc", "include", "tarantool"))
    assert os.path.exists(os.path.join(tmp_path, "bin", "tarantool_2.10.7"))


@pytest.mark.slow
@pytest.mark.docker
def test_install_tarantool_in_docker(tt_cmd, tmp_path):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    config_path = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "", "inc_dir": "./my_inc"}}, f)

    tmp_path_without_config = tempfile.mkdtemp()

    # Install latest tarantool.
    install_cmd = [tt_cmd, "--cfg", config_path, "install", "-f", "tarantool", "--use-docker"]
    tt_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path_without_config,
        stderr=subprocess.STDOUT,
        # Do not use pipe for stdout, if you are not going to read from it.
        # In case of build failure, docker logs are printed to stdout. It fills pipe buffer and
        # blocks all subsequent stdout write calls in tt, because there is no pipe reader in test.
        stdout=subprocess.DEVNULL,
        text=True
    )

    instance_process_rc = tt_process.wait()
    assert instance_process_rc == 0
    installed_cmd = [tmp_path / "bin" / "tarantool", "-v"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=os.path.join(tmp_path, "/bin"),
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)

    # Check tarantool glibc version.
    out = subprocess.getoutput("objdump -T " + os.path.join(tmp_path, "bin", "tarantool") +
                               " | grep -o -E 'GLIBC_[.0-9]+' | sort -V | tail -n1")
    assert out == "GLIBC_2.27"

    assert os.path.exists(os.path.join(tmp_path, "my_inc", "include", "tarantool"))


@pytest.mark.parametrize("tt_dir, expected_bin_path, expected_inc_path", [
    pytest.param(
        "tt_basic",
        os.path.join("bin", "tarantool_2.10.8"),
        os.path.join("inc", "include", "tarantool_2.10.8")
    ),
    pytest.param(
        "tt_empty",
        None,
        None,
    )
])
def test_install_tarantool_dev_bin_invalid(
        tt_cmd,
        tmp_path,
        tt_dir,
        expected_bin_path,
        expected_inc_path):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata")
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    for build_dir in ["build_invalid", "build_invalid2"]:
        build_path = os.path.join(testdata_path, build_dir)
        install_cmd = [
            tt_cmd,
            "--cfg", os.path.join(testdata_path, tt_dir, config_name),
            "install", "tarantool-dev",
            build_path
        ]
        install_process = subprocess.Popen(
            install_cmd,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        install_process_rc = install_process.wait()
        output = install_process.stdout.read()
        assert "tarantool binary was not found" in output
        assert install_process_rc != 0

        if expected_bin_path is not None:
            expected_bin_path = os.path.join(testdata_path, tt_dir, expected_bin_path)
        if expected_inc_path is not None:
            expected_inc_path = os.path.join(testdata_path, tt_dir, expected_inc_path)

        assert is_valid_tarantool_installed(
            os.path.join(testdata_path, tt_dir, "bin"),
            os.path.join(testdata_path, tt_dir, "inc", "include"),
            expected_bin_path,
            expected_inc_path
        )


@pytest.mark.parametrize("tt_dir", [
    "tt_basic",
    "tt_empty",
    "tt_invalid"
])
@pytest.mark.parametrize("build_dir, exec_rel_path, include_rel_path", [
    pytest.param(
        "build_ce",
        os.path.join("src", "tarantool"),
        os.path.join("tarantool-prefix", "include", "tarantool")
    ),
    pytest.param(
        "build_ee",
        os.path.join("tarantool", "src", "tarantool"),
        None
    ),
    pytest.param(
        "build_static",
        os.path.join("tarantool-prefix", "bin", "tarantool"),
        os.path.join("tarantool-prefix", "include", "tarantool")
    )
])
def test_install_tarantool_dev_no_include_option(
        tt_cmd,
        tmp_path,
        build_dir,
        exec_rel_path,
        include_rel_path,
        tt_dir
):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata")
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    build_path = os.path.join(testdata_path, build_dir)
    install_cmd = [
        tt_cmd,
        "--cfg", os.path.join(testdata_path, tt_dir, config_name),
        "install", "tarantool-dev",
        build_path
    ]
    install_process = subprocess.Popen(
        install_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.DEVNULL,
    )

    install_process_rc = install_process.wait()
    assert install_process_rc == 0

    expected_include_symlink = None
    if include_rel_path is not None:
        expected_include_symlink = os.path.join(
            testdata_path, build_dir, include_rel_path
        )

    assert is_valid_tarantool_installed(
        os.path.join(testdata_path, tt_dir, "bin"),
        os.path.join(testdata_path, tt_dir, "inc", "include"),
        os.path.join(testdata_path, build_dir, exec_rel_path),
        expected_include_symlink,
    )


@pytest.mark.parametrize("tt_dir", [
     "tt_basic",
     "tt_empty",
     "tt_invalid"
])
@pytest.mark.parametrize("rc, include_dir", [
    pytest.param(0, "custom_include/tarantool", id='dir exists'),
    pytest.param(1, "include/tarantool", id='dir not exists')
])
def test_install_tarantool_dev_include_option(
        tt_cmd, tmp_path, rc, include_dir, tt_dir
):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata")
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    build_dir = "build_ee"
    build_path = os.path.join(testdata_path, build_dir)
    install_cmd = [
        tt_cmd,
        "--cfg", os.path.join(testdata_path, tt_dir, config_name),
        "install", "tarantool-dev",
        build_path,
        "--include-dir", os.path.join(build_path, include_dir)
    ]

    install_process = subprocess.Popen(
        install_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.DEVNULL
    )
    install_process_rc = install_process.wait()
    assert install_process_rc == rc

    if rc == 0:
        assert is_valid_tarantool_installed(
            os.path.join(testdata_path, tt_dir, "bin"),
            os.path.join(testdata_path, tt_dir, "inc", "include"),
            os.path.join(build_path, "tarantool/src/tarantool"),
            os.path.join(build_path, include_dir),
        )


def test_install_tarantool_already_exists(tt_cmd, tmp_path):
    # Copy test files.
    testdata_path = os.path.join(
        os.path.dirname(__file__),
        "testdata/test_install_tarantool_already_exists"
    )
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    tt_dir = os.path.join(testdata_path, "tt")

    install_cmd = [
        tt_cmd,
        "--cfg", os.path.join(tt_dir, config_name),
        "install", "tarantool", "1.10.13"
    ]

    install_process = subprocess.Popen(
        install_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    install_process_rc = install_process.wait()
    output = install_process.stdout.read()
    assert "version of tarantool already exists" in output
    assert install_process_rc == 0

    assert is_valid_tarantool_installed(
        os.path.join(tt_dir, "bin"),
        os.path.join(tt_dir, "inc", "include"),
        os.path.join(tt_dir, "bin", "tarantool_1.10.13"),
        os.path.join(tt_dir, "inc", "include", "tarantool_1.10.13"),
    )


def test_install_tt_already_exists_no_symlink(tt_cmd, tmp_path):
    # Copy test files.
    testdata_path = os.path.join(
        os.path.dirname(__file__),
        "testdata/test_install_tt_already_exists"
    )
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    tt_dir = os.path.join(testdata_path, "tt")

    install_cmd = [
        tt_cmd,
        "--cfg", os.path.join(tt_dir, config_name),
        "install", "tt", "1.1.2"
    ]

    install_process = subprocess.Popen(
        install_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    install_process_rc = install_process.wait()
    output = install_process.stdout.read()
    assert "already exists" in output
    assert install_process_rc == 0

    bin_path = os.path.join(tt_dir, "bin")
    expected_bin = os.path.join(bin_path, "tt_v1.1.2")
    tarantool_bin = os.path.realpath(os.path.join(bin_path, "tt"))
    assert tarantool_bin == expected_bin


def test_install_tt_already_exists_with_symlink(tt_cmd, tmp_path):
    # Copy test files.
    testdata_path = os.path.join(
        os.path.dirname(__file__),
        "testdata/test_install_tt_already_exists"
    )
    shutil.copytree(testdata_path, os.path.join(tmp_path, "testdata"), True)
    testdata_path = os.path.join(tmp_path, "testdata")

    tt_dir = os.path.join(testdata_path, "tt")
    os.symlink(tt_cmd, os.path.join(tt_dir, "bin", "tt"))
    install_cmd = [
        tt_cmd,
        "--cfg", os.path.join(tt_dir, config_name),
        "install", "tt", "1.1.2"
    ]

    install_process = subprocess.Popen(
        install_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    install_process_rc = install_process.wait()
    output = install_process.stdout.read()
    assert "already exists" in output
    assert install_process_rc == 0

    bin_path = os.path.join(tt_dir, "bin")
    expected_bin = os.path.join(bin_path, "tt_v1.1.2")
    tarantool_bin = os.path.realpath(os.path.join(bin_path, "tt"))
    assert tarantool_bin == expected_bin


@pytest.mark.parametrize("package_to_exclude", [
    pytest.param("mage"),
    pytest.param("git")
])
def test_install_tt_fail_exit_code_dependency_check(
        tt_cmd,
        tmp_path,
        package_to_exclude):
    # Create new PATH with excluded packages.
    original_path = os.environ['PATH']
    new_path = os.pathsep.join(filter(lambda dir: os.path.isdir(dir)
                               and package_to_exclude not in os.listdir(dir),
                               original_path.split(os.pathsep)))

    # Create test config.
    configPath = os.path.join(tmp_path, config_name)
    with open(configPath, 'w') as f:
        f.write('env:\n  bin_dir:\n  inc_dir:\n')
    install_cmd = [tt_cmd, "--cfg", configPath, "install", "tt"]
    install_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        env={'PATH': new_path},
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    install_process_rc = install_process.wait()
    assert install_process_rc == 1


@pytest.mark.slow
@pytest.mark.parametrize("old_tarantool_version, expected_install_msg, is_interactive", [
    pytest.param("7192bf6", "Found newest commit of tarantool in master", True),
    pytest.param("master", "tarantool_master version of tarantool already exists", False),
    pytest.param("7192bf6", "tarantool_master version of tarantool already exists", False)
])
def test_install_tarantool_fetch_latest_version(
        tt_cmd,
        tmp_path,
        old_tarantool_version,
        expected_install_msg,
        is_interactive):
    config_path = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "", "inc_dir": "./my_inc"}}, f)

    tmp_path_without_config = tempfile.mkdtemp()

    # Install not latest version of tarantool by commit hash.
    install_cmd = [tt_cmd, "--cfg", config_path, "install",
                   "-f", "tarantool", old_tarantool_version, "--dynamic"]
    instance_process_rc, _ = run_command_and_get_output(install_cmd,
                                                        cwd=tmp_path_without_config,
                                                        stdout=subprocess.DEVNULL)
    assert instance_process_rc == 0

    # Renaming installed 'old' tarantool to 'master'
    # and change symlink to old version to simulate
    # that this is the latest version.
    os.rename(os.path.join(tmp_path, "bin", "tarantool_" +
              old_tarantool_version), os.path.join(tmp_path, "bin", "tarantool_master"))
    os.remove(os.path.join(tmp_path, "bin", "tarantool"))
    os.symlink(os.path.join(tmp_path, "bin", "tarantool_master"),
               os.path.join(tmp_path, "bin", "tarantool"))
    os.remove(os.path.join(tmp_path, "my_inc", "include", "tarantool"))
    os.rename(os.path.join(tmp_path, "my_inc", "include",
              "tarantool_" + old_tarantool_version),
              os.path.join(tmp_path, "my_inc", "include", "tarantool_master"))

    # Installing newest version.
    install_cmd = [tt_cmd, "--cfg", config_path]
    if not is_interactive:
        install_cmd.append("--no-prompt")
    install_cmd.extend(["install", "-f", "tarantool", "master", "--dynamic"])

    instance_process_rc, output = run_command_and_get_output(install_cmd,
                                                             cwd=tmp_path_without_config,
                                                             input="y\n" if is_interactive
                                                             is True else None)
    assert instance_process_rc == 0
    assert expected_install_msg in output


@pytest.mark.slow
@pytest.mark.parametrize("active_symlink, expected_install_msg, is_interactive", [
    ("tt_master", "Found newest commit of tt in master", True),
    ("tt_master", "tt_master version of tt already exists, updating symlink...", False),
    ("tt_v1.2.3", "Found newest commit of tt in master", True),
])
def test_install_tt_fetch_latest_version(active_symlink,
                                         expected_install_msg,
                                         is_interactive,
                                         tt_cmd,
                                         tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    # Create test config
    with open(config_path, 'w') as f:
        f.write("env:\n  bin_dir: \n  inc_dir:\n")

    # Create executalbe file with 'tt version --commit'
    # functionality to emulate real work of tt. We need
    # the following to avoid a situation where the last
    # git commit in the master branch makes it impossible
    # to detect the latest version which contradicts the test logic.
    os.mkdir(os.path.join(tmp_path, "bin"))
    with open(os.path.join(tmp_path, "bin", "tt_master"), 'a') as f:
        f.write('''#!/bin/sh
                echo "2.0.0.677234a"''')
    os.chmod(os.path.join(tmp_path, "bin", "tt_master"), 0o775)
    with open(os.path.join(tmp_path, "bin", "tt_v1.2.3"), 'a') as f:
        f.write('''#!/bin/sh
                echo "1.2.3.087ac72"''')
    os.chmod(os.path.join(tmp_path, "bin", "tt_v1.2.3"), 0o775)
    os.symlink(os.path.join(tmp_path, active_symlink),
               os.path.join(tmp_path, "tt"))

    install_cmd = [tt_cmd, "--cfg", config_path]
    if not is_interactive:
        install_cmd.append("--no-prompt")
    install_cmd.extend(["install", "tt", "master"])
    instance_process_rc, install_output = run_command_and_get_output(
        install_cmd,
        cwd=tmp_path,
        input="y\n" if is_interactive
        is True else None)
    assert instance_process_rc == 0
    assert expected_install_msg in install_output
