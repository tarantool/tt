import os

import pytest

from utils import create_tt_config, run_command_and_get_output


@pytest.mark.slow_ee
@pytest.mark.parametrize("development", [False, True])
def test_install_sdk_remote(tt_cmd, tmp_path, development):
    create_tt_config(tmp_path)
    flags = ["--dev"] if development else []
    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "search", "tarantool-sdk", *flags],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0, output
    versions = [line.split()[0] for line in output.splitlines() if line.startswith("gc64-")]
    assert versions, output
    selected = versions[-1]
    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "install", "tarantool-sdk", selected, *flags],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0, output
    root = tmp_path / ".tarantool"
    assert (root / "bin" / "tarantool").is_file()
    assert (root / "bin" / "tcm").is_file()
    assert (root / "bin" / "tt").is_file()
    assert (root / "include" / "tarantool").is_dir()
    assert not (root / "rocks").exists()
    assert not (root / "env.sh").exists()
    for program, executable in (("tarantool-ee", "tarantool"), ("tcm", "tcm"), ("tt-ee", "tt")):
        installation = root / "versions" / program / f"{executable}_sdk_{selected}"
        assert not (installation / "rocks").exists()
        assert not (installation / "env.sh").exists()
