import pytest

from utils import create_external_module, create_tt_config, run_command_and_get_output


# ##### #
# Tests #
# ##### #
@pytest.mark.parametrize("help_cmd", ["help", "--help", "-h", ""])
def test_help_without_external_modules(tt_cmd, help_cmd):
    rc, output = run_command_and_get_output([tt_cmd, help_cmd])
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
    create_tt_config(tmp_path, "modules")
    module_message = create_external_module("help", tmp_path / "modules")

    rc, output = run_command_and_get_output([tt_cmd, "help"], cwd=tmp_path)
    assert rc == 0
    assert f"{module_message}\nList of passed args:\n" == output

    # In the cases below, external help shouldn't be called.
    # Should call internal help module and show a list of
    # available external modules.
    commands = [
        [tt_cmd, "-h"],
        [tt_cmd, "--help"],
        [tt_cmd, "-I", "help"],
        [tt_cmd],
    ]

    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 0
        assert "help\tDescription for external module help" in output


def test_internal_help_list_external_commands(tt_cmd, tmp_path):
    # No external help module, but external version module.
    # List of available external commands should be displayed.
    create_tt_config(tmp_path, "modules")
    create_external_module("version", tmp_path / "modules")
    rc, output = run_command_and_get_output([tt_cmd, "help"], cwd=tmp_path)
    assert rc == 0
    assert "EXTERNAL COMMANDS" in output
    assert "version\tDescription for external module version" in output


def test_call_help_for_external_override_module(tt_cmd, tmp_path):
    # In this case, the external module 'version' should be called with the --help flag.
    create_tt_config(tmp_path, "modules")
    create_external_module("version", tmp_path / "modules")
    rc, output = run_command_and_get_output([tt_cmd, "help", "version"], cwd=tmp_path)
    assert rc == 0
    assert "Help for external version module\nList of passed args: --help\n" == output


def test_call_help_for_external_custom_module(tt_cmd, tmp_path):
    # In this case, the external module version should be called with the --help flag.
    create_tt_config(tmp_path, "modules")
    create_external_module("abc", tmp_path / "modules")
    # External modules without internal implementation.
    rc, output = run_command_and_get_output([tt_cmd, "help", "abc"], cwd=tmp_path)
    assert rc == 0
    assert "Help for external abc module\nList of passed args: --help\n" == output


def test_external_help_module_with_args(tt_cmd, tmp_path):
    # If the external module help and version exist at the same time,
    # then the external module help should be called with the <version>
    # argument. For example, execute "path/to/external/help version" command.
    create_tt_config(tmp_path, "modules")
    create_external_module("version", tmp_path / "modules")
    module_message = create_external_module("help", tmp_path / "modules")
    rc, output = run_command_and_get_output([tt_cmd, "help", "version"], cwd=tmp_path)
    assert rc == 0
    assert f"{module_message}\nList of passed args: version\n" == output
