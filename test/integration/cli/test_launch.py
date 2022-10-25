import os
import subprocess
import tempfile

import pytest
import yaml

from utils import (create_external_module, create_tt_config,
                   run_command_and_get_output)

# Some of the tests below should check the behavior
# of modules that have an internal implementation.
# `version` module is the lightest module, so we test using it.


# ##### #
# Tests #
# ##### #
def test_local_launch(tt_cmd, tmpdir):
    module = "version"
    cmd = [tt_cmd, module, "-L", tmpdir]

    # No configuration file specified.
    assert subprocess.run(cmd).returncode == 1

    # With the specified config file.
    create_tt_config(tmpdir, tmpdir)
    module_message = create_external_module(module, tmpdir)

    rc, output = run_command_and_get_output(cmd, cwd=os.getcwd())
    assert rc == 0
    assert module_message in output


def test_local_launch_find_cfg(tt_cmd, tmpdir):
    module = "version"

    # Find tarantool.yaml at cwd parent.
    tmpdir_without_config = tempfile.mkdtemp(dir=tmpdir)
    cmd = [tt_cmd, module, "-L", tmpdir_without_config]

    create_tt_config(tmpdir, tmpdir)
    module_message = create_external_module(module, tmpdir)

    rc, output = run_command_and_get_output(cmd, cwd=os.getcwd())
    assert rc == 0
    assert module_message in output


def test_local_launch_find_cfg_modules_relative_path(tt_cmd, tmpdir):
    module = "version"

    # Find tarantool.yaml at cwd parent.
    tmpdir_without_config = tempfile.mkdtemp(dir=tmpdir)
    cmd = [tt_cmd, module]

    modules_dir = os.path.join(tmpdir, "ext_modules")
    os.mkdir(modules_dir)
    create_tt_config(tmpdir, os.path.join(".", "ext_modules"))
    module_message = create_external_module(module, modules_dir)

    rc, output = run_command_and_get_output(cmd, cwd=tmpdir_without_config)
    assert rc == 0
    assert module_message in output


def test_local_launch_non_existent_dir(tt_cmd, tmpdir):
    module = "version"
    cmd = [tt_cmd, module, "-L", "non-exists-dir"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)

    assert rc == 1
    assert "Failed to change working directory" in output


# This test looking for tarantool.yaml from cwd to root (without -L flag).
def test_default_launch_find_cfg_at_cwd(tt_cmd, tmpdir):
    module = "version"
    module_message = create_external_module(module, tmpdir)

    # Find tarantool.yaml at current work directory.
    create_tt_config(tmpdir, tmpdir)

    cmd = [tt_cmd, module]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert module_message in output


def test_default_launch_find_cfg_at_parent(tt_cmd, tmpdir):
    module = "version"
    module_message = create_external_module(module, tmpdir)

    create_tt_config(tmpdir, tmpdir)
    cmd = [tt_cmd, module]

    # Find tarantool.yaml at cwd parent.
    tmpdir_without_config = tempfile.mkdtemp(dir=tmpdir)
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir_without_config)
    assert rc == 0
    assert module_message in output


def test_launch_local_tt_executable(tt_cmd, tmpdir):
    # We check if exec works on the local tt executable.
    # In the future, the same should be done when checking the
    # local Tarantool executable, but so far this is impossible.
    create_tt_config(tmpdir, tmpdir)
    os.mkdir(tmpdir + "/bin")

    tt_message = "Hello, I'm CLI exec!"
    with open(os.path.join(tmpdir, "bin/tt"), "w") as f:
        f.write(f"#!/bin/sh\necho \"{tt_message}\"")

    # tt found but not executable - there should be an error.
    commands = [
        [tt_cmd, "version"],
        [tt_cmd, "-L", tmpdir, "version"],
        [tt_cmd, "version", "--cfg", os.path.join(tmpdir, "tarantool.yaml")]
    ]

    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 1
        assert "permission denied" in output

    # tt found and executable.
    os.chmod(os.path.join(tmpdir, "bin/tt"), 0o777)
    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 0
        assert tt_message in output


def test_launch_local_tt_executable_in_parent_dir(tt_cmd, tmpdir):
    create_tt_config(tmpdir, tmpdir)
    os.mkdir(tmpdir + "/bin")

    tt_message = "Hello, I'm CLI exec!"
    with open(os.path.join(tmpdir, "bin/tt"), "w") as f:
        f.write(f"#!/bin/sh\necho \"{tt_message}\"")

    commands = [
        [tt_cmd, "version"],
        [tt_cmd, "-L", tmpdir, "version"]
    ]

    tmpdir_without_config = tempfile.mkdtemp(dir=tmpdir)
    os.chmod(os.path.join(tmpdir, "bin/tt"), 0o777)
    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmpdir_without_config)
        assert rc == 0
        assert tt_message in output


def test_launch_local_tt_executable_relative_bin_dir(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))

    tt_message = "Hello, I'm CLI exec!"
    with open(os.path.join(tmpdir, "binaries/tt"), "w") as f:
        f.write(f"#!/bin/sh\necho \"{tt_message}\"")
    os.chmod(os.path.join(tmpdir, "binaries", "tt"), 0o777)

    commands = [
        [tt_cmd, "version"],
        [tt_cmd, "-L", tmpdir, "version"]
    ]

    with tempfile.TemporaryDirectory(dir=tmpdir) as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 0
            assert tt_message in output


def test_launch_local_tt_missing_executable(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))

    commands = [
        [tt_cmd, "version"],
        [tt_cmd, "-L", tmpdir, "version"],
        [tt_cmd, "version", "--cfg", config_path]
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            # No error. Current tt is executed.
            assert rc == 0
            assert "Tarantool CLI version" in output


def test_launch_local_tarantool(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmpdir, "binaries/tarantool"), "w") as f:
        f.write(f"#!/bin/sh\necho \"{tarantool_message}\"")
    os.chmod(os.path.join(tmpdir, "binaries/tarantool"), 0o777)

    commands = [
        [tt_cmd, "run", "-L", tmpdir, "--version"],
        [tt_cmd, "run", "--cfg", config_path, "--version"]
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 0
            assert tarantool_message in output


def test_launch_local_tarantool_missing_in_bin_dir(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))

    commands = [
        [tt_cmd, "run", "--version"],
        [tt_cmd, "run", "-L", tmpdir, "--version"],
        [tt_cmd, "run", "--cfg", config_path, "--version"]
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            # Missing binaries is not a error. Default Tarantool is used.
            assert rc == 0
            assert "Tarantool" in output


def test_launch_local_launch_tarantool_with_config_in_parent_dir(tt_cmd, tmpdir):
    tmpdir_without_config = tempfile.mkdtemp(dir=tmpdir)
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmpdir, "binaries/tarantool"), "w") as f:
        f.write(f"#!/bin/sh\ntouch file.txt\necho \"{tarantool_message}\"")
    os.chmod(os.path.join(tmpdir, "binaries/tarantool"), 0o777)

    commands = [
        [tt_cmd, "run", "-L", tmpdir_without_config, "--version"],
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 0
            assert tarantool_message in output
            assert os.path.exists(os.path.join(tmpdir_without_config, "file.txt"))


def test_launch_system_tarantool(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"modules": {"directory": f"{tmpdir}"},
                   "app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmpdir, "binaries/tarantool"), "w") as f:
        f.write(f"#!/bin/sh\necho \"{tarantool_message}\"")
    os.chmod(os.path.join(tmpdir, "binaries/tarantool"), 0o777)

    command = [tt_cmd, "run", "-S"]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        with open(os.path.join(tmp_working_dir, "tarantool.yaml"), "w") as f:
            yaml.dump({"tt": {"modules": {"directory": f"{tmpdir}"},
                       "app": {"bin_dir": ""}}}, f)
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmpdir
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 0
        assert tarantool_message in output


def test_launch_system_tarantool_missing_executable(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"modules": {"directory": f"{tmpdir}"},
                   "app": {"bin_dir": "./binaries"}}}, f)

    command = [tt_cmd, "run", "-S", "--version"]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmpdir
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 0
        assert "Tarantool" in output


def test_launch_system_config_not_loaded_if_local_enabled(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmpdir, "binaries/tarantool"), "w") as f:
        f.write(f"#!/bin/sh\necho \"{tarantool_message}\"")
    os.chmod(os.path.join(tmpdir, "binaries/tarantool"), 0o777)

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        command = [tt_cmd, "run", "-L", tmp_working_dir, "--version"]
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmpdir
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 1
        assert "Failed to find Tarantool CLI config for " in output


def test_launch_system_config_not_loaded_if_cfg_specified_is_missing(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))
    tarantool_message = "Hello, I'm Tarantool"
    with open(os.path.join(tmpdir, "binaries/tarantool"), "w") as f:
        f.write(f"#!/bin/sh\necho \"{tarantool_message}\"")
    os.chmod(os.path.join(tmpdir, "binaries/tarantool"), 0o777)

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        command = [tt_cmd, "run", "-c", os.path.join(tmp_working_dir, "tarantool.yaml"),
                   "--version"]
        my_env = os.environ.copy()
        my_env["TT_SYSTEM_CONFIG_DIR"] = tmpdir
        rc, output = run_command_and_get_output(command, cwd=tmp_working_dir, env=my_env)
        assert rc == 1
        assert "Failed to configure Tarantool CLI" in output


def test_launch_ambiguous_config_opts(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"app": {"bin_dir": "./binaries"}}}, f)

    os.mkdir(os.path.join(tmpdir, "binaries"))

    commands = [
        [tt_cmd, "run", "--cfg", config_path, "-L", tmpdir, "--version"],
        [tt_cmd, "run", "--cfg", config_path, "-S", "--version"],
        [tt_cmd, "run", "-S", "-L", tmpdir, "--version"],
    ]

    with tempfile.TemporaryDirectory() as tmp_working_dir:
        for cmd in commands:
            rc, output = run_command_and_get_output(cmd, cwd=tmp_working_dir)
            assert rc == 1
            assert "You can specify only one of" in output


def test_external_module_without_internal_implementation(tt_cmd, tmpdir):
    # Create an external module, which don't have internal
    # implementation.
    module = "abc-example"
    module_message = create_external_module(module, tmpdir)
    create_tt_config(tmpdir, tmpdir)

    cmd = [tt_cmd, module]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert module_message in output

    # Trying to start external module with -I flag.
    # In this case, tt should ignore this flag and just
    # start module.
    cmd = [tt_cmd, module, "-I"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert module_message in output


def test_launch_with_cfg_flag(tt_cmd, tmpdir):
    module = "version"

    # Send non existent config path.
    non_exist_cfg_tmpdir = tempfile.mkdtemp(dir=tmpdir)
    cmd = [tt_cmd, module, "--cfg", "non-exists-path"]
    rc, output = run_command_and_get_output(cmd, cwd=non_exist_cfg_tmpdir)
    assert rc == 1
    assert "Specified path to the configuration file is invalid" in output

    # Create one more temporary directory
    exists_cfg_tmpdir = tempfile.mkdtemp(dir=tmpdir)
    module_message = create_external_module(module, exists_cfg_tmpdir)
    config_path = create_tt_config(exists_cfg_tmpdir, exists_cfg_tmpdir)

    cmd = [tt_cmd, module, "--cfg", config_path]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert module_message in output


@pytest.mark.parametrize("module", ["version", "non-exists-module"])
def test_launch_external_cmd_with_flags(tt_cmd, tmpdir, module):
    module_message = create_external_module(module, tmpdir)
    create_tt_config(tmpdir, tmpdir)

    cmd = [tt_cmd, module, "--non-existent-flag", "-f", "argument1"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert module_message in output
