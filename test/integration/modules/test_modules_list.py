from utils import create_external_module, create_tt_config, run_command_and_get_output


def test_show_available_modules(tt_cmd, tmp_path):
    """
    Run 'tt' without args should show available external commands.
    """
    modules = ("ext_cmd1", "ext_cmd2", "ext_cmd3")
    create_tt_config(tmp_path, "modules")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    rc, output = run_command_and_get_output(tt_cmd, cwd=tmp_path)
    assert rc == 0
    assert "EXTERNAL COMMANDS" in output
    for module in modules:
        assert f"{module}\tDescription for external module {module}\n" in output


def test_show_available_modules_with_env(tt_cmd, tmp_path):
    """
    Run 'tt' without args should show available external commands.
    While some modules declared with environment variable TT_CLI_MODULES_PATH.
    """
    ext_modules = ("ext_cmd1", "ext_cmd2", "ext_cmd3")
    int_modules = ("int_cmd1", "int_cmd2", "int_cmd3")
    cfg_dir = tmp_path / "tt"
    create_tt_config(cfg_dir, "modules")

    for module in int_modules:
        create_external_module(module, cfg_dir / "modules")
    for module in ext_modules:
        create_external_module(module, tmp_path / "ext_modules")

    rc, output = run_command_and_get_output(
        tt_cmd,
        cwd=cfg_dir,
        env={"TT_CLI_MODULES_PATH": str(tmp_path / "ext_modules")},
    )
    assert rc == 0
    assert "EXTERNAL COMMANDS" in output
    for module in int_modules + ext_modules:
        assert f"{module}\tDescription for external module {module}\n" in output


def test_show_available_multiple_modules(tt_cmd, tmp_path):
    """
    'tt.yaml' has multiple modules directories, with custom names, not "modules".
    """
    modules1 = ("cmd1", "cmd2", "cmd3")
    modules2 = ("mod1", "mod2", "mod3")
    create_tt_config(tmp_path, ["extra_cmd", "plugins"])

    for module in modules1:
        create_external_module(module, tmp_path / "extra_cmd")
    for module in modules2:
        create_external_module(module, tmp_path / "plugins")

    rc, output = run_command_and_get_output(tt_cmd, cwd=tmp_path)
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
    create_tt_config(tmp_path, "modules")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    cmd = (tt_cmd, "modules", "list")
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
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
    create_tt_config(tmp_path, "modules")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    cmd = (tt_cmd, "modules", "list", "--version")
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
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
    create_tt_config(tmp_path, "modules")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    cmd = (tt_cmd, "modules", "list", "--path")
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
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
    create_tt_config(tmp_path, "modules")

    for module in modules:
        create_external_module(module, tmp_path / "modules")

    cmd = (tt_cmd, "modules", "list", "--version", "--path")
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    expected = ""
    for module in sorted(modules):
        expected += f"0.0.1\t{module} - {tmp_path / 'modules' / module / 'main'}\n"
    assert expected == output
