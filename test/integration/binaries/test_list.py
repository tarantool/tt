import re
from pathlib import Path
from shutil import copyfile, copytree

from utils import config_name, run_command_and_get_output

DATA_DIR = Path(__file__).parent / "testdata"
ANSI_ESCAPE = re.compile(r"\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])")


def cleanup_ansi(text):
    return ANSI_ESCAPE.sub("", text)


def compare_output(actual: str, expected: str) -> bool:
    """
    Compare the output of the command with the expected output.
    """
    # Cleaning lines. Remove leading, trailing whitespace and ANSI escape codes.
    got_lines = [cleanup_ansi(line.strip()) for line in actual.splitlines()]
    expected_lines = [line.strip() for line in expected.splitlines()]

    return got_lines == expected_lines


def test_list(tt_cmd, tmp_path):
    copytree(DATA_DIR / "list", tmp_path, symlinks=True, dirs_exist_ok=True)

    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    expected_output = """List of installed binaries:
   • tt:
    0.1.0
   • tarantool:
    2.10.3
    2.8.1 [active]
   • tcm:
    1.3.1-0-g074b5ffa [active]
    1.2.0-11-g2d0a0f495
"""
    assert rc == 0
    assert compare_output(output, expected_output)


def test_list_no_directory(tt_cmd, tmp_path):
    # Create test config
    copyfile(DATA_DIR / "list" / config_name, tmp_path / config_name)

    # Print binaries
    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    assert rc == 1
    assert "there are no binaries installed in this environment of 'tt'" in output


def test_list_empty_directory(tt_cmd, tmp_path):
    # Create test config and empty bin directory.
    copyfile(DATA_DIR / "list" / config_name, tmp_path / config_name)
    (tmp_path / "bin").mkdir()

    # Print binaries
    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    assert rc == 1
    assert "there are no binaries installed in this environment of 'tt'" in output


def test_list_tarantool_dev(tt_cmd, tmp_path):
    # Copy the test dir to the "run" directory.
    copytree(DATA_DIR / "tarantool_dev", tmp_path, symlinks=True, dirs_exist_ok=True)

    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    expected_output = f"""List of installed binaries:
   • tarantool:
    2.10.7
    1.10.0
   • tarantool-dev:
    tarantool-dev -> {tmp_path}/tarantool [active]
"""
    assert rc == 0
    assert compare_output(output, expected_output)


def replace_prog(prog: Path, version: str) -> None:
    prog.unlink(missing_ok=True)
    with open(prog, "w") as p:
        p.write(
            f"""#!/bin/sh
echo '{version}'
"""
        )
    prog.chmod(0o750)


def test_list_tarantool_no_symlink(tt_cmd, tmp_path):
    # Copy the test bin_dir to the "run" directory.
    copytree(DATA_DIR / "list", tmp_path, symlinks=True, dirs_exist_ok=True)

    replace_prog(
        tmp_path / "bin" / "tarantool", "Tarantool 3.1.0-entrypoint-83-gcb0264c3c"
    )

    # Print binaries
    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    expected_output = """List of installed binaries:
   • tt:
    0.1.0
   • tarantool:
    3.1.0-entrypoint-83-gcb0264c3c [active]
    2.10.3
    2.8.1
   • tcm:
    1.3.1-0-g074b5ffa [active]
    1.2.0-11-g2d0a0f495
"""

    assert rc == 0
    assert compare_output(output, expected_output)

    # Remove non-versioned tarantool binary and TCM symlink.
    (tmp_path / "bin" / "tarantool").unlink(missing_ok=True)
    (tmp_path / "bin" / "tcm").unlink(missing_ok=True)
    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    expected_output = """List of installed binaries:
   • tt:
    0.1.0
   • tarantool:
    2.10.3
    2.8.1
   • tcm:
    1.3.1-0-g074b5ffa
    1.2.0-11-g2d0a0f495
"""

    assert rc == 0
    assert compare_output(output, expected_output)
