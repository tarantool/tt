import os
import re
import shutil
import subprocess
from time import sleep

from utils import run_command_and_get_output


def test_play_unset_arg(tt_cmd, tmpdir):
    # Testing with unset uri and .xlog or .snap file.
    cmd = [tt_cmd, "play"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"required to specify an URI and at least one .xlog or .snap file.", output)


def test_play_non_existent_uri(tt_cmd, tmpdir):
    # Testing with non-existent uri.
    cmd = [tt_cmd, "play", "localhost:0", "_"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"no connection to the host", output)


def test_play_non_existent_file(tt_cmd, tmpdir):
    # Testing with non-existent .xlog or .snap file.
    cmd = [tt_cmd, "play", "localhost:49001", "path-to-non-existent-file"]
    instance = subprocess.Popen(["tarantool", "-e", "box.cfg{listen = 'localhost:49001';}"])
    # The delay is needed so that the instance has time to start and configure itself
    sleep(1)
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    instance.kill()
    assert rc == 1
    assert re.search(r"No such file or directory", output)


def test_play_test_remote_instance(tt_cmd, tmpdir):
    # Copy the .xlog and instance config files to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file")
    shutil.copy(test_app_path + "/test.xlog", tmpdir)
    shutil.copy(test_app_path + "/remote_instance_cfg.lua", tmpdir)

    # Play .xlog file to the remote instance.
    cmd = [tt_cmd, "play", "localhost:3301", "test.xlog", "--space=999"]
    instance = subprocess.Popen(["tarantool", "remote_instance_cfg.lua"], cwd=tmpdir)
    # The delay is needed so that the instance has time to start and configure itself
    sleep(1)
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    instance.kill()
    assert rc == 0
    assert re.search(r"Play result: completed successfully", output)

    # Testing played .xlog file from the remote instance.
    cmd = [tt_cmd, "cat", "00000000000000000000.xlog", "--space=999"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"space_id: 999", output)
    assert re.search(r"[1, 'Roxette', 1986]", output)
    assert re.search(r"[2, 'Scorpions', 2015]", output)
    assert re.search(r"[3, 'Ace of Base', 1993]", output)
