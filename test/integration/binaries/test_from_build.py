from shutil import rmtree

import pytest

from utils import run_command_and_get_output


@pytest.fixture
def local_build(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    for version in ["2.10.3", "2.10.5"]:
        installation = root / "versions" / "tarantool" / f"tarantool_{version}"
        (installation / "include" / "tarantool").mkdir(parents=True)
        (installation / "tarantool").write_text("#!/bin/sh\nexit 99\n")
        (installation / "tarantool").chmod(0o755)

    # A managed version name in the source path must not identify this build
    # as the installation being removed.
    build_dir = tmp_path / "tarantool_2.10.3" / "local build"
    binary = build_dir / "src" / "tarantool"
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\necho 'Tarantool 3.0.0'\n")
    binary.chmod(0o755)
    headers = build_dir / "tarantool-prefix" / "include" / "tarantool"
    headers.mkdir(parents=True)
    (headers / "module.h").write_text("/* local build headers */\n")
    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "install", "tarantool", "--from-build", build_dir],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert (root / "bin" / "tarantool").readlink() == binary
    assert (root / "include" / "tarantool").readlink() == headers
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

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "--scope", "project", "list"],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert f"tarantool -> {binary} [active]" in output
    assert "2.10.3 [active]" not in output
    assert "tarantool-dev" not in output

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "--scope", "project", *command],
        cwd=tmp_path,
    )
    assert rc == 0, output
    root = tmp_path / ".tarantool"
    installation = root / "versions" / "tarantool" / f"tarantool_{expected_version}"
    assert (root / "bin" / "tarantool").resolve() == installation / "tarantool"
    assert (root / "include" / "tarantool").resolve() == installation / "include" / "tarantool"
    if not broken:
        assert binary.read_text() == "#!/bin/sh\necho 'Tarantool 3.0.0'\n"
        assert (headers / "module.h").read_text() == "/* local build headers */\n"
    else:
        assert not binary.exists()
        assert not headers.exists()


@pytest.mark.parametrize("broken", [False, True])
def test_uninstall_version_keeps_local_build(tt_cmd, tmp_path, local_build, broken):
    binary, headers = local_build
    if broken:
        binary.unlink()
        rmtree(headers)

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "--scope", "project", "uninstall", "tarantool", "2.10.3"],
        cwd=tmp_path,
    )
    assert rc == 0, output
    root = tmp_path / ".tarantool"
    assert not (root / "versions" / "tarantool" / "tarantool_2.10.3").exists()
    assert (root / "bin" / "tarantool").readlink() == binary
    assert (root / "include" / "tarantool").readlink() == headers
    if not broken:
        assert binary.is_file()
        assert (headers / "module.h").is_file()


@pytest.mark.parametrize("with_headers", [False, True])
@pytest.mark.parametrize("broken", [False, True])
def test_uninstall_only_local_build(tt_cmd, tmp_path, local_build, with_headers, broken):
    binary, headers = local_build
    root = tmp_path / ".tarantool"
    rmtree(root / "versions")
    header_link = root / "include" / "tarantool"
    if not with_headers:
        header_link.unlink()
    if broken:
        binary.unlink()
        rmtree(headers)

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "--scope", "project", "uninstall", "tarantool"],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert not (root / "bin" / "tarantool").is_symlink()
    assert not header_link.is_symlink()
    if not broken:
        assert binary.is_file()
        assert (headers / "module.h").is_file()
    else:
        assert not binary.exists()
        assert not headers.exists()
