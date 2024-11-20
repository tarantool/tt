import os
import re

import pytest

from utils import TarantoolTestInstance, run_command_and_get_output

# The name of instance config file within this integration tests.
# This file should be in /test/integration/play/test_file/.
INSTANCE_NAME = "remote_instance_cfg.lua"


@pytest.fixture
def test_instance(request, tmp_path):
    dir = os.path.dirname(__file__)
    test_app_path = os.path.join(dir, "test_file")
    lua_utils_path = os.path.join(dir, "..", "..")
    inst = TarantoolTestInstance(INSTANCE_NAME, test_app_path, lua_utils_path, tmp_path)
    inst.start(use_lua=True)
    request.addfinalizer(lambda: inst.stop())
    return inst


def test_play_non_existent_uri(tt_cmd, tmp_path):
    # Testing with non-existent uri.
    cmd = [tt_cmd, "play", "127.0.0.1:0", "_"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert re.search(r"no connection to the host", output)


@pytest.mark.parametrize("args, play_error", [
    (
        # Testing with unset uri and .xlog or .snap file.
        "",
        "required to specify an URI and at least one .xlog or .snap file",
    ),
    (
        "path-to-non-existent-file",
        "No such file or directory",
    ),
    (
        ("test.xlog", "--timestamp=abcdef", "--space=999"),
        'failed to parse a timestamp: parsing time "abcdef"',
    ),
    (
        ("test.xlog", "--timestamp=2024-11-14T14:02:36.abc", "--space=999"),
        'failed to parse a timestamp: parsing time "2024-11-14T14:02:36.abc"',
    ),
])
def test_play_test_remote_instance_timestamp_failed(tt_cmd, test_instance, args, play_error):
    # Play .xlog file to the remote instance.
    cmd = [tt_cmd, "play", f"127.0.0.1:{test_instance.port}"]
    cmd.extend(args)
    rc, play_output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
    assert rc == 1
    assert play_error in play_output


@pytest.mark.parametrize("input, expected", [
    (
        1731592956.1182,
        "---\n- []\n...\n\n",
    ),
    (
        1731592956.8184,
        "---\n- - [1, 0]\n  - [2, 1]\n  - [3, 2]\n  - [4, 3]\n  - [5, 4]\n  - [6, 5]\n...\n\n",
    ),
    (
        "2024-11-14T14:02:36.818+00:00",
        "---\n- - [1, 0]\n...\n\n",
    ),
    (
        "2024-11-14T14:02:36+00:00",
        "---\n- []\n...\n\n",
    ),
])
def test_play_remote_instance_timestamp_valid(tt_cmd, test_instance,
                                              input, expected):
    test_dir = os.path.join(os.path.dirname(__file__), "test_file/timestamp", )

    # Create space and primary index.
    cmd_space = [tt_cmd, "connect", f"test_user:secret@127.0.0.1:{test_instance.port}",
                 "-f", f"{test_dir}/create_space.lua", "-"]
    rc, _ = run_command_and_get_output(cmd_space, cwd=test_instance._tmpdir)
    assert rc == 0

    # Play .xlog file to the instance.
    cmd_play = [tt_cmd, "play", f"127.0.0.1:{test_instance.port}",
                "-u", "test_user", "-p", "secret",
                f"{test_dir}/timestamp.xlog", f"--timestamp={input}"]
    rc, _ = run_command_and_get_output(cmd_play, cwd=test_instance._tmpdir)
    assert rc == 0

    # Get data from the instance.
    cmd_data = [tt_cmd, "connect", f"test_user:secret@127.0.0.1:{test_instance.port}",
                "-f", f"{test_dir}/get_data.lua", "-"]
    rc, cmd_output = run_command_and_get_output(cmd_data, cwd=test_instance._tmpdir)
    assert rc == 0
    assert cmd_output == expected


@pytest.mark.parametrize("opts", [
    pytest.param({"flags": ["--username=test_user", "--password=4"]}),
    pytest.param({"flags": ["--username=fry"]}),
    pytest.param({"env": {"TT_CLI_USERNAME": "test_user", "TT_CLI_PASSWORD": "4"}}),
    pytest.param({"env": {"TT_CLI_USERNAME": "fry"}}),
    pytest.param({"uri": "test_user:4"}),
])
def test_play_wrong_creds(tt_cmd, tmp_path, opts, test_instance):
    # Play .xlog file to the remote instance.
    uri = "127.0.0.1:" + test_instance.port
    if "uri" in opts:
        uri = opts["uri"] + "@" + uri
    if "env" in opts:
        env = opts["env"]
    else:
        env = None
    cmd = [tt_cmd, "play", uri, "test.xlog", "--space=999"]
    if "flags" in opts:
        cmd.extend(opts["flags"])

    rc, _ = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc != 0


@pytest.mark.parametrize("opts", [
    pytest.param({"flags": ["--username=test_user", "--password=secret"]}),
    pytest.param({"env": {
        "TT_CLI_USERNAME": "test_user",
        "TT_CLI_PASSWORD": "secret",
        "PATH": os.getenv("PATH"),
    }}),
    pytest.param({"uri": "test_user:secret"}),
])
def test_play_creds(tt_cmd, tmp_path, opts, test_instance):
    # Play .xlog file to the remote instance.
    uri = "127.0.0.1:" + test_instance.port
    if "uri" in opts:
        uri = opts["uri"] + "@" + uri
    if "env" in opts:
        env = opts["env"]
    else:
        env = None
    cmd = [tt_cmd, "play", uri, "test.xlog", "--space=999"]
    if "flags" in opts:
        cmd.extend(opts["flags"])

    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0
