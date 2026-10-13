#!/usr/bin/env python3
import tarfile
from pathlib import Path
from shutil import copy2, copytree

import pytest

from utils import run_command_and_get_output

TestDataDir = Path(__file__).parent / "testdata"


def prepare_testdata(program: str, case: str, tmp_path: Path) -> Path:
    """Translate legacy test data into a scope without changing shared fixtures."""
    source = TestDataDir / case / program
    work_dir = tmp_path / program
    root = work_dir / ".tarantool"
    for binary in (source / "bin").iterdir():
        if binary.is_symlink():
            continue
        product, version = binary.name.split("_", 1)
        executable = "tarantool" if product == "tarantool-ee" else product
        installation = root / "versions" / product / f"{product}_{version}"
        installation.mkdir(parents=True)
        copy2(binary, installation / executable)
        if executable == "tarantool":
            copytree(
                source / "include" / "include" / binary.name,
                installation / "inc" / "tarantool",
            )

    for link in (source / "bin").iterdir():
        if not link.is_symlink():
            continue
        product = link.resolve().name.split("_", 1)[0]
        installation = root / "versions" / product / link.resolve().name
        (root / "bin").mkdir(exist_ok=True)
        (root / "bin" / link.name).symlink_to(installation / link.name)
        if link.name == "tarantool":
            (root / "inc").mkdir()
            (root / "inc" / link.name).symlink_to(installation / "inc" / link.name)
    return work_dir


def assert_no_extra_in_rest_files(root: Path, rest_files: dict[str, str]) -> None:
    """Check remaining installations and the full targets of active links."""
    installations = {
        installation.name: installation
        for product in (root / "versions").iterdir()
        for installation in product.iterdir()
    }
    actual = dict.fromkeys(installations, "")
    for link in (root / "bin").iterdir():
        assert link.is_symlink()
        target = link.resolve()
        assert target == installations[target.parent.name] / link.name
        actual[link.name] = target.parent.name
        if link.name == "tarantool":
            assert (root / "inc" / link.name).resolve() == target.parent / "inc" / link.name
    assert actual == rest_files
    for installation in installations.values():
        if installation.parent.name in {"tarantool", "tarantool-ee"}:
            assert (installation / "tarantool").is_file()
            assert (installation / "inc" / "tarantool").is_dir()


@pytest.mark.parametrize(
    "program, ver_remove, rest_files",
    [
        (
            "tcm",
            "1.3.1-0-g074b5ffa",
            {
                "tcm": "tcm_1.2.0-11-g2d0a0f495",
                "tcm_1.2.0-11-g2d0a0f495": "",
            },
        ),
        (
            "tarantool-ee",
            "gc64-2.11.6-0-r683",
            {
                "tarantool": "tarantool-ee_gc64-3.4.0-0-r60",
                "tarantool-ee_gc64-3.4.0-0-r60": "",
            },
        ),
    ],
)
def test_uninstall_active_version(
    tt_cmd: Path,
    tmp_path: Path,
    program: str,
    ver_remove: str,
    rest_files: dict[str, str],
) -> None:
    """
    Verify that the active version is removed.
    For case with multiple installed versions.
    """
    wort_dir = prepare_testdata(program, "2vers_installed", tmp_path)
    cmd = [
        tt_cmd,
        "--self",
        "-V",
        "binary",
        "--scope",
        "project",
        "uninstall",
        program,
        ver_remove,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 0
    assert "• Removing binary..." in output
    assert "• Changing symlinks..." in output
    assert f"• {program}={ver_remove} is uninstalled." in output
    assert_no_extra_in_rest_files(wort_dir / ".tarantool", rest_files)


@pytest.mark.parametrize(
    "program, ver_remove, rest_files",
    [
        (
            "tcm",
            "1.2.0-11-g2d0a0f495",
            {
                "tcm": "tcm_1.3.1-0-g074b5ffa",
                "tcm_1.3.1-0-g074b5ffa": "",
                "tcm_2d0a0f495": "",
            },
        ),
        (
            "tarantool-ee",
            "gc64-3.4.0-0-r60",
            {
                "tarantool": "tarantool-ee_gc64-2.11.6-0-r683",
                "tarantool-ee_gc64-2.11.6-0-r683": "",
                "tarantool_3.5.0": "",
            },
        ),
    ],
)
def test_uninstall_inactive_version(
    tt_cmd: Path,
    tmp_path: Path,
    program: str,
    ver_remove: str,
    rest_files: dict[str, str],
) -> None:
    """
    Verify that the inactive version is removed.
    For case with multiple installed versions.
    Active version should be same after uninstalling.
    """
    wort_dir = prepare_testdata(program, "3vers_installed", tmp_path)
    cmd = [
        tt_cmd,
        "--self",
        "-V",
        "binary",
        "--scope",
        "project",
        "uninstall",
        program,
        ver_remove,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 0
    assert "• Removing binary..." in output
    assert "• Changing symlinks..." not in output
    assert f"• {program}={ver_remove} is uninstalled." in output
    assert_no_extra_in_rest_files(wort_dir / ".tarantool", rest_files)


@pytest.mark.parametrize(
    "program, ver_remove, rest_files",
    [
        (
            "tcm",
            "1.3.1-0-g074b5ffa",
            {
                "tcm": "tcm_1.2.0-11-g2d0a0f495",
                "tcm_2d0a0f495": "",
                "tcm_1.2.0-11-g2d0a0f495": "",
            },
        ),
        (
            "tarantool-ee",
            "gc64-2.11.6-0-r683",
            {
                "tarantool": "tarantool_3.5.0",
                "tarantool_3.5.0": "",
                "tarantool-ee_gc64-3.4.0-0-r60": "",
            },
        ),
    ],
)
def test_uninstall_switch_version(
    tt_cmd: Path,
    tmp_path: Path,
    program: str,
    ver_remove: str,
    rest_files: dict[str, str],
) -> None:
    """
    Verify that the active version is removed.
    For case with multiple installed versions.
    """
    wort_dir = prepare_testdata(program, "3vers_installed", tmp_path)
    cmd = [
        tt_cmd,
        "--self",
        "-V",
        "binary",
        "--scope",
        "project",
        "uninstall",
        program,
        ver_remove,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 0
    assert "• Removing binary..." in output
    assert "• Changing symlinks..." in output
    assert f"• {program}={ver_remove} is uninstalled." in output
    assert_no_extra_in_rest_files(wort_dir / ".tarantool", rest_files)


@pytest.mark.parametrize(
    "program",
    ["tcm", "tarantool-ee"],
)
def test_uninstall_no_version(tt_cmd: Path, tmp_path: Path, program: str) -> None:
    """
    If multiple versions are installed and no version to uninstall is specified,
    the `uninstall` command should generate an error.
    """
    wort_dir = prepare_testdata(program, "2vers_installed", tmp_path)

    cmd = [
        tt_cmd,
        "--self",
        "binary",
        "--scope",
        "project",
        "uninstall",
        program,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 1
    assert (
        f"⨯ {program} has more than one installed version, please specify the version to uninstall"
    ) in output


@pytest.mark.parametrize(
    "program",
    ["tcm", "tarantool-ee"],
)
def test_uninstall_no_active(tt_cmd: Path, tmp_path: Path, program: str) -> None:
    """
    Check that if there is no active version and there are several installed versions,
    the `uninstall` command will generate an error.
    """
    wort_dir = prepare_testdata(program, "2vers_installed_no_active", tmp_path)

    cmd = [
        tt_cmd,
        "--self",
        "-V",
        "binary",
        "--scope",
        "project",
        "uninstall",
        program,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 1
    assert (
        f"⨯ {program} has more than one installed version, please specify the version to uninstall"
    ) in output


@pytest.mark.parametrize(
    "program, executable, version, bundle_name, bundle_dir",
    [
        (
            "tarantool-ee",
            "tarantool",
            "gc64-3.4.0-0-r60",
            "tarantool-enterprise-sdk-gc64-3.4.0-0-r60.linux.x86_64.tar.gz",
            "tarantool-enterprise",
        ),
        ("tcm", "tcm", "1.3.1-0-g074b5ffa", "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz", ""),
    ],
)
def test_uninstall_installed_local_bundle(
    tt_cmd,
    tmp_path,
    program,
    executable,
    version,
    bundle_name,
    bundle_dir,
):
    source = tmp_path / "source" / bundle_dir
    source.mkdir(parents=True)
    binary = source / executable
    binary.write_text("#!/bin/sh\necho 'local bundle'\n")
    binary.chmod(0o755)
    if executable == "tarantool":
        includes = source / "include" / executable
        includes.mkdir(parents=True)
        (includes / "module.h").write_text("/* bundle headers */\n")
    repository = tmp_path / "local bundles"
    repository.mkdir()
    archive = repository / bundle_name
    with tarfile.open(archive, "w:gz") as bundle:
        bundle.add(source, arcname=bundle_dir or ".")

    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            program,
            version,
            "--local-dir",
            repository,
            "--force",
        ],
        cwd=tmp_path,
    )
    assert rc == 0, output
    root = tmp_path / ".tarantool"
    installation = root / "versions" / program / f"{program}_{version}"
    assert (installation / executable).read_bytes() == binary.read_bytes()
    assert (root / "bin" / executable).resolve() == installation / executable
    if executable == "tarantool":
        assert (installation / "inc" / "tarantool" / "module.h").is_file()

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "uninstall", program],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert f"{program}={version} is uninstalled." in output
    assert not installation.exists()
    assert not (root / "bin" / executable).is_symlink()
    assert not (root / "inc" / executable).is_symlink()
    assert archive.is_file()
    assert binary.is_file()
