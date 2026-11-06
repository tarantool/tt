import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path

import pytest
import yaml
from integration.connect.test_connect import start_app, stop_app, try_execute_on_instance
from integration.data.test_cat import JOURNALS_SCRIPT

from utils import (
    BINARY_PORT_NAME,
    TarantoolTestInstance,
    cluster_config_file,
    control_socket,
    initial_snap,
    initial_xlog,
    inst_lib_dir,
    inst_run_dir,
    instances_file,
    run_command_and_get_output,
    run_command_and_get_streams,
    skip_if_tarantool_ce,
    wait_file,
    wait_files,
)

TEST_FILES = Path(__file__).parent / "test_file"
LUA_UTILS = Path(__file__).parent.parent.parent

# The name of instance config file within this integration tests.
# This file should be in /test/integration/data/test_file/.
INSTANCE_NAME = "remote_instance_cfg.lua"


def start_instance(request, tmp_path, config) -> TarantoolTestInstance:
    inst = TarantoolTestInstance(config, TEST_FILES, LUA_UTILS, tmp_path)
    inst.start(use_lua=True)
    request.addfinalizer(lambda: inst.stop())
    return inst


@pytest.fixture
def test_instance(request, tmp_path) -> TarantoolTestInstance:
    return start_instance(request, tmp_path, INSTANCE_NAME)


@pytest.fixture(scope="module")
def source(tmp_path_factory) -> Path:
    """The journals of an instance JOURNALS_SCRIPT writes: a snapshot, and
    every kind of data change after it."""
    work = tmp_path_factory.mktemp("source")
    (work / "journals.lua").write_text(JOURNALS_SCRIPT)
    subprocess.run(["tarantool", "journals.lua"], cwd=work, check=True)
    (work / "journals.lua").unlink()
    return work


def select(tt_cmd, inst, space="test"):
    """The tuples of a space of the instance, by tt connect."""
    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "connect",
            f"test_user:secret@127.0.0.1:{inst.port}",
            "-f-",
        ],
        input=f"return box.space.{space}:select()",
    )
    assert rc == 0, output
    return yaml.safe_load(output)[0]


def play(tt_cmd, inst, *args, cwd=None, env=None):
    cmd = [tt_cmd, "data", "play", f"test_user:secret@127.0.0.1:{inst.port}", *map(str, args)]
    return run_command_and_get_streams(cmd, cwd=cwd, env=env)


def test_play_non_existent_uri(tt_cmd, test_instance, tmp_path):
    cmd = [tt_cmd, "data", "play", "127.0.0.1:0", f"{test_instance._tmpdir}/test.xlog"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 2
    assert re.search(r"connecting to 127.0.0.1:0: failed to connect", output)


@pytest.mark.parametrize(
    "args, play_error",
    [
        ([], "specify a target and at least one .snap or .xlog file or directory"),
        (["path-to-non-existent-file"], "no such file or directory"),
        (
            ["test.xlog", "--timestamp=abcdef", "--space=999"],
            'failed to parse a timestamp: parsing time "abcdef"',
        ),
        (
            ["test.xlog", "--timestamp=2024-11-14T14:02:36.abc", "--space=999"],
            'failed to parse a timestamp: parsing time "2024-11-14T14:02:36.abc"',
        ),
        (["test.xlog", "--txn-mode=nested"], '--txn-mode "nested": want auto, stream, row'),
        (["test.xlog", "--on-error=retry"], '--on-error "retry": want stop, skip'),
        (["test.xlog", "--batch=0"], "--batch must be at least 1"),
    ],
)
def test_play_invalid(tt_cmd, test_instance, args, play_error):
    cmd = [tt_cmd, "data", "play", f"127.0.0.1:{test_instance.port}", *args]
    rc, play_output = run_command_and_get_output(cmd, cwd=test_instance._tmpdir)
    assert rc == 1
    assert play_error in play_output


@pytest.mark.parametrize(
    "input, expected",
    [
        (1731592956.1182, "---\n- []\n...\n\n"),
        (
            1731592956.8184,
            "---\n- - [1, 0]\n  - [2, 1]\n  - [3, 2]\n  - [4, 3]\n  - [5, 4]\n  - [6, 5]\n...\n\n",
        ),
        ("2024-11-14T14:02:36.818+00:00", "---\n- - [1, 0]\n...\n\n"),
        ("2024-11-14T14:02:36+00:00", "---\n- []\n...\n\n"),
    ],
)
def test_play_timestamp(tt_cmd, test_instance, input, expected):
    test_dir = TEST_FILES

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

    rc, _, stderr = play(
        tt_cmd,
        test_instance,
        f"{test_dir}/timestamp/timestamp.xlog",
        f"--timestamp={input}",
        cwd=test_instance._tmpdir,
    )
    assert rc == 0, stderr

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


def test_play_space_error(tt_cmd, test_instance):
    # The target has no space 512: the error is the server's, with the row.
    rc, _, stderr = play(
        tt_cmd,
        test_instance,
        TEST_FILES / "timestamp" / "timestamp.xlog",
        cwd=test_instance._tmpdir,
    )
    assert rc == 1
    assert "the target refused a transaction: the INSERT of space 512 at LSN 3 of replica 1" in (
        stderr
    )
    assert "Space '512' does not exist" in stderr
    assert "Applied 0 of 10 transactions" in stderr

    # A dry run finds it out without applying anything.
    rc, _, stderr = play(
        tt_cmd,
        test_instance,
        TEST_FILES / "timestamp" / "timestamp.xlog",
        "--dry-run",
        cwd=test_instance._tmpdir,
    )
    assert rc == 1
    assert "Dry run: 10 transactions with 10 data changes would be applied" in stderr
    assert "The target has no space 512, which 10 rows change" in stderr


def test_play_directories(tt_cmd, test_instance):
    shutil.copytree(TEST_FILES, test_instance._tmpdir / "files")

    cmd_space = [
        tt_cmd,
        "connect",
        f"test_user:secret@127.0.0.1:{test_instance.port}",
        "-f",
        "files/create_space.lua",
        "-",
    ]
    rc, _ = run_command_and_get_output(cmd_space, cwd=test_instance._tmpdir)
    assert rc == 0

    rc, _, stderr = play(
        tt_cmd,
        test_instance,
        "files/test.xlog",
        "files/test.snap",
        "files/timestamp",
        cwd=test_instance._tmpdir,
    )
    assert rc == 0, stderr
    # Of the two snapshots one is read.
    assert stderr.count("Skipping the snapshot") == 1
    # The 3 rows of the space tester of test.xlog, the 10 of space 512 of
    # timestamp.xlog.
    assert "Applied 13 of 13 transactions, 13 of 13 data changes" in stderr


@pytest.mark.parametrize(
    "opts, error",
    [
        pytest.param({"flags": ["--username=test_user", "--password=4"]}, "credentials"),
        pytest.param({"flags": ["--username=fry"]}, "credentials"),
        pytest.param({"env": {"TT_CLI_USERNAME": "test_user", "TT_CLI_PASSWORD": "4"}}, "cred"),
        pytest.param({"env": {"TT_CLI_USERNAME": "fry"}}, "credentials"),
        pytest.param({"uri": "test_user:4"}, "credentials"),
    ],
)
def test_play_wrong_creds(tt_cmd, tmp_path, opts, error, test_instance):
    uri = "127.0.0.1:" + test_instance.port
    if "uri" in opts:
        uri = opts["uri"] + "@" + uri
    env = opts.get("env")
    if env is not None:
        env = dict(os.environ, **env)
    cmd = [tt_cmd, "data", "play", uri, "test.xlog", "--space=999"]
    cmd.extend(opts.get("flags", []))

    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 1
    # The error is the one of the authentication, not one of a space.
    assert "User not found or supplied credentials are invalid" in output
    assert "space" not in output.lower()


@pytest.mark.parametrize(
    "opts",
    [
        pytest.param({"flags": ["--username=test_user", "--password=secret"]}),
        pytest.param({"env": {"TT_CLI_USERNAME": "test_user", "TT_CLI_PASSWORD": "secret"}}),
        pytest.param({"uri": "test_user:secret"}),
    ],
)
def test_play_creds(tt_cmd, tmp_path, opts, test_instance):
    uri = "127.0.0.1:" + test_instance.port
    if "uri" in opts:
        uri = opts["uri"] + "@" + uri
    env = opts.get("env")
    if env is not None:
        env = dict(os.environ, **env)
    cmd = [tt_cmd, "data", "play", uri, "test.xlog", "--space=999"]
    cmd.extend(opts.get("flags", []))

    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0, output


@pytest.mark.parametrize("config", ["target_mvcc.lua", "target_no_mvcc.lua"])
def test_play_journals(tt_cmd, request, tmp_path, source, config):
    inst = start_instance(request, tmp_path, config)

    # The snapshot - three rows of the space - and the seven transactions
    # after it, one of two rows.
    rc, _, stderr = play(tt_cmd, inst, source)
    assert rc == 0, stderr
    assert "Applied 10 of 10 transactions, 11 of 11 data changes" in stderr
    # Without MVCC the transactions of several rows are applied row by row.
    assert ("applying the transactions row by row" in stderr) == (config == "target_no_mvcc.lua")

    data = select(tt_cmd, inst)
    assert [row[0] for row in data] == [1, 3, 4, 5, 6, 7, 8, 9]
    assert data[0] == [1, "x"]
    assert data[2][1] == 12.34
    assert data[4][2] == 18446744073709551615

    # A second play stops at the first insert, which is refused: with one
    # request in flight nothing after it is applied.
    rc, _, stderr = play(tt_cmd, inst, source, "--batch=1")
    assert rc == 1
    assert "the target refused a transaction: the INSERT of space 512 at row " in stderr
    assert "Duplicate key exists" in stderr
    assert "Applied 0 of 1 transactions" in stderr

    # With --on-error skip it goes on: the insert of 2, which was deleted,
    # and the changes that are not inserts are applied.
    rc, _, stderr = play(tt_cmd, inst, source, "--on-error=skip")
    assert rc == 3
    assert "Applied 5 of 10 transactions" in stderr
    assert "the target refused 5 of 10 transactions" in stderr


def test_play_stream_mode_without_mvcc(tt_cmd, request, tmp_path, source):
    inst = start_instance(request, tmp_path, "target_no_mvcc.lua")

    rc, _, stderr = play(tt_cmd, inst, source, "--txn-mode=stream", "--on-error=skip")
    assert rc == 3
    assert "memtx_use_mvcc_engine" in stderr
    # The transaction of 8 and 9 is not applied, not even half.
    assert [row[0] for row in select(tt_cmd, inst)] == [1, 3, 4, 5, 6, 7]


def test_play_row_mode(tt_cmd, request, tmp_path, source):
    inst = start_instance(request, tmp_path, "target_mvcc.lua")

    rc, _, stderr = play(tt_cmd, inst, source, "--txn-mode=row", "--batch=1")
    assert rc == 0, stderr
    assert [row[0] for row in select(tt_cmd, inst)] == [1, 3, 4, 5, 6, 7, 8, 9]


def test_play_to_single_instance_app(tt_cmd):
    # The binary port of the instance, under .tarantool/var/run/<app>/<inst>/ of
    # the project, is given to tarantool by its full path: keep it short.
    tmp_path = Path(tempfile.mkdtemp(dir="/tmp"))
    app_name = "test_app"
    test_app_path = TEST_FILES / "test_app.lua"
    test_xlog_name = "timestamp.snap"
    test_xlog_path = TEST_FILES / "timestamp" / test_xlog_name
    app_dir = tmp_path / app_name
    os.makedirs(app_dir / os.path.dirname(instances_file))
    shutil.copy(test_app_path, app_dir)
    shutil.copy(test_xlog_path, app_dir)
    shutil.copy(TEST_FILES / "instances.yml", app_dir / instances_file)
    with open(app_dir / cluster_config_file, "w") as f:
        f.write(
            "app:\n"
            "  file: test_app.lua\n"
            "groups:\n"
            "  group001:\n"
            "    replicasets:\n"
            "      replicaset001:\n"
            "        instances:\n"
            "          instance001: {}\n",
        )
    target = f"{app_name}:instance001"

    start_app(tt_cmd, tmp_path, app_name, start_binary_port=True)
    try:
        file = wait_file(inst_run_dir(app_dir, app_name, "instance001"), control_socket, [])
        assert file != ""

        lib_dir = inst_lib_dir(app_dir, app_name, "instance001")
        file = wait_file(lib_dir, initial_snap, [])
        assert file != ""
        file = wait_file(lib_dir, initial_xlog, [])
        assert file != ""

        rc, _ = try_execute_on_instance(tt_cmd, tmp_path, target, test_app_path)
        assert rc

        cmd = [tt_cmd, "data", "play", target, test_xlog_name]
        rc, output = run_command_and_get_output(cmd, cwd=app_dir)
        assert rc == 0, output

    finally:
        stop_app(tt_cmd, tmp_path, app_name)
        shutil.rmtree(tmp_path)


def test_play_scope(tt_cmd, tmp_path):
    # --scope selects where the application of the target is looked for, as
    # in the instance commands: the instance of the user scope is found, and
    # its binary port is looked for under the user root.
    user = tmp_path / "user"
    version = user / "versions" / "myapp" / "myapp_1.0.0"
    (version / ".templates" / "default").mkdir(parents=True)
    (version / ".templates" / "default" / "instances.yaml").write_text("storage-1:\n")
    (version / ".templates" / "default" / "config.yaml").write_text(
        "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n          storage-1: {}\n",
    )
    (user / "apps").mkdir()
    os.symlink(version, user / "apps" / "myapp")
    shutil.copy(TEST_FILES / "test.xlog", tmp_path)
    env = dict(os.environ, TT_USER_ROOT=str(user), TT_SYSTEM_ROOT=str(tmp_path / "system"))

    rc, out = run_command_and_get_output(
        [tt_cmd, "data", "play", "--scope", "user", "myapp:storage-1", "test.xlog"],
        cwd=tmp_path,
        env=env,
    )
    assert rc == 1
    assert "the binary port of myapp:storage-1 does not exist" in out
    assert os.path.join(user, "var", "run", "myapp", "storage-1", BINARY_PORT_NAME) in out

    rc, out = run_command_and_get_output(
        [tt_cmd, "data", "play", "myapp:storage-1", "test.xlog"],
        cwd=tmp_path,
        env=env,
    )
    assert rc == 1
    assert '"myapp" is not installed in the system scope' in out


def test_play_to_single_instance_without_binary_port(tt_cmd, test_instance):
    test_xlog_name = "test.xlog"
    app_name = "remote_instance_cfg"
    app_dir = test_instance._tmpdir / app_name
    app_dir.mkdir()
    shutil.copy(TEST_FILES / test_xlog_name, app_dir)
    shutil.copy(test_instance._tmpdir / INSTANCE_NAME, app_dir)
    shutil.copy(test_instance._tmpdir / "utils.lua", app_dir)

    start_app(tt_cmd, test_instance._tmpdir, app_name, start_binary_port=False)

    cmd = [tt_cmd, "data", "play", f"{app_name}:{app_name}", test_xlog_name]
    rc, cmd_output = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 1
    assert "binary port of remote_instance_cfg:remote_instance_cfg does not exist" in cmd_output

    stop_app(tt_cmd, test_instance._tmpdir, app_name)


def test_play_to_cluster_app(tt_cmd):
    # The binary port of the instance, under .tarantool/var/run/<app>/<inst>/ of
    # the project, is given to tarantool by its full path: keep it short. The
    # application is named after its directory, which the path holds twice, and
    # /tmp is /private/tmp on macOS.
    tmpdir = tempfile.mkdtemp(dir="/tmp")

    empty_file = "empty.lua"
    app_name = "play_app"
    test_xlog_name = "test.snap"
    test_app_path = Path(__file__).parent / "test_simple_cluster_app"
    tmp_app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(test_app_path, tmp_app_path)
    for item in (TEST_FILES / empty_file, TEST_FILES / test_xlog_name):
        shutil.copy(item, tmp_app_path)

    start_app(tt_cmd, tmp_app_path, app_name, True)
    try:
        master_run_path = inst_run_dir(tmp_app_path, app_name, "master")
        file = wait_file(master_run_path, control_socket, [])
        assert file != ""
        file = wait_file(master_run_path, BINARY_PORT_NAME, [])
        assert file != ""
        file = wait_file(tmp_app_path, "configured", [])
        assert file != ""

        cmd = [tt_cmd, "data", "play", app_name + ":master", test_xlog_name]
        rc, cmd_output = run_command_and_get_output(cmd, cwd=tmp_app_path)
        assert rc == 0, cmd_output

        cmd = [tt_cmd, "data", "play", app_name + ":master123", test_xlog_name]
        rc, cmd_output = run_command_and_get_output(cmd, cwd=tmp_app_path)
        assert rc == 1
        assert "master123" in cmd_output

    finally:
        stop_app(tt_cmd, tmp_app_path, app_name)
        shutil.rmtree(tmpdir)


def test_play_to_ssl_app(tt_cmd, tmpdir_with_cfg):
    skip_if_tarantool_ce()

    tmpdir = tmpdir_with_cfg
    test_app_path = Path(__file__).parent / "test_ssl_app"
    empty_file = "empty.lua"
    test_xlog_path = TEST_FILES / "test.snap"

    app_dir = Path(tmpdir) / "test_ssl_app"
    shutil.copytree(test_app_path, app_dir)
    shutil.copy(TEST_FILES / empty_file, app_dir / empty_file)

    start_app(tt_cmd, app_dir, "test_ssl_app")
    try:
        file = wait_file(app_dir, "ready", [])
        assert file != ""

        server = "localhost:3013"
        ret, output = try_execute_on_instance(tt_cmd, app_dir, server, empty_file)
        assert not ret
        assert re.search(r"   ⨯ unable to establish connection", output)

        cmd = [
            tt_cmd,
            "data",
            "play",
            "localhost:3013",
            test_xlog_path,
            "--sslkeyfile=localhost.key",
            "--sslcertfile=localhost.crt",
            "--sslcafile=ca.crt",
        ]
        rc, output = run_command_and_get_output(cmd, cwd=app_dir)
        assert rc == 0, output

    finally:
        stop_app(tt_cmd, app_dir, "test_ssl_app")


@pytest.mark.tt_app(
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
def test_play_to_cluster_app_by_uri(tt, tt_app, with_scheme, uri):
    if with_scheme:
        uri = f"tcp://{uri}"
    file_to_play = TEST_FILES / "test.snap"

    p = tt.run(
        "instance",
        "start",
        env={"TT_IPROTO_LISTEN": f'[{{"uri":"{uri}"}}]'},
    )
    assert p.returncode == 0
    assert wait_files(5, [tt_app.path("configured")])

    p = tt.run("data", "play", uri, file_to_play)
    assert p.returncode == 0
