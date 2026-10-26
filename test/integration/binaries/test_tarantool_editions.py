import tarfile

import pytest
from test_switch import assert_active, install_version, run_interactive_switch

from utils import run_command_and_get_output


@pytest.mark.parametrize(
    "requested",
    ["3ee", "3.8ee", "3.8.0ee", "gc64-3.8.0-0-r94"],
)
def test_install_enterprise_through_tarantool(tt_cmd, tmp_path, requested):
    root = tmp_path / ".tarantool"
    community = install_version(root, "tarantool", "3.8.0")
    source = tmp_path / "archive" / "tarantool-enterprise"
    headers = source / "include" / "tarantool"
    headers.mkdir(parents=True)
    (headers / "module.h").write_text("/* enterprise headers */\n")
    executable = source / "tarantool"
    executable.write_text("#!/bin/sh\nexit 0\n")
    executable.chmod(0o755)
    bundles = tmp_path / "bundles"
    bundles.mkdir()
    archive = bundles / "tarantool-enterprise-sdk-gc64-3.8.0-0-r94.linux.x86_64.tar.gz"
    with tarfile.open(archive, "w:gz") as bundle:
        bundle.add(source, arcname="tarantool-enterprise")

    cmd = [
        tt_cmd,
        "binary",
        "install",
        "tarantool",
        requested,
        "--local-dir",
        str(bundles),
        "--force",
    ]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0, output
    enterprise = root / "versions" / "tarantool-ee" / "tarantool-ee_3.8.0"
    assert_active(root, enterprise, "tarantool-ee")
    assert (community / "tarantool").is_file()
    assert (enterprise / "include" / "tarantool" / "module.h").is_file()


@pytest.mark.parametrize(
    "requested,program,stored",
    [
        ("3.8.0", "tarantool", "3.8.0"),
        ("3.8.0ce", "tarantool", "3.8.0"),
        ("3.8.0ee", "tarantool-ee", "3.8.0"),
        ("gc64-3.8.0-0-r94", "tarantool-ee", "3.8.0"),
        ("abcdefce", "tarantool", "abcdefce"),
        ("sdk-gc64-3.8.0-0-r94", "tarantool-ee", "sdk-gc64-3.8.0-0-r94"),
        ("dev_gc64-3.8.0-0-r94", "tarantool-ee", "dev_gc64-3.8.0-0-r94"),
        ("sdk-dev_gc64-3.8.0-0-r94", "tarantool-ee", "sdk-dev_gc64-3.8.0-0-r94"),
    ],
)
def test_switch_and_uninstall_edition(tt_cmd, tmp_path, requested, program, stored):
    root = tmp_path / ".tarantool"
    community = install_version(root, "tarantool", "3.8.0")
    enterprise = install_version(root, "tarantool-ee", "3.8.0")
    selected = {"tarantool": community, "tarantool-ee": enterprise}[program]
    if stored != "3.8.0":
        selected = install_version(root, program, stored)
    if stored.startswith("sdk-"):
        selected = selected.rename(selected.with_name(f"tarantool_sdk_{stored[4:]}"))
    cmd = [tt_cmd, "binary"]

    rc, output = run_command_and_get_output(
        [*cmd, "switch", "tarantool", requested],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert_active(root, selected, program)

    rc, output = run_command_and_get_output(
        [*cmd, "uninstall", "tarantool", requested],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert not selected.exists()
    other = enterprise if program == "tarantool" else community
    assert (other / "tarantool").is_file()


@pytest.mark.parametrize("family", ["tarantool", "tt"])
@pytest.mark.parametrize("enterprise", [False, True])
@pytest.mark.parametrize("choose_program", [False, True])
def test_switch_selects_edition_then_version(
    tt_cmd, tmp_path, family, enterprise, choose_program,
):
    root = tmp_path / ".tarantool"
    installed = {
        edition: install_version(root, edition, "3.8.0")
        for edition in [family, family + "-ee"]
    }
    program = family + "-ee" if enterprise else family
    selected = installed[program]
    cmd = [tt_cmd, "binary", "switch"]
    answers = []
    if choose_program:
        keys = "\n" if family == "tarantool" else "\x1b[B\n"
        answers.append(("Select program", keys))
    else:
        cmd.append(family)
    keys = "\x1b[B\n" if enterprise else "\n"
    answers.extend([("Select " + family + " edition", keys), ("Select version", "\n")])

    rc, output = run_interactive_switch(cmd, tmp_path, answers)

    assert rc == 0, output
    assert_active(root, selected, program)


def test_switch_displays_full_dev_version(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    dev_version = "dev_gc64-3.8.1-0-r94"
    selected = install_version(root, "tarantool-ee", dev_version)
    rc, output = run_interactive_switch(
        [tt_cmd, "binary", "switch", "tarantool"],
        tmp_path,
        [("Select tarantool edition", "\x1b[B\n"), ("Select version", "\n")],
    )
    assert rc == 0, output
    assert dev_version in output
    assert_active(root, selected, "tarantool-ee")


@pytest.mark.parametrize("enterprise", [False, True])
def test_switch_sdk_only_in_enterprise_menu(tt_cmd, tmp_path, enterprise):
    root = tmp_path / ".tarantool"
    community = install_version(root, "tarantool", "3.8.1")
    install_version(root, "tarantool-ee", "3.8.1")
    sdk_id = "sdk-gc64-3.6.4-0-r93"
    sdk = install_version(root, "tarantool-ee", sdk_id)
    sdk = sdk.rename(sdk.with_name(f"tarantool_sdk_{sdk_id[4:]}"))

    keys = "\x1b[B\n" if enterprise else "\n"
    rc, output = run_interactive_switch(
        [tt_cmd, "binary", "switch", "tarantool"],
        tmp_path,
        [("Select tarantool edition", keys), ("Select version", keys)],
    )

    assert rc == 0, output
    assert (sdk_id in output) == enterprise
    assert_active(root, sdk if enterprise else community, "tarantool")


@pytest.mark.parametrize(
    "requested,program,stored",
    [
        ("2.15.0", "tt", "2.15.0"),
        ("2.15.0ce", "tt", "2.15.0"),
        ("2.15.0ee", "tt-ee", "2.15.0"),
        ("abcdefce", "tt", "abcdefce"),
    ],
)
def test_switch_and_uninstall_tt_edition(tt_cmd, tmp_path, requested, program, stored):
    root = tmp_path / ".tarantool"
    installed = {
        edition: install_version(root, edition, "2.15.0") for edition in ["tt", "tt-ee"]
    }
    selected = installed[program]
    if stored != "2.15.0":
        selected = install_version(root, program, stored)
    cmd = [tt_cmd, "binary"]

    rc, output = run_command_and_get_output([*cmd, "switch", "tt", requested], cwd=tmp_path)
    assert rc == 0, output
    assert_active(root, selected, program)

    rc, output = run_command_and_get_output([*cmd, "uninstall", "tt", requested], cwd=tmp_path)
    assert rc == 0, output
    assert not selected.exists()
    other = "tt" if program == "tt-ee" else "tt-ee"
    assert (installed[other] / "tt").is_file()
