import os
import re

import pytest

from utils import TarantoolTestInstance, run_command_and_get_output

# The name of instance config file within this integration tests.
# This file should be in /test/integration/play/test_file/.
INSTANCE_NAME = "remote_instance_cfg.lua"


@pytest.fixture
def test_instance(request, tmpdir):
    dir = os.path.dirname(__file__)
    test_app_path = os.path.join(dir, "test_file")
    lua_utils_path = os.path.join(dir, "..", "..")
    inst = TarantoolTestInstance(INSTANCE_NAME, test_app_path, lua_utils_path, tmpdir)
    inst.start()
    request.addfinalizer(lambda: inst.stop())
    return inst


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


def test_play_non_existent_file(tt_cmd, tmpdir, test_instance):
    # Run play with non-existent file.
    cmd = [tt_cmd, "play", "127.0.0.1:" + test_instance.port, "path-to-non-existent-file"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
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


@pytest.mark.parametrize("opts", [
    pytest.param({"flags": ["--username=test_user", "--password=4"]}),
    pytest.param({"flags": ["--username=fry"]}),
    pytest.param({"env": {"TT_CLI_USERNAME": "test_user", "TT_CLI_PASSWORD": "4"}}),
    pytest.param({"env": {"TT_CLI_USERNAME": "fry"}}),
    pytest.param({"uri": "test_user:4"}),
])
def test_play_wrong_creds(tt_cmd, tmpdir, opts, test_instance):
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

    rc, output = run_command_and_get_output(cmd, cwd=tmpdir, env=env)
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
def test_play_creds(tt_cmd, tmpdir, opts, test_instance):
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

    rc, output = run_command_and_get_output(cmd, cwd=tmpdir, env=env)
    assert rc == 0
