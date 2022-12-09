import os
import platform
import re
import shutil

import pytest

from utils import create_tt_config, run_command_and_get_output

# ##### #
# Tests #
# ##### #


def test_rocks_module(tt_cmd, tmpdir):
    create_tt_config(tmpdir, tmpdir)

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "help"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "rocks - LuaRocks package manager\n" in output

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "search", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Rockspecs and source rocks:\n" in output

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "install", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Cloning into 'queue'...\n" in output
    assert os.path.isfile(f'{tmpdir}/.rocks/share/tarantool/queue/init.lua')

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "doc", "queue", "--list"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Documentation files for queue" in output

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "pack", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert re.search("Packed: .*queue-.*[.]rock", output)
    rock_file = output.split("Packed: ")[1].strip()
    assert os.path.isfile(rock_file)

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "unpack", rock_file],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    rock_dir = rock_file.split('.', 1)[0]
    assert os.path.isdir(rock_dir)

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "remove", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Removal successful.\n" in output

    test_app_path = os.path.join(os.path.dirname(__file__), "files", "testapp-scm-1.rockspec")
    shutil.copy(test_app_path, tmpdir)
    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "make", "testapp-scm-1.rockspec"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "testapp scm-1 is now installed" in output


def test_rocks_install_remote(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as tnt_env_file:
        tnt_env_file.write('''tt:
  repo:
    rocks: "repo"''')
    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "install", "stat"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Installing http://rocks.tarantool.org/stat" in output


def test_rocks_install_local(tt_cmd, tmpdir):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as tnt_env_file:
        tnt_env_file.write('''tt:
  repo:
    rocks: "repo"''')

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"),
                    os.path.join(tmpdir, "repo"))

    # Disable network with unshare.
    rc, output = run_command_and_get_output(
            ["unshare", "-r", "-n", tt_cmd, "rocks", "install", "stat"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Installing repo/stat-0.3.2-1.all.rock" in output


def test_rocks_install_local_specific_version(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as tnt_env_file:
        tnt_env_file.write('''tt:
  repo:
    rocks: "repo"''')

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"),
                    os.path.join(tmpdir, "repo"))

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "install", "stat", "0.3.1-1"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Installing repo/stat-0.3.1-1.all.rock" in output
