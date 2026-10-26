import os
import platform
import re
import shutil
import subprocess
import tarfile
from pathlib import Path

import pytest

from utils import create_tt_config, run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.parametrize(
    "program, executable, version, bundle_name, bundle_dir, installed_version",
    [
        (
            "tarantool-ee",
            "tarantool",
            "gc64-3.4.0-0-r60",
            "tarantool-enterprise-sdk-gc64-3.4.0-0-r60.linux.x86_64.tar.gz",
            "tarantool-enterprise",
            "3.4.0",
        ),
        (
            "tcm",
            "tcm",
            "1.3.1-0-g074b5ffa",
            "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
            "",
            "1.3.1-0-g074b5ffa",
        ),
    ],
)
def test_install_local_bundle(
    tt_cmd,
    tmp_path,
    program,
    executable,
    version,
    bundle_name,
    bundle_dir,
    installed_version,
):
    create_tt_config(
        tmp_path,
        {
            "env": {"bin_dir": "binaries", "inc_dir": "headers"},
            "repo": {"distfiles": "local bundles"},
        },
    )
    source = tmp_path / "bundle" / bundle_dir
    source.mkdir(parents=True)
    binary = source / executable
    binary.write_text("#!/bin/sh\necho 'local bundle'\n")
    binary.chmod(0o755)
    if program == "tarantool-ee":
        includes = source / "include" / executable
        includes.mkdir(parents=True)
        (includes / "module.h").write_text("/* bundle headers */\n")

    distfiles = tmp_path / "local bundles"
    distfiles.mkdir()
    with tarfile.open(distfiles / bundle_name, "w:gz") as bundle:
        bundle.add(source, arcname=bundle_dir or ".")

    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "binary",
            "--scope",
            "project",
            "install",
            program.removesuffix("-ee"),
            version,
            "--local-dir",
            str(distfiles),
            "--force",
        ],
        cwd=tmp_path,
    )
    assert rc == 0, output
    installed_name = f"{program}_{installed_version}"
    root = tmp_path / ".tarantool"
    binaries = root / "versions" / program / installed_name
    assert (binaries / executable).read_bytes() == binary.read_bytes()
    assert (root / "bin" / executable).resolve() == binaries / executable
    if program == "tarantool-ee":
        installed_headers = root / "versions" / program / installed_name / "include"
        assert (root / "include" / executable).resolve() == installed_headers / executable
        assert (installed_headers / executable / "module.h").read_text() == "/* bundle headers */\n"


# The real customer-zone tests inherit CI-injected credentials directly in the
# child process. Never read credentials or a developer's credentials file here.
REMOTE_PRODUCTS = [
    ("tarantool-ee", False),
    ("tarantool-ee", True),
    ("tarantool-sdk", False),
    ("tarantool-sdk", True),
    ("tcm", False),
    ("tcm", True),
    ("tt-ee", False),
]


def remote_command(tt_cmd, tmp_path, *args):
    temporary = tmp_path / "temporary"
    temporary.mkdir(exist_ok=True)
    return subprocess.run(
        [
            "env",
            "-u",
            "TT_CLI_CFG",
            f"HOME={tmp_path / 'home'}",
            f"XDG_DATA_HOME={tmp_path / 'user-data'}",
            f"TMPDIR={temporary}",
            tt_cmd,
            "binary",
            "--scope",
            "project",
            *args,
        ],
        cwd=tmp_path,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        timeout=300,
    )


def assert_runtime_version(binary, executable):
    version_args = {
        "tarantool": ["--version"],
        "tcm": ["version"],
        "tt": ["--self", "version"],
    }
    result = subprocess.run(
        [binary, *version_args[executable]],
        cwd=binary.parent,
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert result.returncode == 0, result.stdout + result.stderr


@pytest.mark.slow_ee
@pytest.mark.parametrize("program,development", REMOTE_PRODUCTS)
@pytest.mark.parametrize("keep", [False, True], ids=["cleanup", "no-clean"])
def test_install_remote_enterprise(tt_cmd: Path, tmp_path: Path, program, development, keep):
    system, machine = platform.system(), platform.machine()
    if program == "tt-ee" and (system != "Linux" or machine != "x86_64"):
        pytest.skip("Standalone tt-ee archives are available only on Linux amd64")
    if system not in ("Linux", "Darwin") or machine not in ("x86_64", "aarch64", "arm64"):
        pytest.skip("Customer-zone archives require Linux/macOS on amd64/arm64")
    required = {"TT_CLI_EE_USERNAME", "TT_CLI_EE_PASSWORD"}
    if not required.issubset(set(os.environ)):
        pytest.skip("Real customer-zone tests require credentials injected by CI")
    flags = ["--dev"] if development else []
    result = remote_command(tt_cmd, tmp_path, "search", program, *flags)
    assert result.returncode == 0, result.stdout
    pattern = (
        r"gc64-\d+\.\d+\.\d+\S*"
        if program
        in (
            "tarantool-ee",
            "tarantool-sdk",
        )
        else r"\d+\.\d+\.\d+\S*"
    )
    versions = [
        line.split()[0]
        for line in result.stdout.splitlines()
        if re.fullmatch(pattern, line.split()[0] if line.split() else "")
    ]
    assert versions, result.stdout
    selected = versions[-1]
    semantic_version = re.search(r"\d+\.\d+\.\d+", selected)[0]
    result = remote_command(
        tt_cmd,
        tmp_path,
        "install",
        program.removesuffix("-ee"),
        f"{selected}ee" if program == "tt-ee" else selected,
        "--force",
        *flags,
        *(["--no-clean"] if keep else []),
    )
    assert result.returncode == 0, result.stdout
    assert f"Installing {program}={selected}" in result.stdout
    assert f"Downloading {program}..." in result.stdout
    assert "Done." in result.stdout
    root = tmp_path / ".tarantool"
    if program == "tarantool-sdk":
        components = [
            ("tarantool-ee", "tarantool", f"tarantool_sdk_{selected}"),
            ("tcm", "tcm", f"tcm_sdk_{selected}"),
            ("tt-ee", "tt", f"tt_sdk_{selected}"),
        ]
    else:
        executable = {"tarantool-ee": "tarantool", "tcm": "tcm", "tt-ee": "tt"}[program]
        identity = selected
        if program == "tarantool-ee":
            # Standalone EE omits commit counts, git hashes and SDK revisions,
            # but retains prerelease labels such as rc1.
            identity = re.sub(r"-\d+(?:-g[0-9a-f]+)?-r\d+$", "", selected)
            if int(semantic_version.split(".")[0]) >= 3:
                identity = identity.removeprefix("gc64-")
        components = [(program, executable, f"{program}_{identity}")]
    for product, executable, identity in components:
        installation = root / "versions" / product / identity
        active = root / "bin" / executable
        assert active.is_symlink()
        assert active.resolve(strict=True) == installation / executable
        assert_runtime_version(active, executable)
        assert not (installation / "rocks").exists()
        assert not (installation / "env.sh").exists()
    if program in ("tarantool-ee", "tarantool-sdk"):
        headers = root / "versions" / "tarantool-ee" / components[0][2] / "include" / "tarantool"
        assert (root / "include" / "tarantool").resolve(strict=True) == headers
        assert any(headers.iterdir())
    assert not (root / "rocks").exists()
    assert not (root / "env.sh").exists()
    temporary = tmp_path / "temporary"
    if keep:
        match = re.search(r"Artifacts can be found at: (.+)", result.stdout)
        assert match, result.stdout
        retained = Path(match[1].strip())
        assert retained.parent == temporary
        archives = list(retained.glob("*.tar.gz"))
        assert len(archives) == 1
        assert selected in archives[0].name
        # SDK component versions differ from the archive's version. Verify every
        # installed executable is the one from the selected SDK archive.
        with tarfile.open(archives[0]) as archive:
            members = {
                member.name.removeprefix("./"): member
                for member in archive.getmembers()
                if member.isfile()
            }
            for _, executable, _ in components:
                name = (
                    "tarantool-enterprise/" + executable
                    if program in ("tarantool-ee", "tarantool-sdk")
                    else executable
                )
                if program == "tcm" and name not in members:
                    name = "tarantool-cluster-manager"
                assert name in members, members.keys()
                expected_binary = archive.extractfile(members[name]).read()
                assert (root / "bin" / executable).read_bytes() == expected_binary
        shutil.rmtree(retained)
        for log in temporary.iterdir():
            log.unlink()
    else:
        assert "Artifacts can be found at:" not in result.stdout
        assert not list(temporary.iterdir())
