import os
import platform
import re
from pathlib import Path

import pytest
from test_install_bundle import EE_VERSION, TCM_VERSION, make_bundle
from test_install_sdk import SDK_VERSION, TT_EE_VERSION, make_sdk, make_tt_ee

from utils import run_command_and_get_output


@pytest.fixture(autouse=True)
def isolated_environment(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path / "user data"))
    monkeypatch.delenv("TT_CLI_CFG", raising=False)
    # Never use the caller's Customer Zone credentials in these tests.
    monkeypatch.delenv("TT_CLI_EE_USERNAME", raising=False)
    monkeypatch.delenv("TT_CLI_EE_PASSWORD", raising=False)


def project_snapshot(directory):
    snapshot = {}
    for path in directory.rglob("*"):
        key = str(path.relative_to(directory))
        if path.is_symlink():
            snapshot[key] = ("link", os.readlink(path))
        elif path.is_file():
            snapshot[key] = ("file", path.read_bytes(), path.stat().st_mode)
        else:
            snapshot[key] = ("directory",)
    return snapshot


def seed_active_binary(tmp_path):
    root = tmp_path / ".tarantool"
    binary = root / "versions/tt/tt_v2.15.0/tt"
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\necho 2.15.0.abcdef0\n")
    binary.chmod(0o755)
    (root / "bin").mkdir()
    (root / "bin/tt").symlink_to(binary)


def invalid_install(tt_cmd, tmp_path, args, error):
    seed_active_binary(tmp_path)
    before = project_snapshot(tmp_path)
    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "--scope", "project", "install", *args],
        cwd=tmp_path,
    )
    assert rc != 0, output
    assert error in output
    assert project_snapshot(tmp_path) == before


@pytest.mark.parametrize("program", ["tt", "tarantool"])
@pytest.mark.parametrize(
    "flag,value", [("local-dir", "local bundles"), ("credentials-file", "credentials")],
)
@pytest.mark.parametrize("selector", ["3.4.0", "3.4.0ce"])
def test_community_rejects_enterprise_flags(tt_cmd, tmp_path, program, flag, value, selector):
    invalid_install(
        tt_cmd, tmp_path, [program, selector, f"--{flag}", value], f"--{flag} is not supported",
    )


def test_community_tarantool_rejects_dev(tt_cmd, tmp_path):
    invalid_install(tt_cmd, tmp_path, ["tarantool", "3.4.0", "--dev"], "--dev is not supported")


@pytest.mark.parametrize(
    "program,selector,flag,value",
    [
        (program, selector, flag, value)
        for program, selector in (("tt", "2.15ee"), ("tarantool", "3.4ee"))
        for flag, value in (("local-repo", "source files"), ("source-build", None))
    ]
    + [("tarantool", "3.4ee", flag, None) for flag in ("dynamic", "use-docker")],
)
def test_enterprise_rejects_community_flags(tt_cmd, tmp_path, program, selector, flag, value):
    args = [program, selector, f"--{flag}"]
    if value is not None:
        args.append(value)
    invalid_install(tt_cmd, tmp_path, args, f"--{flag} is not supported")


@pytest.mark.parametrize(
    "flag,value",
    [
        ("local-dir", "local bundles"),
        ("dev", None),
        ("credentials-file", "credentials"),
        ("use-docker", None),
        ("dynamic", None),
        ("source-build", None),
    ],
)
def test_from_build_rejects_incompatible_flags(tt_cmd, tmp_path, flag, value):
    args = ["tarantool", "--from-build", "build directory", f"--{flag}"]
    if value is not None:
        args.append(value)
    invalid_install(tt_cmd, tmp_path, args, f"group [from-build {flag}]")


@pytest.mark.parametrize("program,selector", [("tt", "2.15ee"), ("tarantool", "3.4ee")])
def test_local_sources_are_mutually_exclusive(tt_cmd, tmp_path, program, selector):
    # Edition validation rejects the source conflict before Cobra checks flag groups.
    invalid_install(
        tt_cmd,
        tmp_path,
        [program, selector, "--local-repo", "source files", "--local-dir", "local bundles"],
        "--local-repo is not supported",
    )


@pytest.mark.parametrize(
    "program,selector,flag",
    [("tt", "2.15ee", flag) for flag in ("dev", "from-build")]
    + [
        (program, "3.4", flag)
        for program in ("tcm", "sdk")
        for flag in ("source-build", "local-repo", "from-build", "dynamic", "use-docker")
    ],
)
def test_bundle_products_reject_unknown_source_flags(tt_cmd, tmp_path, program, selector, flag):
    invalid_install(tt_cmd, tmp_path, [program, selector, f"--{flag}"], f"unknown flag: --{flag}")


@pytest.mark.parametrize(
    "program,selector",
    [
        ("tt", "2.15"),
        ("tt", "2.15ee"),
        ("tarantool", "3.4"),
        ("tarantool", "3.4ee"),
        ("tcm", "1.3"),
        ("sdk", "3.7"),
    ],
)
def test_install_rejects_extra_arguments(tt_cmd, tmp_path, program, selector):
    invalid_install(
        tt_cmd, tmp_path, [program, selector, "extra"], "accepts at most 1 arg(s), received 2",
    )


@pytest.mark.parametrize("program", ["tt-ee", "tarantool-ee"])
def test_install_rejects_legacy_products(tt_cmd, tmp_path, program):
    invalid_install(tt_cmd, tmp_path, [program, "3.4.0"], f'unknown command "{program}"')


def test_install_rejects_invalid_scope(tt_cmd, tmp_path):
    invalid_install(tt_cmd, tmp_path, ["tt", "--scope", "invalid"], 'unknown scope "invalid"')


@pytest.mark.parametrize(
    "program,selector",
    [("tarantool", "3.4ee"), ("tt", "2.15ee"), ("tcm", "1.3"), ("sdk", "3.7")],
)
@pytest.mark.parametrize(
    "failure,content,mode,error",
    [
        ("missing", None, 0o600, "opening the credentials file"),
        ("permissions", "dummy-user\ndummy-password\n", 0o644, "are too open"),
        ("username", "\ndummy-password\n", 0o600, "login not set"),
        ("password", "dummy-user\n", 0o600, "password not set"),
    ],
)
def test_invalid_credentials_file_changes_nothing(
    tt_cmd,
    tmp_path,
    program,
    selector,
    failure,
    content,
    mode,
    error,
):
    if program == "tt" and (
        platform.system() != "Linux" or platform.machine() != "x86_64"
    ):
        pytest.skip("Standalone tt-ee archives are available only on Linux amd64")
    credentials = tmp_path / f"dummy {failure} credentials"
    if content is not None:
        credentials.write_text(content)
        credentials.chmod(mode)
    invalid_install(
        tt_cmd, tmp_path, [program, selector, "--credentials-file", str(credentials)], error,
    )


PRODUCTS = [
    ("tarantool-ee", EE_VERSION, "tarantool", EE_VERSION),
    ("tcm", TCM_VERSION, "tcm", TCM_VERSION),
    ("sdk", SDK_VERSION, "sdk", SDK_VERSION),
    ("tt-ee", TT_EE_VERSION, "tt", f"{TT_EE_VERSION}ee"),
]


def local_archive(tmp_path, program, version):
    if program == "sdk":
        return make_sdk(tmp_path, version)
    if program == "tt-ee":
        return make_tt_ee(tmp_path, version)
    return make_bundle(tmp_path, program, version)


@pytest.mark.parametrize("program,version,command,selector", PRODUCTS)
@pytest.mark.parametrize("absolute", [False, True])
def test_local_archive_needs_no_credentials(
    tt_cmd, tmp_path, program, version, command, selector, absolute,
):
    local_archive(tmp_path, program, version)
    repository = tmp_path / "local bundles" if absolute else "local bundles"
    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "binary",
            "--scope",
            "project",
            "install",
            command,
            selector,
            "--local-dir",
            str(repository),
            "--credentials-file",
            "nonexistent dummy credentials",
        ],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert (
        tmp_path
        / ".tarantool/bin"
        / ("tt" if program == "tt-ee" else "tcm" if program == "tcm" else "tarantool")
    ).is_file()
    assert "Signing in" not in output


@pytest.mark.parametrize("program,version,command,selector", PRODUCTS)
@pytest.mark.parametrize("keep", [False, True])
@pytest.mark.parametrize("failure", [False, True])
def test_local_bundle_temporary_artifacts(
    tt_cmd, tmp_path, monkeypatch, program, version, command, selector, keep, failure,
):
    archive = local_archive(tmp_path, program, version)
    if failure:
        archive.write_bytes(b"invalid archive")
    original = archive.read_bytes()
    temporary = tmp_path / "temporary files"
    temporary.mkdir()
    monkeypatch.setenv("TMPDIR", str(temporary))
    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "binary",
            "--scope",
            "project",
            "install",
            command,
            selector,
            "--local-dir",
            "local bundles",
            *(["--no-clean"] if keep else []),
        ],
        cwd=tmp_path,
    )
    assert (rc != 0) == failure, output
    match = re.search(r"Starting installation steps in (.+?)\.\.\.", output)
    assert match, output
    directory = Path(match.group(1))
    assert directory.parent == temporary
    assert archive.read_bytes() == original
    if keep:
        assert f"Artifacts can be found at: {directory}" in output
        assert directory.is_dir()
        assert (directory / archive.name).read_bytes() == original
    else:
        assert "Artifacts can be found at:" not in output
        assert not directory.exists()
    if failure:
        assert not (tmp_path / ".tarantool").exists()
        assert not (tmp_path / ".env").exists()
