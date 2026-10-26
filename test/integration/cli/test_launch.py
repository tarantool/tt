import os
import subprocess

import pytest
import yaml

from utils import (
    cluster_config_file,
    create_external_module,
    ignored_module_warning,
    instances_file,
    modules_path_env,
    run_command_and_get_output,
    run_command_and_get_streams,
)

# Some of the tests below should check the behavior
# of modules that have an internal implementation.
# `version` module is the lightest module, so we test using it.

# `tt run` acts on the package in the working directory, so the tests that
# reach the interpreter through it need a manifest there.
MANIFEST = """manifest_version = '0.1'

[package]
name = 'launch'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua']
default = true
"""

# A tt.yaml that tt would have read: its bin_dir names the directory the
# tests put a tt and a tarantool of their own into, which must not run.
TT_YAML = {"env": {"bin_dir": "bin"}}

# What the tt and the tarantool in bin_dir print when they run.
BIN_DIR_MESSAGE = "Hello from bin_dir"


def write_manifest(directory) -> None:
    with open(os.path.join(directory, "app.manifest.toml"), "w") as f:
        f.write(MANIFEST)


def write_project(directory) -> None:
    """Make directory a project with the instance launch in its default
    pair."""
    write_manifest(directory)
    os.makedirs(os.path.join(directory, os.path.dirname(instances_file)))
    with open(os.path.join(directory, instances_file), "w") as f:
        yaml.dump({"launch": None}, f)
    with open(os.path.join(directory, cluster_config_file), "w") as f:
        yaml.dump(
            {
                "groups": {
                    "group-001": {
                        "replicasets": {"replicaset-001": {"instances": {"launch": {}}}},
                    },
                },
            },
            f,
        )


def write_bin_dir(directory) -> None:
    """Put a tt and a tarantool into the bin directory of directory, the
    bin_dir of TT_YAML."""
    os.mkdir(os.path.join(directory, "bin"))
    for name in ("tt", "tarantool"):
        path = os.path.join(directory, "bin", name)
        with open(path, "w") as f:
            f.write(f'#!/bin/sh\necho "{BIN_DIR_MESSAGE}"\n')
        os.chmod(path, 0o755)


# ##### #
# Tests #
# ##### #
@pytest.mark.parametrize(
    "flags",
    [["-S"], ["--system"], ["-L", "."], ["--local", "."], ["-c", "tt.yaml"], ["--cfg", "tt.yaml"]],
)
def test_launch_removed_flags(tt_cmd, tmp_path, flags):
    # The flags that chose a tt.yaml are gone along with it.
    rc, stdout, stderr = run_command_and_get_streams([tt_cmd, *flags, "version"], cwd=tmp_path)
    assert rc == 1
    assert stdout == ""
    assert "unknown " in stderr
    assert flags[0].lstrip("-") in stderr


@pytest.mark.parametrize("content", ["[broken", yaml.dump(TT_YAML)])
def test_tt_yaml_is_not_read(tt_cmd, tmp_path, content):
    # A tt.yaml next to a valid project, broken or naming a bin_dir, is not
    # read by any command, and neither is the one TT_CLI_CFG names.
    write_project(tmp_path)
    write_bin_dir(tmp_path)
    for name in ("tt.yaml", "elsewhere.yaml"):
        with open(tmp_path / name, "w") as f:
            f.write(content)

    env = dict(os.environ, TT_CLI_CFG=str(tmp_path / "elsewhere.yaml"))
    for cmd in [
        ["-V", "version"],
        ["run", "--version"],
        ["status"],
        ["completion", "bash"],
        ["registry", "list"],
        ["rocks", "help"],
    ]:
        rc, stdout, stderr = run_command_and_get_streams([tt_cmd, *cmd], cwd=tmp_path, env=env)
        assert rc == 0, cmd
        assert BIN_DIR_MESSAGE not in stdout + stderr, cmd
        assert "tt.yaml" not in stderr, cmd

    rc, stdout, _ = run_command_and_get_streams([tt_cmd, "status"], cwd=tmp_path, env=env)
    assert "launch:launch" in stdout
    assert "NOT RUNNING" in stdout

    rc, stdout, _ = run_command_and_get_streams([tt_cmd, "run", "--version"], cwd=tmp_path)
    assert stdout.startswith("Tarantool ")


@pytest.mark.parametrize("flags", [[], ["-s"], ["--self"]])
def test_launch_does_not_run_tt_of_bin_dir(tt_cmd, tmp_path, flags):
    # tt no longer runs the tt of a tt environment in its place; --self,
    # which kept it from doing so, is still accepted.
    with open(tmp_path / "tt.yaml", "w") as f:
        yaml.dump(TT_YAML, f)
    write_bin_dir(tmp_path)

    rc, output = run_command_and_get_output([tt_cmd, *flags, "version"], cwd=tmp_path)
    assert rc == 0
    assert "Tarantool CLI" in output
    assert BIN_DIR_MESSAGE not in output


def test_external_module_without_internal_implementation(tt_cmd, tmp_path):
    # Create an external module, which don't have internal
    # implementation.
    module = "abc-example"
    module_message = create_external_module(module, tmp_path / "modules")
    env = modules_path_env(tmp_path / "modules")

    cmd = [tt_cmd, module]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0
    assert module_message in output

    # Trying to start external module with -I flag.
    # In this case, tt should ignore this flag and just
    # start module.
    cmd = [tt_cmd, "-I", module]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0
    assert module_message in output


@pytest.mark.parametrize("module", ["cat", "non-exists-module"])
def test_launch_external_cmd_with_flags(tt_cmd, tmp_path, module):
    module_message = create_external_module(module, tmp_path / "modules")

    cmd = [tt_cmd, module, "--non-existent-flag", "-f", "argument1"]
    rc, output = run_command_and_get_output(
        cmd,
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    assert module_message in output


def test_launch_protected_cmd_with_flags(tt_cmd, tmp_path):
    # An external module named version is ignored with a warning: the flags
    # reach tt's own version, which does not know them.
    create_external_module("version", tmp_path / "modules")

    cmd = [tt_cmd, "version", "--non-existent-flag", "-f", "argument1"]
    rc, stdout, stderr = run_command_and_get_streams(
        cmd,
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 1
    assert stdout == ""
    assert stderr.startswith(ignored_module_warning("version", tmp_path / "modules"))
    assert "unknown flag: --non-existent-flag" in stderr
    assert "Hello, I'm version external module!" not in stderr


def test_std_err_stream_unknown_flag(tt_cmd, tmp_path):
    cmd = [tt_cmd, "-L", "non-exists-dir", "version"]
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
    assert "unknown shorthand flag" not in tt_process.stdout.readline()
    assert "unknown shorthand flag" in tt_process.stderr.readline()


def test_local_launch_two_env_modules(tt_cmd, tmp_path):
    """
    Two external modules 'help' and 'version' exist at the same time, in
    two directories TT_CLI_MODULES_PATH lists. Neither command can be
    replaced, so both modules are ignored with a warning and 'tt help
    version' shows tt's own help of 'tt version'.
    Run 'tt' without any config file.
    """
    modules = ("help", "version")

    modules_path = tmp_path / "modules"
    create_external_module(modules[0], modules_path / "modules1")
    create_external_module(modules[1], modules_path / "modules2")

    cmd = [tt_cmd, *modules]

    rc, own_help, _ = run_command_and_get_streams(cmd, env={"TT_CLI_MODULES_PATH": ""})
    assert rc == 0
    assert own_help.startswith("Show Tarantool CLI version information\n")

    rc, stdout, stderr = run_command_and_get_streams(
        cmd,
        env={"TT_CLI_MODULES_PATH": f"{modules_path / 'modules1'}:{modules_path / 'modules2'}"},
    )
    assert rc == 0
    assert stdout == own_help
    assert stderr == ignored_module_warning(
        "help",
        modules_path / "modules1",
    ) + ignored_module_warning("version", modules_path / "modules2")


def test_unknown_command_exit_code(tt_cmd):
    cmd = [tt_cmd, "foo"]
    rc, out = run_command_and_get_output(cmd)

    assert rc != 0
    assert "unknown command" in out


def test_unknown_flag_exit_code(tt_cmd):
    cmd = [tt_cmd, "--unknown-flag"]
    rc, out = run_command_and_get_output(cmd)

    assert rc != 0
    assert "unknown flag" in out
