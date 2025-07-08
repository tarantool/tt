import os
import shutil
import subprocess
import tempfile
from pathlib import Path

import pytest
import yaml

from utils import config_name, create_external_module, create_tt_config, run_command_and_get_output

# Some of the tests below should check the behavior
# of modules that have an internal implementation.
# `version` module is the lightest module, so we test using it.


# ##### #
# Tests #
# ##### #
def test_local_launch(tt_cmd, tmp_path):
    module = "version"
    cmd = [tt_cmd, "-L", tmp_path, module]

    # No configuration file specified.
    assert subprocess.run(cmd).returncode == 1

    # With the specified config file.
    create_tt_config(tmp_path, tmp_path / "modules")
    module_message = create_external_module(module, tmp_path / "modules")

    rc, output = run_command_and_get_output(cmd, cwd=os.getcwd())
    assert rc == 0
    assert module_message in output


def test_local_launch_find_cfg(tt_cmd, tmp_path):
    module = "version"

    # Find tt.yaml at cwd parent.
    tmpdir_without_config = tempfile.mkdtemp(dir=tmp_path)
    cmd = [tt_cmd, "-L", tmpdir_without_config, module]

    create_tt_config(tmp_path, tmp_path / "modules")
    module_message = create_external_module(module, tmp_path / "modules")

    rc, output = run_command_and_get_output(cmd, cwd=os.getcwd())
    assert rc == 0
    assert module_message in output


def test_local_launch_find_cfg_modules_relative_path(tt_cmd, tmp_path):
    module = "version"

    # Find tt.yaml at cwd parent.
    tmpdir_without_config = tempfile.mkdtemp(dir=tmp_path)
    cmd = [tt_cmd, module]

    modules_dir = tmp_path / "ext_modules"
    modules_dir.mkdir(exist_ok=True, parents=True)
    create_tt_config(tmp_path, os.path.join(".", "ext_modules"))
    module_message = create_external_module(module, modules_dir)

    rc, output = run_command_and_get_output(cmd, cwd=tmpdir_without_config)
    assert rc == 0
    assert module_message in output


def test_local_launch_non_existent_dir(tt_cmd, tmp_path):
    module = "version"
    cmd = [tt_cmd, "-L", "non-exists-dir", module]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 1
    assert "failed to change working directory" in output


# This test looking for tt.yaml from cwd to root (without -L flag).
def test_default_launch_find_cfg_at_cwd(tt_cmd, tmp_path):
    module = "version"
    module_message = create_external_module(module, tmp_path / "modules")

    # Find tt.yaml at current work directory.
    create_tt_config(tmp_path, tmp_path / "modules")

    cmd = [tt_cmd, module]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert module_message in output


def test_default_launch_find_cfg_at_parent(tt_cmd, tmp_path):
    module = "version"
    module_message = create_external_module(module, tmp_path / "modules")

    create_tt_config(tmp_path, tmp_path / "modules")
    cmd = [tt_cmd, module]

    # Find tt.yaml at cwd parent.
    tmpdir_without_config = tempfile.mkdtemp(dir=tmp_path)
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir_without_config)
    assert rc == 0
    assert module_message in output


def test_launch_local_tt_executable(tt_cmd, tmp_path):
    # We check if exec works on the local tt executable.
    # In the future, the same should be done when checking the
    # local Tarantool executable, but so far this is impossible.
    create_tt_config(tmp_path, tmp_path / "modules")
    os.mkdir(tmp_path / "bin")

    tt_message = "Hello, I'm CLI exec!"
    with open(os.path.join(tmp_path, "bin/tt"), "w") as f:
        f.write(f'#!/bin/sh\necho "{tt_message}"')

    # tt found but not executable - there should be an error.
    commands = [
        [tt_cmd, "version"],
        [tt_cmd, "-L", tmp_path, "version"],
        [tt_cmd, "version", "--cfg", tmp_path / config_name],
    ]

    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 1
        assert "permission denied" in output

    # tt found and executable.
    os.chmod(tmp_path / "bin" / "tt", 0o777)
    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 0
        assert tt_message in output


def test_launch_local_tt_executable_in_parent_dir(tt_cmd, tmp_path):
    create_tt_config(tmp_path, tmp_path / "modules")
    os.mkdir(tmp_path / "bin")

    tt_message = "Hello, I'm CLI exec!"
    with open(tmp_path / "bin" / "tt", "w") as f:
        f.write(f'#!/bin/sh\necho "{tt_message}"')

    commands = [[tt_cmd, "version"], [tt_cmd, "-L", tmp_path, "version"]]

    tmpdir_without_config = tempfile.mkdtemp(dir=tmp_path)
    os.chmod(tmp_path / "bin" / "tt", 0o777)
    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmpdir_without_config)
        assert rc == 0
        assert tt_message in output


def test_launch_local_tt_executable_relative_bin_dir(tt_cmd, tmp_path):
    config_path = tmp_path / config_name
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")

    tt_message = "Hello, I'm CLI exec!"
    with open(tmp_path / "binaries" / "tt", "w") as f:
        f.write(f'#!/bin/sh\necho "{tt_message}"')
    os.chmod(tmp_path / "binaries" / "tt", 0o777)

    commands = [[tt_cmd, "version"], [tt_cmd, "-L", tmp_path, "version"]]

    with tempfile.TemporaryDirectory(dir=tmp_path) as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 0
            assert tt_message in output


def test_launch_local_tt_missing_executable(tt_cmd, tmp_path):
    config_path = tmp_path / config_name
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")

    commands = [
        [tt_cmd, "version"],
        [tt_cmd, "-L", tmp_path, "version"],
        [tt_cmd, "--cfg", config_path, "version"],
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            # No error. Current tt is executed.
            assert rc == 0
            assert "Tarantool CLI version" in output


def test_launch_local_tarantool(tt_cmd, tmp_path):
    config_path = tmp_path / config_name
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")
    tarantool_message = "Hello, I'm Tarantool"
    with open(tmp_path / "binaries" / "tarantool", "w") as f:
        f.write(f'#!/bin/sh\necho "{tarantool_message}"')
    os.chmod(tmp_path / "binaries" / "tarantool", 0o777)

    commands = [
        [tt_cmd, "-L", tmp_path, "run", "--version"],
        [tt_cmd, "--cfg", config_path, "run", "--version"],
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 0
            assert tarantool_message in output


def test_launch_local_tarantool_missing_in_bin_dir(tt_cmd, tmp_path):
    config_path = tmp_path / config_name
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")

    commands_in_tmp = [
        [tt_cmd, "run", "--version"],
    ]

    commands_external = [
        [tt_cmd, "-L", tmp_path, "run", "--version"],
        [tt_cmd, "--cfg", config_path, "run", "--version"],
    ]

    for cmd in commands_in_tmp:
        rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
        # Missing binaries is not a error. Default Tarantool is used.
        assert rc == 0
        assert "Tarantool" in output

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands_external:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            # Missing binaries is not a error. Default Tarantool is used.
            assert rc == 0
            assert "Tarantool" in output


def test_launch_local_launch_tarantool_with_config_in_parent_dir(tt_cmd, tmp_path):
    tmpdir_without_config = tempfile.mkdtemp(dir=tmp_path)
    config_path = tmp_path / config_name
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmp_path, "binaries/tarantool"), "w") as f:
        f.write(f'#!/bin/sh\ntouch file.txt\necho "{tarantool_message}"')
    os.chmod(os.path.join(tmp_path, "binaries/tarantool"), 0o777)

    commands = [
        [tt_cmd, "-L", tmpdir_without_config, "run", "--version"],
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 0
            assert tarantool_message in output
            assert os.path.exists(os.path.join(tmpdir_without_config, "file.txt"))


def test_launch_local_launch_tarantool_with_yml_config_in_parent_dir(tt_cmd, tmp_path):
    tmpdir_without_config = tempfile.mkdtemp(dir=tmp_path)
    config_path = tmp_path / config_name.replace("yaml", "yml")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmp_path, "binaries/tarantool"), "w") as f:
        f.write(f'#!/bin/sh\ntouch file.txt\necho "{tarantool_message}"')
    os.chmod(os.path.join(tmp_path, "binaries/tarantool"), 0o777)

    commands = [
        [tt_cmd, "-L", tmpdir_without_config, "run", "--version"],
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 0
            assert tarantool_message in output
            assert os.path.exists(os.path.join(tmpdir_without_config, "file.txt"))


def test_launch_system_tarantool(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    with open(config_path, "w") as f:
        yaml.dump({"modules": {"directory": f"{tmp_path}"}, "env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmp_path, "binaries/tarantool"), "w") as f:
        f.write(f'#!/bin/sh\necho "{tarantool_message}"')
    os.chmod(os.path.join(tmp_path, "binaries/tarantool"), 0o777)

    command = [tt_cmd, "-S", "run"]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        with open(os.path.join(tmp_working_dir, config_name), "w") as f:
            yaml.dump({"modules": {"directory": f"{tmp_path}"}, "env": {"bin_dir": ""}}, f)
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmp_path
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 0
        assert tarantool_message in output


def test_launch_system_tarantool_yml_system_config(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name.replace("yaml", "yml"))
    with open(config_path, "w") as f:
        yaml.dump({"modules": {"directory": f"{tmp_path}"}, "env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmp_path, "binaries/tarantool"), "w") as f:
        f.write(f'#!/bin/sh\necho "{tarantool_message}"')
    os.chmod(os.path.join(tmp_path, "binaries/tarantool"), 0o777)

    command = [tt_cmd, "-S", "run"]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        with open(os.path.join(tmp_working_dir, config_name.replace("yaml", "yml")), "w") as f:
            yaml.dump({"tt": {"modules": {"directory": f"{tmp_path}"}, "env": {"bin_dir": ""}}}, f)
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmp_path
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 0
        assert tarantool_message in output


def test_launch_system_tarantool_missing_executable(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    with open(config_path, "w") as f:
        yaml.dump({"modules": {"directory": f"{tmp_path}"}, "env": {"bin_dir": "./binaries"}}, f)

    command = [tt_cmd, "-S", "run", "--version"]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmp_path
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 0
        assert "Tarantool" in output


def test_launch_system_config_not_loaded_if_local_enabled(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": "./binaries"}}, f)

    os.mkdir(tmp_path / "binaries")
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmp_path, "binaries/tarantool"), "w") as f:
        f.write(f'#!/bin/sh\necho "{tarantool_message}"')
    os.chmod(os.path.join(tmp_path, "binaries/tarantool"), 0o777)

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        command = [tt_cmd, "-L", tmp_working_dir, "run", "--version"]
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmp_path
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 1
        assert "failed to find Tarantool CLI config for " in output


def test_launch_system_config_not_loaded_if_cfg_specified_is_missing(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"env": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(tmp_path / "binaries")
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmp_path, "binaries/tarantool"), "w") as f:
        f.write(f'#!/bin/sh\necho "{tarantool_message}"')
    os.chmod(os.path.join(tmp_path, "binaries/tarantool"), 0o777)

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        command = [tt_cmd, "-c", os.path.join(tmp_working_dir, config_name), "run", "--version"]
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmp_path
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 1
        assert "Failed to configure Tarantool CLI" in output


def test_launch_ambiguous_config_opts(tt_cmd, tmp_path):
    config_path = os.path.join(tmp_path, config_name)
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"env": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(tmp_path / "binaries")

    commands = [
        [tt_cmd, "--cfg", config_path, "-L", tmp_path, "run", "--version"],
        [tt_cmd, "--cfg", config_path, "-S", "run", "--version"],
        [tt_cmd, "-S", "-L", tmp_path, "run", "--version"],
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 1
            assert "you can specify only one of" in output


def test_external_module_without_internal_implementation(tt_cmd, tmp_path):
    # Create an external module, which don't have internal
    # implementation.
    module = "abc-example"
    module_message = create_external_module(module, tmp_path / "modules")
    create_tt_config(tmp_path, tmp_path / "modules")

    cmd = [tt_cmd, module]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert module_message in output

    # Trying to start external module with -I flag.
    # In this case, tt should ignore this flag and just
    # start module.
    cmd = [tt_cmd, "-I", module]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert module_message in output


def test_launch_with_cfg_flag(tt_cmd, tmp_path):
    module = "version"

    # Send non existent config path.
    non_exist_cfg_tmpdir = tempfile.mkdtemp(dir=tmp_path)
    cmd = [tt_cmd, "--cfg", "non-exists-path", module]
    rc, output = run_command_and_get_output(cmd, cwd=non_exist_cfg_tmpdir)
    assert rc == 1
    assert "specified path to the configuration file is invalid" in output

    # Create one more temporary directory
    exists_cfg_tmpdir = Path(tempfile.mkdtemp(dir=tmp_path))
    module_message = create_external_module(module, exists_cfg_tmpdir / "modules")
    config_path = create_tt_config(exists_cfg_tmpdir, exists_cfg_tmpdir / "modules")

    cmd = [tt_cmd, "--cfg", config_path, module]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert module_message in output


@pytest.mark.parametrize("module", ["version", "non-exists-module"])
def test_launch_external_cmd_with_flags(tt_cmd, tmp_path, module):
    module_message = create_external_module(module, tmp_path / "modules")
    create_tt_config(tmp_path, tmp_path / "modules")

    cmd = [tt_cmd, module, "--non-existent-flag", "-f", "argument1"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert module_message in output


def test_std_err_stream_local_launch_non_existent_dir(tt_cmd, tmp_path):
    module = "version"
    cmd = [tt_cmd, "-L", "non-exists-dir", module]
    tt_process = subprocess.Popen(
        cmd,
        cwd=tmp_path,
        stderr=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1
    assert "failed to change working directory" not in tt_process.stdout.readline()
    assert "failed to change working directory" in tt_process.stderr.readline()


def test_launch_with_env_cfg(tt_cmd):
    cmd = [tt_cmd, "cfg", "dump"]

    tmpdir_with_env_config = tempfile.mkdtemp()
    tmpdir_with_flag_config = tempfile.mkdtemp()
    try:
        # Test 'TT_CLI_CFG' env. variable.
        env_config_path = tmpdir_with_env_config + "/tt.yaml"
        with open(env_config_path, "w") as f:
            yaml.dump({"env": {"bin_dir": "foo/binaries"}}, f)
        test_env = os.environ.copy()
        test_env["TT_CLI_CFG"] = env_config_path

        rc, output = run_command_and_get_output(cmd, cwd=os.getcwd(), env=test_env)
        expected_bin_dir = "bin_dir: " + tmpdir_with_env_config + "/foo/binaries"
        assert rc == 0
        assert expected_bin_dir in output

        # Test that '-c' flag has higher priority than 'TT_CLI_CFG'.
        flag_config_path = tmpdir_with_flag_config + "/tt.yaml"
        with open(flag_config_path, "w") as f:
            yaml.dump({"env": {"bin_dir": "foo/my_cool_binaries"}}, f)
        cmd = [tt_cmd, "-c", flag_config_path, "cfg", "dump"]

        rc, output = run_command_and_get_output(cmd, cwd=os.getcwd(), env=test_env)
        expected_bin_dir = "bin_dir: " + tmpdir_with_flag_config + "/foo/my_cool_binaries"
        assert rc == 0
        assert expected_bin_dir in output
    finally:
        shutil.rmtree(tmpdir_with_env_config)
        shutil.rmtree(tmpdir_with_flag_config)


def test_launch_with_invalid_env_cfg(tt_cmd):
    cmd = [tt_cmd, "cfg", "dump"]

    # Set invalid 'TT_CLI_CFG' env. variable.
    test_env = os.environ.copy()
    test_env["TT_CLI_CFG"] = "foo/bar"

    rc, output = run_command_and_get_output(cmd, cwd=os.getcwd(), env=test_env)
    assert rc == 1
    assert "specified path to the configuration file is invalid" in output


def test_launch_with_verbose_output(tt_cmd, tmp_path):
    tmpdir_with_flag_config = Path(tempfile.mkdtemp())
    try:
        create_tt_config(tmpdir_with_flag_config, tmp_path)
        cmd = [tt_cmd, "-c", tmpdir_with_flag_config / "tt.yaml", "-V", "-h"]

        rc, output = run_command_and_get_output(cmd, cwd=os.getcwd())
        assert rc == 0
        assert str(tmpdir_with_flag_config / "tt.yaml") in output
    finally:
        shutil.rmtree(tmpdir_with_flag_config)


@pytest.mark.parametrize(
    "expected_output, is_self_enabled",
    [("tt", False), ("Tarantool CLI", True)],
)
def test_launch_tt_with_self_flag(expected_output, is_self_enabled, tt_cmd, tmp_path):
    # We check if tt can be executed itself with '-s' flag.
    # If this flag is provided we don't search for 'tt' binary file
    # in bin dir.
    os.mkdir(tmp_path / "bin")
    with open(tmp_path / "bin" / "tt", "w") as f:
        f.write('''#!/bin/sh
                echo "tt"''')
    os.chmod(tmp_path / "bin" / "tt", 0o775)

    configPath = tmp_path / config_name
    # Create test config.
    with open(configPath, "w") as f:
        f.write("env:\n    bin_dir: bin\n    inc_dir:\n")

    cmd = [tt_cmd, "--cfg", configPath]
    if is_self_enabled:
        cmd.append("-s")
    cmd.append("version")

    uninstall_process_rc, cmd_output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert uninstall_process_rc == 0
    assert expected_output in cmd_output


def test_local_launch_find_env_modules(tt_cmd, tmp_path):
    """
    Two external modules 'help' and 'version' exist at the same time,
    so the module 'help' should be called with the 'version' argument.
    One module configured with tt.yaml, while another with TT_CLI_MODULES_PATH.
    """
    modules = ("help", "version")
    cfg_env = tmp_path / "tt"

    create_tt_config(cfg_env, "modules")
    module_message = create_external_module(modules[0], cfg_env / "modules")
    create_external_module(modules[1], tmp_path / "ext_modules")

    cmd = [tt_cmd, *modules]

    rc, output = run_command_and_get_output(
        cmd,
        cwd=cfg_env,
        env={"TT_CLI_MODULES_PATH": str(tmp_path / "ext_modules")},
    )
    assert rc == 0
    assert f"{module_message}\nList of passed args: version\n" == output
    assert module_message in output


def test_local_launch_two_env_modules(tt_cmd, tmp_path):
    """
    Two external modules 'help' and 'version' exist at the same time,
    so the module 'help' should be called with the 'version' argument.
    Both modules configured through TT_CLI_MODULES_PATH.
    Run 'tt' without any config file.
    """
    modules = ("help", "version")

    modules_path = tmp_path / "modules"
    module_message = create_external_module(modules[0], modules_path / "modules1")
    create_external_module(modules[1], modules_path / "modules2")

    cmd = [tt_cmd, *modules]

    rc, output = run_command_and_get_output(
        cmd,
        env={"TT_CLI_MODULES_PATH": f"{modules_path / 'modules1'}:{modules_path / 'modules2'}"},
    )
    assert rc == 0
    assert f"{module_message}\nList of passed args: version\n" == output
    assert module_message in output
