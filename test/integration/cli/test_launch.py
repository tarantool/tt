import os
import subprocess
import tempfile

import pytest
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
    assert subprocess.run(cmd).returncode == 0

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

    tt_message = "Hello, I'm CLI exec!"
    with open(os.path.join(tmpdir, "tt"), "w") as f:
        f.write(f"#!/bin/sh\necho \"{tt_message}\"")

    # tt found but not executable - there should be an error.
    commands = [
        [tt_cmd, "version"],
        [tt_cmd, "-L", tmpdir, "version"]
    ]

    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 1
        assert tt_message not in output

    # tt found and executable.
    os.chmod(os.path.join(tmpdir, "tt"), 0o777)
    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 0
        assert tt_message in output


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
