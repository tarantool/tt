import pytest
from utils import (create_external_module, create_tt_config,
                   run_command_and_get_output)


# ##### #
# Tests #
# ##### #
@pytest.mark.parametrize("help_cmd", ["help", "--help", "-h", ""])
def test_help_without_external_modules(tt_cmd, help_cmd):
    rc, output = run_command_and_get_output([tt_cmd, help_cmd])
    assert rc == 0
    assert "No available external commands" in output


def test_external_help_module(tt_cmd, tmpdir):
    create_tt_config(tmpdir, tmpdir)
    module_message = create_external_module("help", tmpdir)

    rc, output = run_command_and_get_output([tt_cmd, "help"], cwd=tmpdir)
    assert rc == 0
    assert f"{module_message}\nList of passed args:\n" == output

    # In the cases below, external help shouldn't be called.
    # Should call internal help module and show a list of
    # available external modules.
    commands = [
        [tt_cmd, "-h"],
        [tt_cmd, "--help"],
        [tt_cmd, "help", "-I"],
        [tt_cmd],
    ]

    for cmd in commands:
        rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 0
        assert "help\tDescription for external module help" in output


def test_internal_help_with_external_module(tt_cmd, tmpdir):
    create_tt_config(tmpdir, tmpdir)
    module_message = create_external_module("version", tmpdir)

    # No external help module, but external version module.
    # List of available external commands should be displayed.
    rc, output = run_command_and_get_output([tt_cmd, "help"], cwd=tmpdir)
    assert rc == 0
    assert "version\tDescription for external module version" in output

    # In this case, the external module version should be called with the --help flag.
    rc, output = run_command_and_get_output([tt_cmd, "help", "version"], cwd=tmpdir)
    assert rc == 0
    assert "Help for external version module\nList of passed args: --help\n" == output

    create_external_module("abc", tmpdir)
    # External modules without internal implementation.
    rc, output = run_command_and_get_output([tt_cmd, "help", "abc"], cwd=tmpdir)
    assert rc == 0
    assert "Help for external abc module\nList of passed args: --help\n" == output

    # If the external module help and version exist at the same time,
    # then the external module help should be called with the <version>
    # argument. For example, execute "path/to/external/help version" command.
    module_message = create_external_module("help", tmpdir)
    rc, output = run_command_and_get_output([tt_cmd, "help", "version"], cwd=tmpdir)
    assert rc == 0
    assert f"{module_message}\nList of passed args: version\n" == output
