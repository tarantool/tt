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


def test_play_unset_arg(tt_cmd, tmp_path):
    # Testing with unset uri and .xlog or .snap file.
    cmd = [tt_cmd, "play"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert re.search(r"required to specify an URI and at least one .xlog or .snap file", output)


def test_play_non_existent_uri(tt_cmd, tmp_path):
    # Testing with non-existent uri.
    cmd = [tt_cmd, "play", "127.0.0.1:0", "_"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert re.search(r"no connection to the host", output)


def test_play_non_existent_file(tt_cmd, tmp_path, test_instance):
    # Run play with non-existent file.
    cmd = [tt_cmd, "play", "127.0.0.1:" + test_instance.port, "path-to-non-existent-file"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert re.search(r"No such file or directory", output)


def test_play_test_remote_instance(tt_cmd, test_instance):
    # Play .xlog file to the remote instance.
    cmd = [tt_cmd, "play", "127.0.0.1:" + test_instance.port, "test.xlog", "--space=999"]
    rc, output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
    assert rc == 0
    assert re.search(r"Play result: completed successfully", output)

    # Testing played .xlog file from the remote instance.
    cmd = [tt_cmd, "cat", "00000000000000000000.xlog", "--space=999"]
    rc, output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
    assert rc == 0
    assert re.search(r"space_id: 999", output)
    assert re.search(r"[1, 'Roxette', 1986]", output)
    assert re.search(r"[2, 'Scorpions', 2015]", output)
    assert re.search(r"[3, 'Ace of Base', 1993]", output)


TEST_PLAY_TIMESTAMP_PARAMS_CCONFIG = ("input, play_result, found, not_found")


def make_test_play_timestamp_param(
    input="",
    play_result=0,
    found={},
    not_found={},
):
    return pytest.param(input, play_result, found, not_found)


@pytest.mark.parametrize(TEST_PLAY_TIMESTAMP_PARAMS_CCONFIG, [
    make_test_play_timestamp_param(
        input="abcdef",
        play_result=1,
        found={"failed to parse a timestamp: parsing time \"abcdef\""},
    ),
    make_test_play_timestamp_param(
        input="2024-11-14T14:02:36.abc",
        play_result=1,
        found={"failed to parse a timestamp: parsing time \"2024-11-14T14:02:36.abc\""},
    ),
    make_test_play_timestamp_param(
        input="",
        play_result=0,
        found={"[3, 'Ace of Base', 1993]"},
    ),
    make_test_play_timestamp_param(
        input="1651130533.1534",
        play_result=0,
        found={"space_id: 999",
               "[1, 'Roxette', 1986]",
               "[2, 'Scorpions', 2015]"},
        not_found={"Ace of Base"},
    ),
    make_test_play_timestamp_param(
        input="2022-04-28T07:22:13.1534+00:00",
        play_result=0,
        found={"space_id: 999",
               "[1, 'Roxette', 1986]",
               "[2, 'Scorpions', 2015]"},
        not_found={"Ace of Base"},
    ),
    make_test_play_timestamp_param(
        input="2022-04-28T07:22:12+00:00",
        play_result=0,
        found={"space_id: 999",
               "[1, 'Roxette', 1986]"},
        not_found={"Scorpions",
                   "Ace of Base"},
    ),
])
def test_play_test_remote_instance_timestamp(tt_cmd, test_instance, input,
                                             play_result, found, not_found):
    # Play .xlog file to the remote instance.
    cmd = [tt_cmd, "play", "127.0.0.1:" + test_instance.port, "test.xlog",
           "--timestamp={0}".format(input), "--space=999"]
    rc, output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
    assert rc == play_result
    if play_result == 0:
        # Testing played .xlog file from the remote instance.
        cmd = [tt_cmd, "cat", "00000000000000000000.xlog", "--space=999"]
        rc, output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
        assert rc == 0
        for item in found:
            assert re.search(r"{0}".format(item), output)
        for item in not_found:
            assert not re.search(r"{0}".format(item), output)


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

    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
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
