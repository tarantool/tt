import os
import re
import shutil

import pytest

from utils import TarantoolTestInstance, kill_child_process, run_command_and_get_output

# The name of instance config file within this integration tests.
# This file should be in /test/integration/play/test_file/.
INSTANCE_NAME = "remote_instance_cfg.lua"


# In case of unsuccessful completion of tests, tarantool test instances may remain running.
# This is autorun wrapper for each test case in this module.
@pytest.fixture(autouse=True)
def kill_remain_instance_wrapper():
    # Run test.
    yield
    # Kill a test instance if it was not stopped due to a failed test.
    kill_child_process()


def test_play_unset_arg(tt_cmd, tmpdir):
    # Testing with unset uri and .xlog or .snap file.
    cmd = [tt_cmd, "play"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"required to specify an URI and at least one .xlog or .snap file", output)


def test_play_non_existent_uri(tt_cmd, tmpdir):
    # Testing with non-existent uri.
    cmd = [tt_cmd, "play", "127.0.0.1:0", "_"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"no connection to the host", output)


def test_play_non_existent_file(tt_cmd, tmpdir):
    # Testing with non-existent .xlog or .snap file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file")

    # Create tarantool instance for testing and start it.
    path_to_lua_utils = os.path.join(os.path.dirname(__file__), "test_file/../../../")
    test_instance = TarantoolTestInstance(INSTANCE_NAME, test_app_path, path_to_lua_utils, tmpdir)
    test_instance.start()

    # Run play with non-existent file.
    cmd = [tt_cmd, "play", "127.0.0.1:" + test_instance.port, "path-to-non-existent-file"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    test_instance.stop()
    assert rc == 1
    assert re.search(r"No such file or directory", output)


def test_play_test_remote_instance(tt_cmd, tmpdir):
    # Testing play using remote instance.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file")
    # Copy the .xlog file to the "run" directory.
    shutil.copy(test_app_path + "/test.xlog", tmpdir)

    # Create tarantool instance for testing and start it.
    path_to_lua_utils = os.path.join(os.path.dirname(__file__), "test_file/../../../")
    test_instance = TarantoolTestInstance(INSTANCE_NAME, test_app_path, path_to_lua_utils, tmpdir)
    test_instance.start()

    # Play .xlog file to the remote instance.
    cmd = [tt_cmd, "play", "127.0.0.1:" + test_instance.port, "test.xlog", "--space=999"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    test_instance.stop()
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
