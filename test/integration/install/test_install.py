import os
import platform
import re
import shutil
import subprocess
import tempfile

import pytest
import yaml

from utils import (
    config_name,
    get_tarantool_commit,
    is_tarantool_ee,
    is_valid_tarantool_installed,
    run_command_and_get_output,
)


@pytest.mark.slow
def test_install_tt_noexist_commit(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)

    # Create test config
    tmp_dir = tempfile.mkdtemp(dir=tmp_path)
    tmp_name = tmp_dir.rpartition("/")[2]
    with open(configPath, "w") as f:
        f.write('env:\n  bin_dir:\n  inc_dir:\nrepo:\n  distfiles: "%s"' % tmp_name)

    os.makedirs(tmp_dir + "/tt")

    install_cmd = ["git", "init"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_dir + "/tt",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Install tt.
    install_cmd = [
        tt_cmd,
        "--cfg",
        configPath,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "--local-repo",
        tmp_dir,
        "2df3077",
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
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
    with open(configPath, "w") as f:
        f.write("env:\n  bin_dir:\n  inc_dir:\n")

    # Install latest tt.
    install_cmd = [tt_cmd, "--cfg", configPath, "binary", "--scope", "project", "install", "tt"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    os.remove(configPath)

    installed_cmd = [tmp_path / ".tarantool" / "bin" / "tt", "version"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmp_path / ".tarantool" / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool CLI version \d+.\d+.\d+", start_output)


@pytest.mark.slow
def test_install_tt_specific_commit(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    with open(configPath, "w") as f:
        f.write("env:\n  bin_dir:\n  inc_dir:\n")

    # Install specific tt's commit.
    install_cmd = [
        tt_cmd,
        "--cfg",
        configPath,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "97c7b73",
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0

    installed_cmd = [tmp_path / ".tarantool" / "bin" / "tt", "version"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmp_path / ".tarantool" / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool CLI version 2.1.1", start_output)
    assert re.search(r"commit: 97c7b73", start_output)


@pytest.mark.slow
def test_wrong_format_hash(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    with open(configPath, "w") as f:
        f.write("env:\n  bin_dir:\n  inc_dir:\n")

    # Install specific tt's commit.
    install_cmd = [
        tt_cmd,
        "--cfg",
        configPath,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "111",
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc != 0
    assert re.search(r"Searching in versions...", instance_process.stdout.readline())
    assert re.search(r"Searching in commits...", instance_process.stdout.readline())
    assert re.search(
        r"the hash must contain at least 7 characters",
        instance_process.stdout.readline(),
    )

    # Install specific tt's commit.
    install_cmd_second = [
        tt_cmd,
        "--cfg",
        configPath,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "zzzzzzz",
    ]
    instance_process_second = subprocess.Popen(
        install_cmd_second,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    instance_process_rc = instance_process_second.wait()
    assert instance_process_rc != 0
    first_output = instance_process_second.stdout.readline()
    assert re.search(r"Searching in commits...", first_output)
    second_output = instance_process_second.stdout.readline()
    assert re.search(r"hash has a wrong format", second_output)


@pytest.mark.slow
@pytest.mark.parametrize(
    "required_ver, installed_ver",
    [
        ("2.1.2", "2.1.2"),
        ("v2.2", "2.2.1"),
        ("1", "1.3.1"),
    ],
)
def test_install_tt_specific_version(tt_cmd, tmp_path, required_ver: str, installed_ver: str):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    with open(configPath, "w") as f:
        f.write("env:\n  bin_dir:\n  inc_dir:\n")

    # Install latest tt.
    install_cmd = [
        tt_cmd,
        "--cfg",
        configPath,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        required_ver,
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    os.remove(configPath)

    installed_cmd = [tmp_path / ".tarantool" / "bin" / "tt", "version"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmp_path / ".tarantool" / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_output = installed_program_process.stdout.readline()
    assert re.search(rf"Tarantool CLI.*{installed_ver}", start_output)


@pytest.mark.slow
def test_install_tarantool_commit(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "", "inc_dir": "./my_inc"}}, f)

    tmp_path_without_config = tmp_path / "project"
    tmp_path_without_config.mkdir()
    root = tmp_path_without_config / ".tarantool"

    # Install specific tarantool's commit.
    install_cmd = [
        tt_cmd,
        "--cfg",
        config_path,
        "binary",
        "--scope",
        "project",
        "install",
        "-f",
        "tarantool",
        "--source-build",
        "00a9e59",
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path_without_config,
        stderr=subprocess.STDOUT,
        # Do not use pipe for stdout, if you are not going to read from it.
        # In case of build failure, logs are printed to stdout. It fills pipe buffer and
        # blocks all subsequent stdout write calls in tt, because there is no pipe reader in test.
        stdout=subprocess.DEVNULL,
        text=True,
    )

    # Check that the process was shutdown correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    installed_cmd = [root / "bin" / "tarantool", "-v"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=root / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)
    assert (root / "include" / "tarantool").is_dir()

    assert is_valid_tarantool_installed(
        root / "bin",
        root / "include",
        str(root / "versions" / "tarantool" / "tarantool_00a9e59" / "tarantool"),
        str(root / "versions" / "tarantool" / "tarantool_00a9e59" / "include" / "tarantool"),
    )


@pytest.mark.skipif(
    is_tarantool_ee(),
    reason="Tarantool CE required",
)
@pytest.mark.slow
@pytest.mark.docker
def test_install_tarantool_in_docker(tt_cmd, tmp_path):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    config_path = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "", "inc_dir": "./my_inc"}}, f)

    tmp_path_without_config = tmp_path / "project"
    tmp_path_without_config.mkdir()
    root = tmp_path_without_config / ".tarantool"

    commit = get_tarantool_commit()

    # Install latest tarantool.
    install_cmd = [
        tt_cmd,
        "-V",
        "--cfg",
        config_path,
        "binary",
        "--scope",
        "project",
        "install",
        "-f",
        "tarantool",
        "--source-build",
        commit,
        "--use-docker",
    ]
    tt_process = subprocess.run(
        install_cmd,
        cwd=tmp_path_without_config,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    assert tt_process.returncode == 0

    installed_cmd = [root / "bin" / "tarantool", "-v"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=root / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)

    # spell-checker:ignore objdump
    # Check tarantool glibc version.
    out = subprocess.getoutput(
        "objdump -T "
        + str(root / "bin" / "tarantool")
        + " | grep -o -E 'GLIBC_[.0-9]+' | sort -V | tail -n1",
    )
    assert out == "GLIBC_2.29"

    assert (root / "include" / "tarantool").is_dir()


@pytest.mark.parametrize(
    "args, error",
    [
        (
            ["--local-repo", "distfiles", "tarantool", "--from-build", "build"],
            "unknown flag: --local-repo",
        ),
        (
            ["tarantool", "--from-build", "build", "--local-repo", "distfiles"],
            "group [from-build local-repo]",
        ),
        (
            ["tarantool", "unexpected", "--from-build", "build"],
            "accepts 0 arg(s), received 1",
        ),
        (
            ["tarantool", "--include-dir", "headers"],
            "--include-dir requires --from-build",
        ),
        (
            ["tarantool", "--from-build="],
            "--from-build requires a non-empty directory",
        ),
    ],
)
def test_install_tarantool_from_build_invalid_options(tt_cmd, tmp_path, args, error):
    testdata = os.path.join(os.path.dirname(__file__), "testdata", "tt_basic")
    shutil.copytree(testdata, tmp_path, symlinks=True, dirs_exist_ok=True)

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "--scope", "project", "install", *args],
        cwd=tmp_path,
    )

    assert rc != 0
    assert error in output
    assert is_valid_tarantool_installed(
        tmp_path / "bin",
        tmp_path / "inc" / "include",
        str(tmp_path / "bin" / "tarantool_2.10.8"),
        str(tmp_path / "inc" / "include" / "tarantool_2.10.8"),
    )


@pytest.mark.parametrize("package_to_exclude", [pytest.param("mage"), pytest.param("git")])
def test_install_tt_fail_exit_code_dependency_check(tt_cmd, tmp_path, package_to_exclude):
    # Create new PATH with excluded packages.
    original_path = os.environ["PATH"]
    new_path = os.pathsep.join(
        filter(
            lambda dir: os.path.isdir(dir) and package_to_exclude not in os.listdir(dir),
            original_path.split(os.pathsep),
        ),
    )

    # Create test config.
    configPath = os.path.join(tmp_path, config_name)
    with open(configPath, "w") as f:
        f.write("env:\n  bin_dir:\n  inc_dir:\n")
    install_cmd = [
        tt_cmd,
        "--cfg",
        configPath,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "--source-build",
    ]
    install_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        env={"PATH": new_path},
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    install_process_rc = install_process.wait()
    assert install_process_rc == 1


@pytest.mark.slow
@pytest.mark.parametrize(
    "old_tarantool_version, expected_install_msg, is_interactive",
    [
        pytest.param("7192bf6", "Found newest commit of tarantool in master", True),
        pytest.param("master", "tarantool_master version of tarantool already exists", False),
        pytest.param("7192bf6", "tarantool_master version of tarantool already exists", False),
    ],
)
def test_install_tarantool_fetch_latest_version(
    tt_cmd,
    tmp_path,
    old_tarantool_version,
    expected_install_msg,
    is_interactive,
):
    config_path = os.path.join(tmp_path, config_name)
    # Create test config.
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "", "inc_dir": "./my_inc"}}, f)

    tmp_path_without_config = tmp_path / "project"
    tmp_path_without_config.mkdir()
    root = tmp_path_without_config / ".tarantool"

    # Install not latest version of tarantool by commit hash.
    install_cmd = [
        tt_cmd,
        "--cfg",
        config_path,
        "binary",
        "--scope",
        "project",
        "install",
        "-f",
        "tarantool",
        "--source-build",
        old_tarantool_version,
        "--dynamic",
    ]
    instance_process_rc, _ = run_command_and_get_output(
        install_cmd,
        cwd=tmp_path_without_config,
        stdout=subprocess.DEVNULL,
    )
    assert instance_process_rc == 0

    # Rename the entire installation to master to simulate an older master build.
    versions = root / "versions" / "tarantool"
    master = versions / "tarantool_master"
    (versions / ("tarantool_" + old_tarantool_version)).rename(master)
    for directory in ["bin", "include"]:
        link = root / directory / "tarantool"
        link.unlink()
        link.symlink_to(master / directory / "tarantool")

    # Installing newest version.
    install_cmd = [tt_cmd, "--cfg", config_path]
    if not is_interactive:
        install_cmd.append("--no-prompt")
    install_cmd.extend(
        [
            "binary",
            "--scope",
            "project",
            "install",
            "-f",
            "tarantool",
            "--source-build",
            "master",
            "--dynamic",
        ],
    )

    instance_process_rc, output = run_command_and_get_output(
        install_cmd,
        cwd=tmp_path_without_config,
        input="y\n" if is_interactive is True else None,
    )
    assert instance_process_rc == 0
    assert expected_install_msg in output


@pytest.mark.slow
@pytest.mark.parametrize(
    "active_symlink, expected_install_msg, is_interactive",
    [
        ("tt_master", "Found newest commit of tt in master", True),
        ("tt_master", "tt_master version of tt already exists.", False),
        ("tt_v1.2.3", "Found newest commit of tt in master", True),
    ],
)
def test_install_tt_fetch_latest_version(
    active_symlink,
    expected_install_msg,
    is_interactive,
    tt_cmd,
    tmp_path,
):
    config_path = os.path.join(tmp_path, config_name)
    # Create test config
    with open(config_path, "w") as f:
        f.write("env:\n  bin_dir: \n  inc_dir:\n")

    # Create executable file with 'tt version --commit'
    # functionality to emulate real work of tt. We need
    # the following to avoid a situation where the last
    # git commit in the master branch makes it impossible
    # to detect the latest version which contradicts the test logic.
    root = tmp_path / ".tarantool"
    versions = root / "versions" / "tt"
    for directory, version in [("tt_master", "2.0.0.677234a"), ("tt_v1.2.3", "1.2.3.087ac72")]:
        binary = versions / directory / "tt"
        binary.parent.mkdir(parents=True)
        binary.write_text(f'#!/bin/sh\necho "{version}"\n')
        binary.chmod(0o755)
    (root / "bin").mkdir()
    (root / "bin" / "tt").symlink_to(versions / active_symlink / "tt")

    install_cmd = [tt_cmd, "--cfg", config_path]
    if not is_interactive:
        install_cmd.append("--no-prompt")
    install_cmd.extend(["binary", "--scope", "project", "install", "tt", "master"])
    instance_process_rc, install_output = run_command_and_get_output(
        install_cmd,
        cwd=tmp_path,
        input="y\n" if is_interactive is True else None,
    )
    assert instance_process_rc == 0
    assert expected_install_msg in install_output
