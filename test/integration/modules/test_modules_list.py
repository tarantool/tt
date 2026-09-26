import pytest

from utils import (
    create_external_module,
    create_tt_config,
    ignored_module_warning,
    modules_path_env,
    run_command_and_get_output,
    run_command_and_get_streams,
)


def test_show_available_modules(tt_cmd, tmp_path):
    """
    Run 'tt' without args should show available external commands.
    """
    modules = ("ext_cmd1", "ext_cmd2", "ext_cmd3")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    rc, output = run_command_and_get_output(
        tt_cmd,
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    assert "EXTERNAL COMMANDS" in output
    for module in modules:
        assert f"{module}\tDescription for external module {module}\n" in output


@pytest.mark.parametrize(
    "config, cfg_modules_dir",
    [
        pytest.param({}, "modules", id="default-dir"),
        pytest.param({"modules": {"directory": "cfg_modules"}}, "cfg_modules", id="tt-yaml-dir"),
    ],
)
def test_modules_only_from_env(tt_cmd, tmp_path, config, cfg_modules_dir):
    """
    tt finds external modules only in the directories TT_CLI_MODULES_PATH
    lists: tt.yaml does not name module directories, and the "modules"
    directory next to it is not searched.
    """
    cfg_dir = tmp_path / "tt"
    create_tt_config(cfg_dir, config)
    create_external_module("cfg_cmd", cfg_dir / cfg_modules_dir)
    create_external_module("env_cmd", tmp_path / "env_modules")

    rc, output = run_command_and_get_output(
        (tt_cmd, "modules", "list"),
        cwd=cfg_dir,
        env=modules_path_env(tmp_path / "env_modules"),
    )
    assert rc == 0
    assert output == "env_cmd - Description for external module env_cmd\n"


def test_show_available_multiple_modules(tt_cmd, tmp_path):
    """
    TT_CLI_MODULES_PATH lists several modules directories relative to the
    working directory, with custom names, not "modules".
    """
    modules1 = ("cmd1", "cmd2", "cmd3")
    modules2 = ("mod1", "mod2", "mod3")

    for module in modules1:
        create_external_module(module, tmp_path / "extra_cmd")
    for module in modules2:
        create_external_module(module, tmp_path / "plugins")

    rc, output = run_command_and_get_output(
        tt_cmd,
        cwd=tmp_path,
        env=modules_path_env("extra_cmd", "plugins"),
    )
    assert rc == 0
    assert "EXTERNAL COMMANDS" in output
    for module in modules1 + modules2:
        assert f"{module}\tDescription for external module {module}\n" in output


def test_show_available_multiple_modules_env(tt_cmd, tmp_path):
    """
    Run 'tt' without configured environment.
    TT_CLI_MODULES_PATH has multiple modules directories.
    """
    modules1 = ("cmd1", "cmd2", "cmd3")
    modules2 = ("mod1", "mod2", "mod3")

    for module in modules1:
        create_external_module(module, tmp_path / "extra_cmd")
    for module in modules2:
        create_external_module(module, tmp_path / "plugins")

    rc, output = run_command_and_get_output(
        tt_cmd,
        env={"TT_CLI_MODULES_PATH": f"{tmp_path / 'extra_cmd'}:{tmp_path / 'plugins'}"},
    )
    assert rc == 0
    assert "EXTERNAL COMMANDS" in output
    for module in modules1 + modules2:
        assert f"{module}\tDescription for external module {module}\n" in output


def test_list_available_modules(tt_cmd, tmp_path):
    """
    Run 'tt modules list' - produce sorted list of available external modules.
    """
    modules = ("002_cmd", "004_cmd", "003_cmd", "001_cmd")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    cmd = (tt_cmd, "modules", "list")
    rc, output = run_command_and_get_output(
        cmd,
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    expected = ""
    for module in sorted(modules):
        expected += f"{module} - Description for external module {module}\n"
    assert expected == output


def test_list_available_modules_version(tt_cmd, tmp_path):
    """
    Run 'tt modules list --version' - produce sorted list of available
    external modules with version info.
    """
    modules = ("002_cmd", "004_cmd", "003_cmd", "001_cmd")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    cmd = (tt_cmd, "modules", "list", "--version")
    rc, output = run_command_and_get_output(
        cmd,
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    expected = ""
    for module in sorted(modules):
        expected += f"0.0.1\t{module} - Description for external module {module}\n"
    assert expected == output


def test_list_available_modules_path(tt_cmd, tmp_path):
    """
    Run 'tt modules list --path' - produce sorted list of available
    external modules with path up to executable entry point instead description.
    """
    modules = ("002_cmd", "004_cmd", "003_cmd", "001_cmd")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    cmd = (tt_cmd, "modules", "list", "--path")
    rc, output = run_command_and_get_output(
        cmd,
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    expected = ""
    for module in sorted(modules):
        expected += f"{module} - {tmp_path / 'modules' / module / 'main'}\n"
    assert expected == output


def test_list_available_modules_version_and_path(tt_cmd, tmp_path):
    """
    Run 'tt modules list --version --path' - produce sorted list of available
    external modules with with version info and path up to executable entry point.
    """
    modules = ("002_cmd", "004_cmd", "003_cmd", "001_cmd")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    cmd = (tt_cmd, "modules", "list", "--version", "--path")
    rc, output = run_command_and_get_output(
        cmd,
        cwd=tmp_path,
        env=modules_path_env(tmp_path / "modules"),
    )
    assert rc == 0
    expected = ""
    for module in sorted(modules):
        expected += f"0.0.1\t{module} - {tmp_path / 'modules' / module / 'main'}\n"
    assert expected == output


@pytest.mark.parametrize(
    "module, args",
    [
        pytest.param("env", ["--flag", "arg"], id="command"),
        pytest.param("cluster", ["show", "app", "--flag"], id="group"),
    ],
)
def test_module_replaces_builtin_command(tt_cmd, tmp_path, module, args):
    """
    An external module named like a builtin command takes its place, with no
    warning: every argument reaches the module, a subcommand of a group
    included. With -I the builtin command runs.
    """
    modules_dir = tmp_path / "modules"
    module_message = create_external_module(module, modules_dir)
    env = modules_path_env(modules_dir)

    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, module, *args],
        cwd=tmp_path,
        env=env,
    )
    assert rc == 0
    assert stdout == f"{module_message}\nList of passed args: {' '.join(args)}\n"
    assert stderr == ""

    rc, own_help, _ = run_command_and_get_streams(
        [tt_cmd, module, "--help"],
        cwd=tmp_path,
        env=modules_path_env(),
    )
    assert rc == 0

    rc, stdout, stderr = run_command_and_get_streams(
        [tt_cmd, "-I", module, "--help"],
        cwd=tmp_path,
        env=env,
    )
    assert rc == 0
    assert stdout == own_help
    assert stderr == ""


def test_module_named_modules_is_ignored(tt_cmd, tmp_path):
    """
    tt modules cannot be replaced: an external module named modules is
    ignored with a warning, is not listed, and tt keeps working.
    """
    modules_dir = tmp_path / "modules"
    create_external_module("modules", modules_dir)
    create_external_module("abc", modules_dir)

    rc, stdout, stderr = run_command_and_get_streams(
        (tt_cmd, "modules", "list"),
        cwd=tmp_path,
        env=modules_path_env(modules_dir),
    )
    assert rc == 0
    assert stdout == "abc - Description for external module abc\n"
    assert stderr == ignored_module_warning("modules", modules_dir)
