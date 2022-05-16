import os
import re

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
    assert(os.path.isfile(f'{tmpdir}/.rocks/share/tarantool/queue/init.lua'))

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
    assert(os.path.isfile(rock_file))

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "unpack", rock_file],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    rock_dir = rock_file.split('.', 1)[0]
    assert(os.path.isdir(rock_dir))

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "remove", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Removal successful.\n" in output
