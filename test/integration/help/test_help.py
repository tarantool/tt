import pytest

from utils import (
    create_external_module,
    ignored_module_warning,
    modules_path_env,
    run_command_and_get_output,
    run_command_and_get_streams,
)


# ##### #
# Tests #
# ##### #
@pytest.mark.parametrize("help_cmd", ["help", "--help", "-h", None])
def test_help_without_external_modules(tt_cmd, help_cmd):
    rc, output = run_command_and_get_output([tt_cmd, help_cmd] if help_cmd else [tt_cmd])
    assert rc == 0
    assert "EXTERNAL COMMANDS" not in output


def test_help_internal_module(tt_cmd, tmp_path):
    module = "version"
    commands = [
        [tt_cmd, "help", module],
        [tt_cmd, module, "--help"],
        [tt_cmd, module, "-h"],
    ]

    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 0
        assert "Show Tarantool CLI version information" in output


def test_external_help_module(tt_cmd, tmp_path):
    # tt help cannot be replaced: an external module named help is ignored
    # with a warning, is not listed, and every way of asking for help shows
    # tt's own help, as tt shows it with no external modules.
    create_external_module("help", tmp_path / "modules")
    env = modules_path_env(tmp_path / "modules")

    commands = [
        [tt_cmd, "help"],
        [tt_cmd, "-h"],
        [tt_cmd, "--help"],
        [tt_cmd, "-I", "help"],
        [tt_cmd],
    ]

    for cmd in commands:
        rc, own_help, _ = run_command_and_get_streams(cmd, cwd=tmp_path, env=modules_path_env())
        assert rc == 0
        assert "EXTERNAL COMMANDS" not in own_help

        rc, stdout, stderr = run_command_and_get_streams(cmd, cwd=tmp_path, env=env)
        assert rc == 0
        assert stdout == own_help
        assert stderr == ignored_module_warning("help", tmp_path / "modules")


def test_internal_help_list_external_commands(tt_cmd, tmp_path):
    # No external help module, but external modules: the list of available
    # external commands is displayed. An external version module is ignored
    # with a warning, and is not listed.
    create_external_module("version", tmp_path / "modules")
    create_external_module("abc", tmp_path / "modules")
    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, "help"],
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    assert "EXTERNAL COMMANDS" in stdout
    assert "abc\tDescription for external module abc" in stdout
    assert "version\tDescription for external module version" not in stdout
    assert stderr == ignored_module_warning("version", tmp_path / "modules")


def test_call_help_for_external_override_module(tt_cmd, tmp_path):
    # The external module 'env' replaces the command, so its help is the
    # module's: the module is called with the --help flag, and its help goes
    # to stdout as any help does.
    create_external_module("env", tmp_path / "modules")
    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, "help", "env"],
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    assert stdout == "Help for external env module\nList of passed args: --help\n"
    assert stderr == ""


def test_call_help_for_protected_command(tt_cmd, tmp_path):
    # The external module 'version' is ignored with a warning: the help of
    # tt version is tt's own.
    create_external_module("version", tmp_path / "modules")
    rc, own_help, _ = run_command_and_get_streams(
        [tt_cmd, "help", "version"],
        cwd=tmp_path,
        env=modules_path_env(),
    )
    assert rc == 0
    assert own_help.startswith("Show Tarantool CLI version information\n")

    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, "help", "version"],
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    assert stdout == own_help
    assert stderr == ignored_module_warning("version", tmp_path / "modules")


def test_call_help_for_external_custom_module(tt_cmd, tmp_path):
    # In this case, the external module version should be called with the --help flag.
    create_external_module("abc", tmp_path / "modules")
    # External modules without internal implementation.
    rc, output = run_command_and_get_output(
        [tt_cmd, "help", "abc"],
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    assert "Help for external abc module\nList of passed args: --help\n" == output


def test_external_help_module_with_args(tt_cmd, tmp_path):
    # If the external modules help and version exist at the same time, both
    # are ignored with a warning: tt help version shows tt's own help of tt
    # version.
    create_external_module("version", tmp_path / "modules")
    create_external_module("help", tmp_path / "modules")
    rc, own_help, _ = run_command_and_get_streams(
        [tt_cmd, "help", "version"],
        cwd=tmp_path,
        env=modules_path_env(),
    )
    assert rc == 0

    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, "help", "version"],
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    assert stdout == own_help
    assert stderr == ignored_module_warning(
        "help",
        tmp_path / "modules",
    ) + ignored_module_warning("version", tmp_path / "modules")
