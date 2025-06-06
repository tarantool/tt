import os
import re
import shutil
import tempfile
from pathlib import Path

import pytest
from integration.connect.test_connect import start_app, stop_app, try_execute_on_instance

from utils import (
    BINARY_PORT_NAME,
    TarantoolTestInstance,
    control_socket,
    create_tt_config,
    get_tarantool_version,
    initial_snap,
    initial_xlog,
    lib_path,
    run_command_and_get_output,
    run_path,
    skip_if_cluster_app_unsupported,
    skip_if_tarantool_ce,
    wait_file,
    wait_files,
)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()

# The name of instance config file within this integration tests.
# This file should be in /test/integration/play/test_file/.
INSTANCE_NAME = "remote_instance_cfg.lua"


@pytest.fixture
def test_instance(request, tmp_path) -> TarantoolTestInstance:
    dir = os.path.dirname(__file__)
    test_app_path = os.path.join(dir, "test_file")
    lua_utils_path = os.path.join(dir, "..", "..")
    inst = TarantoolTestInstance(INSTANCE_NAME, test_app_path, lua_utils_path, tmp_path)
    inst.start(use_lua=True)
    request.addfinalizer(lambda: inst.stop())
    return inst


def test_play_non_existent_uri(tt_cmd, test_instance, tmp_path):
    # Testing with non-existent uri.
    cmd = [tt_cmd, "play", "127.0.0.1:0", f"{test_instance._tmpdir}/test.xlog"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert re.search(r'no connection to the host "127.0.0.1:0"', output)


@pytest.mark.parametrize(
    "args, play_error",
    [
        (
            # Testing with unset uri and .xlog or .snap file.
            "",
            "required to specify an URI and at least one .xlog/.snap file or directory",
        ),
        (
            "path-to-non-existent-file",
            "error: could not collect WAL files",
        ),
        (
            ("test.xlog", "--timestamp=abcdef", "--space=999"),
            'failed to parse a timestamp: parsing time "abcdef"',
        ),
        (
            ("test.xlog", "--timestamp=2024-11-14T14:02:36.abc", "--space=999"),
            'failed to parse a timestamp: parsing time "2024-11-14T14:02:36.abc"',
        ),
    ],
)
def test_play_test_remote_instance_timestamp_failed(tt_cmd, test_instance, args, play_error):
    # Play .xlog file to the remote instance.
    cmd = [tt_cmd, "play", f"127.0.0.1:{test_instance.port}"]
    cmd.extend(args)
    rc, play_output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
    assert rc == 1
    assert play_error in play_output


@pytest.mark.parametrize(
    "input, expected",
    [
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
    ],
)
def test_play_remote_instance_timestamp_valid(tt_cmd, test_instance, input, expected):
    test_dir = os.path.join(
        os.path.dirname(__file__),
        "test_file",
    )

    # Create space and primary index.
    cmd_space = [
        tt_cmd,
        "connect",
        f"test_user:secret@127.0.0.1:{test_instance.port}",
        "-f",
        f"{test_dir}/create_space.lua",
        "-",
    ]
    rc, _ = run_command_and_get_output(cmd_space, cwd=test_instance._tmpdir)
    assert rc == 0

    # Play .xlog file to the instance.
    cmd_play = [
        tt_cmd,
        "play",
        f"127.0.0.1:{test_instance.port}",
        "-u",
        "test_user",
        "-p",
        "secret",
        f"{test_dir}/timestamp/timestamp.xlog",
        f"--timestamp={input}",
    ]
    rc, _ = run_command_and_get_output(cmd_play, cwd=test_instance._tmpdir)
    assert rc == 0

    # Get data from the instance.
    cmd_data = [
        tt_cmd,
        "connect",
        f"test_user:secret@127.0.0.1:{test_instance.port}",
        "-f",
        f"{test_dir}/get_data.lua",
        "-",
    ]
    rc, cmd_output = run_command_and_get_output(cmd_data, cwd=test_instance._tmpdir)
    assert rc == 0
    assert cmd_output == expected


def test_play_remote_instance_space_error(tt_cmd, test_instance):
    test_dir = os.path.join(
        os.path.dirname(__file__),
        "test_file",
    )

    # Create space and primary index.
    cmd_space = [tt_cmd, "connect", f"test_user:secret@127.0.0.1:{test_instance.port}"]
    rc, _ = run_command_and_get_output(cmd_space, cwd=test_instance._tmpdir)
    assert rc == 0

    # Play .xlog file to the instance.
    cmd_play = [
        tt_cmd,
        "play",
        f"127.0.0.1:{test_instance.port}",
        "-u",
        "test_user",
        "-p",
        "secret",
        f"{test_dir}/timestamp/timestamp.xlog",
    ]
    rc, output = run_command_and_get_output(cmd_play, cwd=test_instance._tmpdir)
    assert rc == 1
    assert "Fatal error: no space #512 or permissions to work with it, stopping" in output


@pytest.mark.parametrize(
    "input, expected",
    [
        (
            ("test_file/test.xlog", "test_file/test.snap", "test_file/timestamp"),
            (
                'Play is processing file "{tmp}/test_file/test.xlog"',
                'Play is processing file "{tmp}/test_file/test.snap"',
                'Play is processing file "{tmp}/test_file/timestamp/timestamp.snap"',
                'Play is processing file "{tmp}/test_file/timestamp/timestamp.xlog"',
            ),
        ),
    ],
)
def test_play_directories_successful(
    tt_cmd: Path,
    test_instance: TarantoolTestInstance,
    input,
    expected,
):
    # Copy files to the "run" directory.
    shutil.copytree(
        Path(__file__).parent / "test_file",
        test_instance._tmpdir / "test_file",
    )

    # Create space and primary index.
    cmd_space = [
        tt_cmd,
        "connect",
        f"test_user:secret@127.0.0.1:{test_instance.port}",
        "-f",
        "test_file/create_space.lua",
        "-",
    ]
    rc, _ = run_command_and_get_output(cmd_space, cwd=test_instance._tmpdir)
    assert rc == 0

    # Play .xlog file to the remote instance.
    cmd = [tt_cmd, "play", f"127.0.0.1:{test_instance.port}", "-u", "test_user", "-p", "secret"]
    cmd.extend(input)

    rc, cmd_output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
    assert rc == 0
    for item in expected:
        item = item.format(tmp=test_instance._tmpdir)
        assert item in cmd_output


@pytest.mark.parametrize(
    "opts",
    [
        pytest.param({"flags": ["--username=test_user", "--password=4"]}),
        pytest.param({"flags": ["--username=fry"]}),
        pytest.param({"env": {"TT_CLI_USERNAME": "test_user", "TT_CLI_PASSWORD": "4"}}),
        pytest.param({"env": {"TT_CLI_USERNAME": "fry"}}),
        pytest.param({"uri": "test_user:4"}),
    ],
)
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


@pytest.mark.parametrize(
    "opts",
    [
        pytest.param({"flags": ["--username=test_user", "--password=secret"]}),
        pytest.param(
            {
                "env": {
                    "TT_CLI_USERNAME": "test_user",
                    "TT_CLI_PASSWORD": "secret",
                    "PATH": os.getenv("PATH"),
                },
            },
        ),
        pytest.param({"uri": "test_user:secret"}),
    ],
)
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


def test_play_to_single_instance_app(tt_cmd, tmp_path):
    # The test application file.
    app_name = "test_app"
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "test_app.lua")
    test_xlog_name = "timestamp.snap"
    test_xlog_path = os.path.join(
        os.path.dirname(__file__),
        "test_file",
        "timestamp",
        test_xlog_name,
    )
    # Copy test data into temporary directory.
    shutil.copy(test_app_path, tmp_path)
    shutil.copy(test_xlog_path, tmp_path)
    shutil.copy(os.path.join(os.path.dirname(__file__), "test_file", "config.yml"), tmp_path)
    shutil.copy(os.path.join(os.path.dirname(__file__), "test_file", "instances.yml"), tmp_path)
    create_tt_config(tmp_path, "")

    # Start an instance.
    start_app(tt_cmd, tmp_path, "test_app", start_binary_port=True)
    try:
        # Check for start.
        file = wait_file(
            os.path.join(tmp_path, "test_app", run_path, "test_app"),
            control_socket,
            [],
        )
        assert file != ""

        # Wait until application is ready.
        lib_dir = os.path.join(tmp_path, app_name, lib_path, app_name)
        file = wait_file(lib_dir, initial_snap, [])
        assert file != ""
        file = wait_file(lib_dir, initial_xlog, [])
        assert file != ""

        # Connect to the instance and execute a script.
        rc, _ = try_execute_on_instance(tt_cmd, tmp_path, "test_app", test_app_path)
        assert rc

        cmd = [tt_cmd, "play", "test_app", test_xlog_name]
        rc, _ = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 0

    finally:
        # Stop the Instance.
        stop_app(tt_cmd, tmp_path, "test_app")


def test_play_to_single_instance_without_binary_port(tt_cmd, test_instance):
    test_xlog_name = "test.xlog"
    test_xlog_path = os.path.join(os.path.dirname(__file__), "test_file", test_xlog_name)
    shutil.copy(test_xlog_path, test_instance._tmpdir)

    create_tt_config(test_instance._tmpdir, "")

    # Start an instance.
    start_app(tt_cmd, test_instance._tmpdir, "remote_instance_cfg", start_binary_port=False)

    cmd = [tt_cmd, "play", "remote_instance_cfg", test_xlog_name]
    rc, cmd_output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
    assert rc == 1
    assert "application binary port does not exist" in cmd_output


def test_play_to_cluster_app(tt_cmd):
    tmpdir = tempfile.mkdtemp()
    create_tt_config(tmpdir, "")
    skip_if_cluster_app_unsupported()

    empty_file = "empty.lua"
    app_name = "test_simple_cluster_app"
    test_xlog_name = "test.snap"
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), app_name)
    tmp_app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(test_app_path, tmp_app_path)
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    test_xlog_path = os.path.join(os.path.dirname(__file__), "test_file", test_xlog_name)
    # Copy test data into temporary directory.
    for item in (empty_file_path, test_xlog_path):
        shutil.copy(item, tmpdir)

    # Start instances.
    start_app(tt_cmd, tmpdir, app_name, True)
    try:
        # Check for start.
        instances = ["master"]
        for instance in instances:
            master_run_path = os.path.join(tmpdir, app_name, run_path, instance)
            file = wait_file(master_run_path, control_socket, [])
            assert file != ""
            file = wait_file(master_run_path, BINARY_PORT_NAME, [])
            assert file != ""
            file = wait_file(os.path.join(tmpdir, app_name), "configured", [])
            assert file != ""

        # Play to the instances.
        cmd = [tt_cmd, "play", app_name + ":master", test_xlog_name]
        rc, cmd_output = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 0

        cmd = [tt_cmd, "play", app_name + ":master123", test_xlog_name]
        rc, cmd_output = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 1
        assert (
            "could not resolve URI or application: " + '"test_simple_cluster_app:master123"'
        ) in cmd_output

    finally:
        # Stop the Instance.
        stop_app(tt_cmd, tmpdir, app_name)
        shutil.rmtree(tmpdir)


@pytest.mark.skipif(tarantool_major_version == 1, reason="skip TLS test for Tarantool 1.0")
def test_play_to_ssl_app(tt_cmd, tmpdir_with_cfg):
    skip_if_tarantool_ce()

    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_ssl_app")
    # The test file.
    empty_file = "empty.lua"
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # File to play.
    test_xlog_path = os.path.join(os.path.dirname(__file__), "test_file", "test.snap")

    # Copy test data into temporary directory.
    shutil.copytree(test_app_path, os.path.join(tmpdir, "test_ssl_app"))
    shutil.copy(empty_file_path, os.path.join(tmpdir, "test_ssl_app", empty_file))

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_ssl_app")
    try:
        # 'ready' file should be created by application.
        file = wait_file(os.path.join(tmpdir, "test_ssl_app"), "ready", [])
        assert file != ""

        server = "localhost:3013"
        # Connect without SSL options.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, server, empty_file)
        assert not ret
        assert re.search(r"   ⨯ unable to establish connection", output)

        cmd = [
            tt_cmd,
            "play",
            "localhost:3013",
            test_xlog_path,
            "--sslkeyfile=test_ssl_app/localhost.key",
            "--sslcertfile=test_ssl_app/localhost.crt",
            "--sslcafile=test_ssl_app/ca.crt",
        ]
        rc, _ = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 0

    finally:
        # Stop the Instance.
        stop_app(tt_cmd, tmpdir, "test_ssl_app")


@pytest.mark.tt(
    app_path="test_simple_cluster_app",
    instances=["master"],
)
@pytest.mark.parametrize(
    "with_scheme",
    [
        pytest.param(True, id="with-scheme"),
        pytest.param(False, id="no-scheme"),
    ],
)
@pytest.mark.parametrize(
    "uri",
    [
        pytest.param("localhost:3013", id="hostname"),
        pytest.param("127.0.0.1:3013", id="ipv4"),
        pytest.param("[::1]:3013", id="ipv6"),
    ],
)
def test_play_to_cluster_app_by_uri(tt, with_scheme, uri):
    skip_if_cluster_app_unsupported()

    if with_scheme:
        uri = f"tcp://{uri}"
    file_to_play = Path(__file__) / "test_file" / "test.snap"

    # Start instances.
    p = tt.run(
        "start",
        env={"TT_IPROTO_LISTEN": f'[{{"uri":"{uri}"}}]'},
    )
    # Check for start.
    assert p.returncode == 0
    assert wait_files(5, [tt.path("configured")])

    # Play to the instance.
    p = tt.run("play", uri, file_to_play)
    assert p.returncode == 0
