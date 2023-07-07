import os
import platform
import re
import shutil
import subprocess
import tempfile

import pytest
import yaml

from utils import config_name, is_valid_tarantool_installed


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
    install_cmd = [tt_cmd, "--cfg", config_path, "install", "-f", "tarantool", "2.10.7"]
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
    assert os.path.exists(os.path.join(tmpdir, "bin", "tarantool_2.10.7"))


@pytest.mark.slow
@pytest.mark.docker
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


def test_install_tarantool_dev_bin_invalid(tt_cmd, tmpdir):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata")
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    tt_dir = "tt_basic"
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

        assert is_valid_tarantool_installed(
            os.path.join(testdata_path, tt_dir, "bin"),
            os.path.join(testdata_path, tt_dir, "inc", "include"),
            os.path.join(testdata_path, tt_dir, "bin", "tarantool_2.10.8"),
            os.path.join(testdata_path, tt_dir, "inc", "include",
                         "tarantool_2.10.8")
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
    )
])
def test_install_tarantool_dev_no_include_option(
        tt_cmd,
        tmpdir,
        build_dir,
        exec_rel_path,
        include_rel_path,
        tt_dir
):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata")
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

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
        tt_cmd, tmpdir, rc, include_dir, tt_dir
):
    # Copy test files.
    testdata_path = os.path.join(os.path.dirname(__file__), "testdata")
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

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
