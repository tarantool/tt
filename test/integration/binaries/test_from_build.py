from pathlib import Path
from shutil import copytree, rmtree

import pytest

from utils import run_command_and_get_output

DATA_DIR = Path(__file__).parent / "testdata"


@pytest.fixture
def local_build(tt_cmd, tmp_path):
    copytree(DATA_DIR / "no_active", tmp_path, symlinks=True, dirs_exist_ok=True)

    # Include an installed version in the source path: it must never make
    # uninstalling that version remove the links to this unrelated build.
    build_dir = tmp_path / "tarantool_2.10.3" / "local build"
    binary = build_dir / "src" / "tarantool"
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\necho 'Tarantool 3.0.0'\n")
    binary.chmod(0o755)
    headers = build_dir / "tarantool-prefix" / "include" / "tarantool"
    headers.mkdir(parents=True)
    (headers / "module.h").write_text("/* local build headers */\n")

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "install", "tarantool", "--from-build", build_dir.relative_to(tmp_path)],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert (tmp_path / "bin" / "tarantool").readlink() == binary
    assert (tmp_path / "inc" / "include" / "tarantool").readlink() == headers
    return binary, headers


@pytest.mark.parametrize("broken", [False, True])
@pytest.mark.parametrize(
    "command, expected_version",
    [
        pytest.param(["switch", "tarantool", "2.10.3"], "2.10.3", id="switch"),
        pytest.param(["uninstall", "tarantool"], "2.10.5", id="uninstall"),
    ],
)
def test_from_build_lifecycle(tt_cmd, tmp_path, local_build, command, expected_version, broken):
    binary, headers = local_build
    if broken:
        binary.unlink()
        rmtree(headers)

    rc, output = run_command_and_get_output([tt_cmd, "binary", "list"], cwd=tmp_path)
    assert rc == 0, output
    assert f"tarantool -> {binary} [active]" in output
    assert "2.10.3 [active]" not in output
    assert "tarantool-dev" not in output

    rc, output = run_command_and_get_output([tt_cmd, "binary", *command], cwd=tmp_path)
    assert rc == 0, output
    installed_name = f"tarantool_{expected_version}"
    assert (tmp_path / "bin" / "tarantool").resolve() == tmp_path / "bin" / installed_name
    includes = tmp_path / "inc" / "include"
    assert (includes / "tarantool").resolve() == includes / installed_name
    if not broken:
        assert binary.read_text() == "#!/bin/sh\necho 'Tarantool 3.0.0'\n"
        assert (headers / "module.h").read_text() == "/* local build headers */\n"


@pytest.mark.parametrize("broken", [False, True])
def test_uninstall_version_keeps_local_build(tt_cmd, tmp_path, local_build, broken):
    binary, headers = local_build
    if broken:
        binary.unlink()
        rmtree(headers)

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "uninstall", "tarantool", "2.10.3"],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert not (tmp_path / "bin" / "tarantool_2.10.3").exists()
    assert not (tmp_path / "inc" / "include" / "tarantool_2.10.3").exists()
    assert (tmp_path / "bin" / "tarantool").readlink() == binary
    assert (tmp_path / "inc" / "include" / "tarantool").readlink() == headers
    if not broken:
        assert binary.is_file()
        assert (headers / "module.h").is_file()


@pytest.mark.parametrize("with_headers", [False, True])
def test_uninstall_only_local_build(tt_cmd, tmp_path, local_build, with_headers):
    binary, headers = local_build
    for installed in (tmp_path / "bin").glob("tarantool_*"):
        installed.unlink()
    for installed in (tmp_path / "inc" / "include").glob("tarantool_*"):
        rmtree(installed)
    header_link = tmp_path / "inc" / "include" / "tarantool"
    if not with_headers:
        header_link.unlink()

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "uninstall", "tarantool"],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert not (tmp_path / "bin" / "tarantool").is_symlink()
    assert not header_link.is_symlink()
    assert binary.is_file()
    assert (headers / "module.h").is_file()
