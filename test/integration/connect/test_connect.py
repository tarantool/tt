import os
import platform
import re
import shutil
import subprocess
import tempfile
from pathlib import Path

import psutil
import pytest

from utils import (control_socket, create_tt_config, get_tarantool_version,
                   is_tarantool_ee, kill_procs, run_command_and_get_output,
                   run_path, wait_file)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()
BINARY_PORT_NAME = "tarantool.sock"


@pytest.fixture(autouse=True)
def kill_remain_processes_wrapper(tt_cmd):
    # Run test.
    yield

    tt_proc = subprocess.Popen(
        ['pgrep', '-f', tt_cmd],
        stdout=subprocess.PIPE,
        shell=False
    )
    response = tt_proc.communicate()[0]
    procs = [psutil.Process(int(pid)) for pid in response.split()]

    kill_procs(procs)


def copy_data(dst, file_paths):
    for path in file_paths:
        shutil.copy(path, dst)


def start_app(tt_cmd, tmpdir_with_cfg, app_name, start_binary_port=False):
    test_env = os.environ.copy()
    # Set empty TT_LISTEN, so no binary port will be created.
    if start_binary_port is False:
        test_env['TT_LISTEN'] = ''

    # Start an instance.
    start_cmd = [tt_cmd, "start", app_name]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        env=test_env
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)


def stop_app(tt_cmd, tmpdir, app_name):
    stop_cmd = [tt_cmd, "stop", app_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)


def try_execute_on_instance(tt_cmd, tmpdir, instance,
                            file_path=None,
                            stdin=None,
                            stdin_as_file=False,
                            env=None,
                            opts=None,
                            args=None):
    connect_cmd = [tt_cmd, "connect", instance]

    if file_path is not None:
        connect_cmd.append("-f")
        connect_cmd.append(file_path)

    if stdin_as_file:
        connect_cmd.append("-f-")

    if opts is not None:
        for k, v in opts.items():
            connect_cmd.append(k)
            connect_cmd.append(v)

    if args is not None:
        for arg in args:
            connect_cmd.append(arg)

    instance_process = subprocess.run(
        connect_cmd,
        cwd=tmpdir,
        input=stdin,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        env=env,
    )
    return instance_process.returncode == 0, instance_process.stdout


def prepare_test_app_languages(tt_cmd, tmpdir):
    lua_file = "hello.lua"
    sql_file = "hello.sql"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_single_app", "test_app.lua")
    # The test file with Lua code.
    lua_file_path = os.path.join(os.path.dirname(__file__), "test_file", lua_file)
    # The test file with SQL code.
    sql_file_path = os.path.join(os.path.dirname(__file__), "test_file", sql_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, lua_file_path, sql_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, "test_app"), 'configured', [])
    assert file != ""
    return "test_app", lua_file, sql_file


def get_version(tt_cmd, tmpdir):
    run_cmd = [tt_cmd, "run", "-v"]
    instance_process = subprocess.run(
        run_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    if instance_process.returncode == 0:
        stdout = instance_process.stdout
        full = stdout.splitlines()[0]
        for word in re.split(r'\s', full):
            matched = re.match(r'^\d+\.\d+\.\d+', word)
            if matched:
                print("Matched:")
                print(matched)
                version = re.split(r'\.', matched.group(0))
                return True, int(version[0]), int(version[1]), int(version[2])
    return False, 0, 0, 0


def is_quit_supported(tt_cmd, tmpdir):
    ok, major, minor, patch = get_version(tt_cmd, tmpdir)
    assert ok
    return major >= 2


def is_language_supported(tt_cmd, tmpdir):
    ok, major, minor, patch = get_version(tt_cmd, tmpdir)
    assert ok
    return major >= 2


def is_cluster_app_supported(tt_cmd, tmpdir):
    ok, major, minor, patch = get_version(tt_cmd, tmpdir)
    assert ok
    return major >= 3


def is_tuple_format_supported(tt_cmd, tmpdir):
    ok, major, minor, patch = get_version(tt_cmd, tmpdir)
    assert ok
    return major > 3 or (major == 3 and minor >= 2)


def is_tarantool_major_one():
    cmd = ["tarantool", "--version"]
    instance_process = subprocess.run(
        cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    if instance_process.returncode == 0:
        return "Tarantool 1." in instance_process.stdout
    return False


def skip_if_quit_unsupported(tt_cmd, tmpdir):
    if not is_quit_supported(tt_cmd, tmpdir):
        pytest.skip("\\q is unsupported")


def skip_if_language_unsupported(tt_cmd, tmpdir, test_app):
    if not is_language_supported(tt_cmd, tmpdir):
        stop_app(tt_cmd, tmpdir, test_app)
        pytest.skip("\\set language is unsupported")


def skip_if_language_supported(tt_cmd, tmpdir, test_app):
    if is_language_supported(tt_cmd, tmpdir):
        stop_app(tt_cmd, tmpdir, test_app)
        pytest.skip("\\set language is supported")


def skip_if_tarantool_ce():
    if not is_tarantool_ee():
        pytest.skip("Tarantool Enterprise required")


def skip_if_cluster_app_unsupported(tt_cmd, tmpdir):
    if not is_cluster_app_supported(tt_cmd, tmpdir):
        pytest.skip("Tarantool 3.0 or above required")


def skip_if_tuple_format_supported(tt_cmd, tmpdir):
    if is_tuple_format_supported(tt_cmd, tmpdir):
        pytest.skip("Tuple format is supported")


def skip_if_tuple_format_unsupported(tt_cmd, tmpdir):
    if not is_tuple_format_supported(tt_cmd, tmpdir):
        pytest.skip("Tuple format is unsupported")


def test_connect_and_get_commands_outputs(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_localhost_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    commands = {}
    help_output = """
  To get help, see the Tarantool manual at https://tarantool.io/en/doc/
  To start the interactive Tarantool tutorial, type 'tutorial()' here.

  This help is expanded with additional backslash commands
  because tt connect is using.

  Available backslash commands:

  \\help, ?                        -- show this screen
  \\set language <language>        -- set language lua (default) or sql
  \\set output <format>            -- set format lua, table, ttable or yaml (default)
  \\set table_format <format>      -- set table format default, jira or markdown
  \\set graphics <false/true>      -- disables/enables pseudographics for table modes
  \\set table_column_width <width> -- set max column width for table/ttable
  \\set delimiter <marker>         -- set expression delimiter
  \\xw <width>                     -- set max column width for table/ttable
  \\x                              -- switches output format cyclically
  \\x[l,t,T,y]                     -- set output format lua, table, ttable or yaml
  \\x[g,G]                         -- disables/enables pseudographics for table modes
  \\shortcuts                      -- show available hotkeys and shortcuts
  \\quit, \\q                       -- quit from the console

"""
    commands["\\help"] = help_output
    commands["?"] = help_output
    commands["\\set output lua"] = ""
    commands["\\set output table"] = ""
    commands["\\set output ttable"] = ""
    commands["\\set output yaml"] = ""
    commands["\\set table_format default"] = ""
    commands["\\set table_format jira"] = ""
    commands["\\set table_format markdown"] = ""
    commands["\\set graphics false"] = ""
    commands["\\set graphics true"] = ""
    commands["\\set table_column_width 1"] = ""
    commands["\\set delimiter ;"] = ""
    commands["\\set delimiter"] = ""
    commands["\\xw 1"] = ""
    commands["\\x"] = ""
    commands["\\xl"] = ""
    commands["\\xt"] = ""
    commands["\\xT"] = ""
    commands["\\xy"] = ""
    commands["\\xg"] = ""
    commands["\\xG"] = ""
    commands["\\shortcuts"] = """---
- - |
    Available hotkeys and shortcuts:

       Ctrl + J / Ctrl + M [Enter] -- Enter the command
       Ctrl + A [Home]             -- Go to the beginning of the command
       Ctrl + E [End]              -- Go to the end of the command
       Ctrl + P [Up Arrow]         -- Previous command
       Ctrl + N [Down Arrow]       -- Next command
       Ctrl + F [Right Arrow]      -- Forward one character
       Ctrl + B [Left Arrow]       -- Backward one character
       Ctrl + H [Backspace]        -- Delete character before the cursor
       Ctrl + I [Tab]              -- Get next completion
       BackTab                     -- Get previous completion
       Ctrl + D                    -- Delete character under the cursor
       Ctrl + W                    -- Cut the word before the cursor
       Ctrl + K                    -- Cut the command after the cursor
       Ctrl + U                    -- Cut the command before the cursor
       Ctrl + L                    -- Clear the screen
       Ctrl + R                    -- Enter in the reverse search mode
       Ctrl + C                    -- Interrupt current unfinished expression
       Alt + B                     -- Move backwards one word
       Alt + F                     -- Move forwards one word
...
"""
    commands["\\quit"] = "   • Quit from the console    \n"
    commands["\\q"] = "   • Quit from the console    \n"

    try:
        for key, value in commands.items():
            ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013", stdin=key)
            print(output)
            assert ret

            assert output == value
    finally:
        stop_app(tt_cmd, tmpdir, "test_app")


def test_connect_and_get_commands_errors(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_localhost_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    commands = {}
    commands["\\help arg"] = "⨯ the command does not expect arguments"
    commands["? arg"] = "⨯ the command does not expect arguments"
    commands["\\set language"] = "⨯ the command expects one of: lua, sql"
    commands["\\set language arg"] = "⨯ the command expects one of: lua, sql"
    commands["\\set language arg arg"] = "⨯ the command expects one of: lua, sql"
    commands["\\set output"] = "⨯ the command expects one of: lua, table, ttable, yaml"
    commands["\\set output arg"] = "⨯ the command expects one of: lua, table, ttable, yaml"
    commands["\\set table_format"] = "⨯ the command expects one of: default, jira, markdown"
    commands["\\set table_format arg"] = "⨯ the command expects one of: default, jira, markdown"
    commands["\\set graphics"] = "⨯ the command expects one boolean"
    commands["\\set graphics arg"] = "⨯ the command expects one boolean"
    commands["\\set table_column_width"] = "⨯ the command expects one unsigned number"
    commands["\\set table_column_width arg"] = "⨯ the command expects one unsigned number"
    commands["\\set delimiter arg arg"] = "⨯ the command expects zero or single argument"
    commands["\\xw"] = "⨯ the command expects one unsigned number"
    commands["\\xw arg"] = "⨯ the command expects one unsigned number"
    commands["\\x arg"] = "⨯ the command does not expect arguments"
    commands["\\xl arg"] = "⨯ the command does not expect arguments"
    commands["\\xt arg"] = "⨯ the command does not expect arguments"
    commands["\\xT arg"] = "⨯ the command does not expect arguments"
    commands["\\xy arg"] = "⨯ the command does not expect arguments"
    commands["\\xg arg"] = "⨯ the command does not expect arguments"
    commands["\\xG arg"] = "⨯ the command does not expect arguments"
    commands["\\shortcuts arg"] = "⨯ the command does not expect arguments"
    commands["\\quit arg"] = "⨯ the command does not expect arguments"
    commands["\\q arg"] = "⨯ the command does not expect arguments"

    try:
        for key, value in commands.items():
            ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013", stdin=key)
            assert ret

            assert value in output
    finally:
        stop_app(tt_cmd, tmpdir, "test_app")


def test_connect_and_execute_quit(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    empty_file = "empty.lua"

    skip_if_quit_unsupported(tt_cmd, tmpdir)

    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_single_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"),
                     control_socket, [])
    assert file != ""

    try:
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, "test_app",
                                              stdin="\\q",
                                              stdin_as_file=True)
        assert ret

        assert output == "\n"
    finally:
        stop_app(tt_cmd, tmpdir, "test_app")


def test_connect_to_localhost_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_localhost_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to a wrong instance.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:6666", empty_file)
    assert not ret
    assert re.search(r"   ⨯ unable to establish connection", output)

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute a script.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri, empty_file)
        assert ret
        # Execute stdout.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="return ...",
                                              args=["-f-", "Hello", "World"])
        assert ret
        assert output == "---\n- Hello\n- World\n...\n\n"

        # Execute stdout without args.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="2+2")
        assert ret
        assert output == "---\n- 4\n...\n\n"

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_connect_to_ssl_app(tt_cmd, tmpdir_with_cfg):
    skip_if_tarantool_ce()

    tmpdir = tmpdir_with_cfg
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_ssl_app")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)

    # Copy test data into temporary directory.
    shutil.copytree(test_app_path, os.path.join(tmpdir, "test_ssl_app"))
    shutil.copy(empty_file_path, os.path.join(tmpdir, "test_ssl_app", empty_file))

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_ssl_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_ssl_app'), 'ready', [])
    assert file != ""

    server = "localhost:3013"
    # Connect without SSL options.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, server, empty_file)
    assert not ret
    assert re.search(r"   ⨯ unable to establish connection", output)

    # Connect to the instance.
    opts = {
        "--sslkeyfile": "test_ssl_app/localhost.key",
        "--sslcertfile": "test_ssl_app/localhost.crt",
        "--sslcafile": "test_ssl_app/ca.crt",
    }
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, server, empty_file, opts=opts)
    assert ret

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_ssl_app")


def test_connect_to_localhost_app_credentials(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_localhost_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect with a wrong credentials.
    opts = {"-u": "test", "-p": "wrong_password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013", empty_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ unable to establish connection", output)

    # Connect with a wrong credentials via URL.
    uri = "test:wrong_password@localhost:3013"
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri, empty_file)
    assert not ret
    assert re.search(r"   ⨯ unable to establish connection", output)

    # Connect with a wrong credentials via environment variables.
    env = {"TT_CLI_USERNAME": "test", "TT_CLI_PASSWORD": "wrong_password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013", empty_file, env=env)
    assert not ret
    assert re.search(r"   ⨯ unable to establish connection", output)

    # Connect with a valid credentials.
    opts = {"-u": "test", "-p": "password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013", empty_file, opts=opts)
    assert ret

    # Connect with a valid credentials via URL.
    uri = "test:password@localhost:3013"
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri, empty_file)
    assert ret

    # Connect with a valid credentials via environment variables.
    env = {"TT_CLI_USERNAME": "test", "TT_CLI_PASSWORD": "password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013", empty_file, env=env)
    assert ret

    # Connect with a valid credentials and wrong environment variables.
    env = {"TT_CLI_USERNAME": "test", "TT_CLI_PASSWORD": "wrong_password"}
    opts = {"-u": "test", "-p": "password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013",
                                          empty_file, opts=opts, env=env)
    assert ret

    # Connect with a valid credentials via URL and wrong environment variables.
    env = {"TT_CLI_USERNAME": "test", "TT_CLI_PASSWORD": "wrong_password"}
    uri = "test:password@localhost:3013"
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri, empty_file, env=env)
    assert ret

    # Connect with a valid mixes of credentials and environment variables.
    env = {"TT_CLI_PASSWORD": "password"}
    opts = {"-u": "test"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013",
                                          empty_file, opts=opts, env=env)
    assert ret

    env = {"TT_CLI_USERNAME": "test"}
    opts = {"-p": "password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:3013",
                                          empty_file, opts=opts, env=env)
    assert ret

    # Connect with a valid credentials via flags and via URL.
    opts = {"-u": "test", "-p": "password"}
    uri = "test:password@localhost:3013"
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri, empty_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ username and password are specified with flags and a URI", output)

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_connect_to_single_instance_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_single_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"),
                     control_socket, [])
    assert file != ""

    # Connect to a wrong instance.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "any_app", empty_file)
    assert not ret
    assert re.search(r"   ⨯ can\'t collect instance information for any_app", output)

    # Connect to the instance and execute a script.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "test_app", empty_file)
    assert ret

    # Connect to the instance and execute stdout.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "test_app",
                                          stdin="return ...",
                                          args=["-f-", "Hello", "World"])
    print(output)
    assert ret
    assert output == "---\n- Hello\n- World\n...\n\n"

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_connect_to_single_instance_app_credentials(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_single_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"),
                     control_socket, [])
    assert file != ""

    # Connect with a wrong credentials.
    opts = {"-u": "test", "-p": "wrong_password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "test_app", empty_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ username and password are not supported with a" +
                     " connection via a control socket", output)

    # Connect with a valid credentials.
    opts = {"-u": "test", "-p": "password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "test_app", empty_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ username and password are not supported with a" +
                     " connection via a control socket", output)

    # Connect with environment variables.
    env = {"TT_CLI_USERNAME": "test", "TT_CLI_PASSWORD": "wrong_password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "test_app", empty_file, env=env)
    assert ret

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_connect_to_multi_instances_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    instances = ['master', 'replica', 'router']
    app_name = "test_multi_app"
    empty_file = "empty.lua"
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), app_name)
    tmp_app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(test_app_path, tmp_app_path)
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [empty_file_path])

    # Start instances.
    start_app(tt_cmd, tmpdir, app_name)

    # Check for start.
    for instance in instances:
        master_run_path = os.path.join(tmpdir, app_name, run_path, instance)
        file = wait_file(master_run_path, control_socket, [])
        assert file != ""

    # Connect to a non-exist instance.
    non_exist = app_name + ":" + "any_name"
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, non_exist, empty_file)
    assert not ret
    assert re.search(rf"   ⨯ can't collect instance information for {non_exist}", output)

    # Connect to instances.
    for instance in instances:
        full_name = app_name + ":" + instance
        ret, _ = try_execute_on_instance(tt_cmd, tmpdir, full_name, empty_file)
        assert ret

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, app_name)


def test_connect_to_multi_instances_app_credentials(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_multi_app"
    empty_file = "empty.lua"
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), app_name)
    tmp_app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(test_app_path, tmp_app_path)
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [empty_file_path])

    # Start instances.
    start_app(tt_cmd, tmpdir, app_name)

    # Check for start.
    master_run_path = os.path.join(tmpdir, app_name, run_path, "master")
    file = wait_file(master_run_path, control_socket, [])
    assert file != ""

    # Connect with a wrong credentials.
    full_name = app_name + ":master"
    opts = {"-u": "test", "-p": "wrong_password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, full_name, empty_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ username and password are not supported with a" +
                     " connection via a control socket", output)

    # Connect with a valid credentials.
    full_name = app_name + ":master"
    opts = {"-u": "test", "-p": "password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, full_name, empty_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ username and password are not supported with a" +
                     " connection via a control socket", output)

    # Connect with environment variables.
    env = {"TT_CLI_USERNAME": "test", "TT_CLI_PASSWORD": "wrong_password"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, full_name, empty_file, env=env)
    assert ret

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, app_name)


def test_connect_language_default_lua(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    test_app, lua_file, sql_file = prepare_test_app_languages(tt_cmd, tmpdir)

    # Execute Lua-code.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, lua_file)
    assert ret
    assert re.search(r"Hello, world", output)

    # Execute SQL-code.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, sql_file)
    assert ret
    assert re.search(r"metadata:", output) is None

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, test_app)


def test_connect_language_lua(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    test_app, lua_file, sql_file = prepare_test_app_languages(tt_cmd, tmpdir)

    skip_if_language_unsupported(tt_cmd, tmpdir, test_app)

    # Execute Lua-code.
    opts = {"-l": "lua"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, lua_file, opts=opts)
    assert ret
    assert re.search(r"Hello, world", output)

    # Execute SQL-code.
    for lang in ["lua", "LuA", "LUA"]:
        opts = {"-l": lang}
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, sql_file, opts=opts)
        assert ret
        assert re.search(r"metadata:", output) is None

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, test_app)


def test_connect_language_sql(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    test_app, lua_file, sql_file = prepare_test_app_languages(tt_cmd, tmpdir)

    skip_if_language_unsupported(tt_cmd, tmpdir, test_app)

    # Execute Lua-code.
    opts = {"-l": "sql"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, lua_file, opts=opts)
    assert ret
    assert re.search(r"Hello, world", output) is None

    # Execute SQL-code.
    for lang in ["sql", "SqL", "SQL"]:
        opts = {"-l": lang}
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, sql_file, opts=opts)
        assert ret
        assert re.search(r"metadata:", output)

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, test_app)


def test_connect_language_l_equal_language(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    test_app, lua_file, sql_file = prepare_test_app_languages(tt_cmd, tmpdir)

    skip_if_language_unsupported(tt_cmd, tmpdir, test_app)

    for opt in ["-l", "--language"]:
        # Execute Lua-code.
        opts = {opt: "sql"}
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, lua_file, opts=opts)
        assert ret
        assert re.search(r"Hello, world", output) is None

        # Execute SQL-code.
        opts = {opt: "sql"}
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, sql_file, opts=opts)
        assert ret
        assert re.search(r"metadata:", output)

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, test_app)


def test_connect_language_invalid(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    test_app, lua_file, sql_file = prepare_test_app_languages(tt_cmd, tmpdir)

    # Execute Lua-code.
    opts = {"-l": "invalid"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, lua_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ unsupported language: invalid", output)

    # Execute SQL-code.
    opts = {"-l": "invalid"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, sql_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ unsupported language: invalid", output)

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, test_app)


def test_connect_language_set_if_unsupported(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    test_app, lua_file, sql_file = prepare_test_app_languages(tt_cmd, tmpdir)

    skip_if_language_supported(tt_cmd, tmpdir, test_app)

    # Execute Lua-code.
    opts = {"-l": "lua"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, lua_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ unable to change a language: unexpected response:", output)

    # Execute SQL-code.
    opts = {"-l": "sql"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, sql_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ unable to change a language: unexpected response:", output)

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, test_app)


def test_output_format_lua(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="2+2",
            opts={'-x': 'lua'}
        )
        assert ret
        assert output == "4;\n"

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="\n",
            opts={'-x': 'lua'}
        )
        assert ret
        assert output == ";\n"

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="1,2,3",
            opts={'-x': 'lua'}
        )
        assert ret
        assert output == "1, 2, 3;\n"

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="1,\"2\",3",
            opts={'-x': 'lua'}
        )
        assert ret
        assert output == "1, \"2\", 3;\n"

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="{1, 2,   3}",
            opts={'-x': 'lua'}
        )
        assert ret
        assert output == "{1, 2, 3};\n"

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin='{10,20,box.NULL,30},{},{box.NULL},{data="hello world"}',
            opts={'-x': 'lua'}
        )
        assert ret
        assert output == '{10, 20, nil, 30}, {}, {nil}, {data = "hello world"};\n'

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin='error("test")',
            opts={'-x': 'lua'}
        )
        assert ret
        assert output == '{error = "test"};\n'

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true})"),
            opts={'-x': 'lua'})
        assert ret
        assert output == ('{1, 21, "Flint", true};\n')

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_lua_output_format_for_tuples(tt_cmd, tmpdir_with_cfg):
    skip_if_tuple_format_unsupported(tt_cmd, tmpdir_with_cfg)

    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'lua'})
        assert ret
        assert output == ('{1, 21, "Flint", true};\n')

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,32,'Sparrow','Jack','cpt'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "{1,2,3},box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'lua'})
        assert ret
        assert output == ('{1, 21, "Flint", true}, {2, 32, "Sparrow", "Jack", '
                          '"cpt"}, {1, 2, 3}, {3, 33, "Morgan", true};\n')

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,187,'Sparrow'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'height','number'},{'name','string'}})}),"
                   "2002,{box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({4,35,'Blackbeard'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})}"),
            opts={'-x': 'lua'})
        assert ret
        assert output == ('{1, 21, "Flint"}, {2, 187, "Sparrow"}, 2002, '
                          '{{3, 33, "Morgan", true}, {4, 35, "Blackbeard"}};\n')

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',"
                   "box.tuple.new({1,21,'Flint',{data={1,2}}},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'lua'})
        assert ret
        assert output == ('{1, 21, "Flint", {1, 21, "Flint", {data = {1, 2}}}};\n')


def test_yaml_output_format_for_tuples(tt_cmd, tmpdir_with_cfg):
    skip_if_tuple_format_unsupported(tt_cmd, tmpdir_with_cfg)

    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'yaml'})
        assert ret
        assert output == ("---\n"
                          "- [1, 21, 'Flint', true]\n"
                          "...\n\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,32,'Sparrow','Jack','cpt'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "{1,2,3},box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'yaml'})
        assert ret
        assert output == ("---\n"
                          "- [1, 21, 'Flint', true]\n"
                          "- [2, 32, 'Sparrow', 'Jack', 'cpt']\n"
                          "- - 1\n"
                          "  - 2\n"
                          "  - 3\n"
                          "- [3, 33, 'Morgan', true]\n"
                          "...\n\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,187,'Sparrow'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'height','number'},{'name','string'}})}),"
                   "2002,{box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({4,35,'Blackbeard'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})}"),
            opts={'-x': 'yaml'})
        assert ret
        assert output == ("---\n"
                          "- [1, 21, 'Flint']\n"
                          "- [2, 187, 'Sparrow']\n"
                          "- 2002\n"
                          "- - [3, 33, 'Morgan', true]\n"
                          "  - [4, 35, 'Blackbeard']\n"
                          "...\n\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',"
                   "box.tuple.new({1,21,'Flint',{data={1,2}}},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'yaml'})
        assert ret
        assert output == ("---\n"
                          "- [1, 21, 'Flint', [1, 21, 'Flint', {'data': [1, 2]}]]\n"
                          "...\n\n")


def test_table_output_format(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="2+2", opts={'-x': 'table'})
        assert ret
        assert output == ("+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "| 4    |\n"
                          "+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="1,2,3", opts={'-x': 'table'})
        assert ret
        assert output == ("+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "| 1    |\n"
                          "+------+\n"
                          "| 2    |\n"
                          "+------+\n"
                          "| 3    |\n"
                          "+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{1,2,3}", opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+------+\n"
                          "| col1 | col2 | col3 |\n"
                          "+------+------+------+\n"
                          "| 1    | 2    | 3    |\n"
                          "+------+------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{10,20,30},{40,50,60},{70,80},{box.NULL,90}",
                                              opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+------+\n"
                          "| col1 | col2 | col3 |\n"
                          "+------+------+------+\n"
                          "| 10   | 20   | 30   |\n"
                          "+------+------+------+\n"
                          "| 40   | 50   | 60   |\n"
                          "+------+------+------+\n"
                          "| 70   | 80   |      |\n"
                          "+------+------+------+\n"
                          "| nil  | 90   |      |\n"
                          "+------+------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="box.tuple.new({1,100,'Mike',{data=123,'test'},{10,20}})",
            opts={'-x': 'table'})
        assert ret
        assert output == ('+------+------+------+-------------------------+---------+\n'
                          '| col1 | col2 | col3 | col4                    | col5    |\n'
                          '+------+------+------+-------------------------+---------+\n'
                          '| 1    | 100  | Mike | {"1":"test","data":123} | [10,20] |\n'
                          '+------+------+------+-------------------------+---------+\n')

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{ {10,20},{30,40} }", opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+\n"
                          "| col1 | col2 |\n"
                          "+------+------+\n"
                          "| 10   | 20   |\n"
                          "+------+------+\n"
                          "| 30   | 40   |\n"
                          "+------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{10,20},{30,40}", opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+\n"
                          "| col1 | col2 |\n"
                          "+------+------+\n"
                          "| 10   | 20   |\n"
                          "+------+------+\n"
                          "| 30   | 40   |\n"
                          "+------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{ {10,20},{30,40},true }",
                                              opts={'-x': 'table'})
        assert ret
        assert output == ("+---------+---------+------+\n"
                          "| col1    | col2    | col3 |\n"
                          "+---------+---------+------+\n"
                          "| [10,20] | [30,40] | true |\n"
                          "+---------+---------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{10,20},{30,40},true",
                                              opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+\n"
                          "| col1 | col2 |\n"
                          "+------+------+\n"
                          "| 10   | 20   |\n"
                          "+------+------+\n"
                          "| 30   | 40   |\n"
                          "+------+------+\n"
                          "+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "| true |\n"
                          "+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{data=123,'Hi'},{data=321,'My'},{qwe=11}",
                                              opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+\n"
                          "| col1 | data |\n"
                          "+------+------+\n"
                          "| Hi   | 123  |\n"
                          "+------+------+\n"
                          "| My   | 321  |\n"
                          "+------+------+\n"
                          "+-----+\n"
                          "| qwe |\n"
                          "+-----+\n"
                          "| 11  |\n"
                          "+-----+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="{data=123,'Hi'}, {data=321,'My'}," +
            "{qwe=11}, true, box.NULL, 2023, false, {10,20}, {30,40}, {50}",
            opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+\n"
                          "| col1 | data |\n"
                          "+------+------+\n"
                          "| Hi   | 123  |\n"
                          "+------+------+\n"
                          "| My   | 321  |\n"
                          "+------+------+\n"
                          "+-----+\n"
                          "| qwe |\n"
                          "+-----+\n"
                          "| 11  |\n"
                          "+-----+\n"
                          "+-------+\n"
                          "| col1  |\n"
                          "+-------+\n"
                          "| true  |\n"
                          "+-------+\n"
                          "| nil   |\n"
                          "+-------+\n"
                          "| 2023  |\n"
                          "+-------+\n"
                          "| false |\n"
                          "+-------+\n"
                          "+------+------+\n"
                          "| col1 | col2 |\n"
                          "+------+------+\n"
                          "| 10   | 20   |\n"
                          "+------+------+\n"
                          "| 30   | 40   |\n"
                          "+------+------+\n"
                          "| 50   |      |\n"
                          "+------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true})"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+-------+------+\n"
                          "| col1 | col2 | col3  | col4 |\n"
                          "+------+------+-------+------+\n"
                          "| 1    | 21   | Flint | true |\n"
                          "+------+------+-------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("{box.tuple.new({1,21,'Flint',true}),1}"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+---------------------+------+\n"
                          "| col1                | col2 |\n"
                          "+---------------------+------+\n"
                          '| [1,21,"Flint",true] | 1    |\n'
                          "+---------------------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("{box.tuple.new({1,21,'Flint',true}),{2,21,'Alex'}}"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+-------+------+\n"
                          "| col1 | col2 | col3  | col4 |\n"
                          "+------+------+-------+------+\n"
                          "| 1    | 21   | Flint | true |\n"
                          "+------+------+-------+------+\n"
                          "| 2    | 21   | Alex  |      |\n"
                          "+------+------+-------+------+\n")

        if not is_tarantool_major_one():
            # Execute stdin.
            ret, output = try_execute_on_instance(
                tt_cmd, tmpdir, uri,
                stdin="box.execute('select 1 as FOO, 30, 50, 4+4 as DATA')",
                opts={'-x': 'table'})
            assert ret
            assert output == ("+-----+----------+----------+------+\n"
                              "| FOO | COLUMN_1 | COLUMN_2 | DATA |\n"
                              "+-----+----------+----------+------+\n"
                              "| 1   | 30       | 50       | 8    |\n"
                              "+-----+----------+----------+------+\n")

            # Execute stdin.
            if (tarantool_major_version >= 3 or
               (tarantool_major_version == 2 and tarantool_minor_version >= 11)):
                select = "select * from seqscan table1"
            else:
                select = "select * from table1"

            ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                                  stdin=f"box.execute('{select}')",
                                                  opts={'-x': 'table'})
            assert ret
            assert output == ("+---------+-------------------+\n"
                              "| COLUMN1 | COLUMN2           |\n"
                              "+---------+-------------------+\n"
                              "| 10      | Hello SQL world!  |\n"
                              "+---------+-------------------+\n"
                              "| 20      | Hello LUA world!  |\n"
                              "+---------+-------------------+\n"
                              "| 30      | Hello YAML world! |\n"
                              "+---------+-------------------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="error('test')", opts={'-x': 'table'})
        assert ret
        assert output == ("+-------+\n"
                          "| error |\n"
                          "+-------+\n"
                          "| test  |\n"
                          "+-------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin=" ", opts={'-x': 'table'})
        assert ret
        assert output == ("+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "|      |\n"
                          "+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="nil", opts={'-x': 'table'})
        assert ret
        assert output == ("+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "| nil  |\n"
                          "+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{{{2+2}}}", opts={'-x': 'table'})
        assert ret
        assert output == ("+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "| [4]  |\n"
                          "+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{{{{2+2}}}}", opts={'-x': 'table'})
        assert ret
        assert output == ("+-------+\n"
                          "| col1  |\n"
                          "+-------+\n"
                          "| [[4]] |\n"
                          "+-------+\n")

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_table_output_format_for_tuples_no_format(tt_cmd, tmpdir_with_cfg):
    skip_if_tuple_format_supported(tt_cmd, tmpdir_with_cfg)

    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="box.space.customers:select()",
                                              opts={'-x': 'table'})
        assert ret
        assert output == ("+------+-----------+------+\n"
                          "| col1 | col2      | col3 |\n"
                          "+------+-----------+------+\n"
                          "| 1    | Elizabeth | 12   |\n"
                          "+------+-----------+------+\n"
                          "| 2    | Mary      | 46   |\n"
                          "+------+-----------+------+\n"
                          "| 3    | David     | 33   |\n"
                          "+------+-----------+------+\n"
                          "| 4    | William   | 81   |\n"
                          "+------+-----------+------+\n"
                          "| 5    | Jack      | 35   |\n"
                          "+------+-----------+------+\n"
                          "| 6    | William   | 25   |\n"
                          "+------+-----------+------+\n"
                          "| 7    | Elizabeth | 18   |\n"
                          "+------+-----------+------+\n")


def test_table_output_format_for_tuples(tt_cmd, tmpdir_with_cfg):
    skip_if_tuple_format_unsupported(tt_cmd, tmpdir_with_cfg)

    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="box.space.customers:select()",
                                              opts={'-x': 'table'})
        assert ret
        assert output == ("+----+-----------+-----+\n"
                          "| id | name      | age |\n"
                          "+----+-----------+-----+\n"
                          "| 1  | Elizabeth | 12  |\n"
                          "+----+-----------+-----+\n"
                          "| 2  | Mary      | 46  |\n"
                          "+----+-----------+-----+\n"
                          "| 3  | David     | 33  |\n"
                          "+----+-----------+-----+\n"
                          "| 4  | William   | 81  |\n"
                          "+----+-----------+-----+\n"
                          "| 5  | Jack      | 35  |\n"
                          "+----+-----------+-----+\n"
                          "| 6  | William   | 25  |\n"
                          "+----+-----------+-----+\n"
                          "| 7  | Elizabeth | 18  |\n"
                          "+----+-----------+-----+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+----+-----+-------+------+\n"
                          "| id | age | name  | col1 |\n"
                          "+----+-----+-------+------+\n"
                          "| 1  | 21  | Flint | true |\n"
                          "+----+-----+-------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("{box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),1}"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+---------------------+------+\n"
                          "| col1                | col2 |\n"
                          "+---------------------+------+\n"
                          '| [1,21,"Flint",true] | 1    |\n'
                          "+---------------------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("{box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),{2,21,'Alex'}}"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+------+------+-------+------+\n"
                          "| col1 | col2 | col3  | col4 |\n"
                          "+------+------+-------+------+\n"
                          "| 1    | 21   | Flint | true |\n"
                          "+------+------+-------+------+\n"
                          "| 2    | 21   | Alex  |      |\n"
                          "+------+------+-------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({12,187,'Flint',false},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'height','number'},{'name','string'}})}),"
                   "2002,{1,2,3},box.space.customers:select()"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+----+-----+-------+------+\n"
                          "| id | age | name  | col1 |\n"
                          "+----+-----+-------+------+\n"
                          "| 1  | 21  | Flint | true |\n"
                          "+----+-----+-------+------+\n"
                          "+----+--------+-------+-------+\n"
                          "| id | height | name  | col1  |\n"
                          "+----+--------+-------+-------+\n"
                          "| 12 | 187    | Flint | false |\n"
                          "+----+--------+-------+-------+\n"
                          "+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "| 2002 |\n"
                          "+------+\n"
                          "+------+------+------+\n"
                          "| col1 | col2 | col3 |\n"
                          "+------+------+------+\n"
                          "| 1    | 2    | 3    |\n"
                          "+------+------+------+\n"
                          "+----+-----------+-----+\n"
                          "| id | name      | age |\n"
                          "+----+-----------+-----+\n"
                          "| 1  | Elizabeth | 12  |\n"
                          "+----+-----------+-----+\n"
                          "| 2  | Mary      | 46  |\n"
                          "+----+-----------+-----+\n"
                          "| 3  | David     | 33  |\n"
                          "+----+-----------+-----+\n"
                          "| 4  | William   | 81  |\n"
                          "+----+-----------+-----+\n"
                          "| 5  | Jack      | 35  |\n"
                          "+----+-----------+-----+\n"
                          "| 6  | William   | 25  |\n"
                          "+----+-----------+-----+\n"
                          "| 7  | Elizabeth | 18  |\n"
                          "+----+-----------+-----+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,32,'Sparrow'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "{1,2,3},box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+----+-----+---------+------+\n"
                          "| id | age | name    | col1 |\n"
                          "+----+-----+---------+------+\n"
                          "| 1  | 21  | Flint   | true |\n"
                          "+----+-----+---------+------+\n"
                          "| 2  | 32  | Sparrow |      |\n"
                          "+----+-----+---------+------+\n"
                          "+------+------+------+\n"
                          "| col1 | col2 | col3 |\n"
                          "+------+------+------+\n"
                          "| 1    | 2    | 3    |\n"
                          "+------+------+------+\n"
                          "+----+-----+--------+------+\n"
                          "| id | age | name   | col1 |\n"
                          "+----+-----+--------+------+\n"
                          "| 3  | 33  | Morgan | true |\n"
                          "+----+-----+--------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,32,'Sparrow','Jack','cpt'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "{1,2,3},box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+----+-----+---------+------+------+\n"
                          "| id | age | name    | col1 | col2 |\n"
                          "+----+-----+---------+------+------+\n"
                          "| 1  | 21  | Flint   | true |      |\n"
                          "+----+-----+---------+------+------+\n"
                          "| 2  | 32  | Sparrow | Jack | cpt  |\n"
                          "+----+-----+---------+------+------+\n"
                          "+------+------+------+\n"
                          "| col1 | col2 | col3 |\n"
                          "+------+------+------+\n"
                          "| 1    | 2    | 3    |\n"
                          "+------+------+------+\n"
                          "+----+-----+--------+------+\n"
                          "| id | age | name   | col1 |\n"
                          "+----+-----+--------+------+\n"
                          "| 3  | 33  | Morgan | true |\n"
                          "+----+-----+--------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,187,'Sparrow'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'height','number'},{'name','string'}})}),"
                   "2002,{box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({4,35,'Blackbeard'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})}"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+----+-----+-------+\n"
                          "| id | age | name  |\n"
                          "+----+-----+-------+\n"
                          "| 1  | 21  | Flint |\n"
                          "+----+-----+-------+\n"
                          "+----+--------+---------+\n"
                          "| id | height | name    |\n"
                          "+----+--------+---------+\n"
                          "| 2  | 187    | Sparrow |\n"
                          "+----+--------+---------+\n"
                          "+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "| 2002 |\n"
                          "+------+\n"
                          "+----+-----+------------+------+\n"
                          "| id | age | name       | col1 |\n"
                          "+----+-----+------------+------+\n"
                          "| 3  | 33  | Morgan     | true |\n"
                          "+----+-----+------------+------+\n"
                          "| 4  | 35  | Blackbeard |      |\n"
                          "+----+-----+------------+------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',"
                   "box.tuple.new({1,21,'Flint',{data={1,2}}},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'table'})
        assert ret
        assert output == ("+----+-----+-------+-------------------------------+\n"
                          "| id | age | name  | col1                          |\n"
                          "+----+-----+-------+-------------------------------+\n"
                          '| 1  | 21  | Flint | [1,21,"Flint",{"data":[1,2]}] |\n'
                          "+----+-----+-------+-------------------------------+\n")


def test_ttable_output_format(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    print("\n\n")
    print(tt_cmd)
    print("\n\n")
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="2+2", opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+---+\n"
                          "| col1 | 4 |\n"
                          "+------+---+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="1,2,3", opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+---+---+---+\n"
                          "| col1 | 1 | 2 | 3 |\n"
                          "+------+---+---+---+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="{10,20,30},{40,50,60},{70,80},{box.NULL,90}",
                                              opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+----+----+----+-----+\n"
                          "| col1 | 10 | 40 | 70 | nil |\n"
                          "+------+----+----+----+-----+\n"
                          "| col2 | 20 | 50 | 80 | 90  |\n"
                          "+------+----+----+----+-----+\n"
                          "| col3 | 30 | 60 |    |     |\n"
                          "+------+----+----+----+-----+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="{data=123,'Hi'},{data=321,'My'}," +
            "{qwe=11},true,box.NULL,2023,false,{10,20},{30,40},{50}",
            opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+-----+-----+\n"
                          "| col1 | Hi  | My  |\n"
                          "+------+-----+-----+\n"
                          "| data | 123 | 321 |\n"
                          "+------+-----+-----+\n"
                          "+-----+----+\n"
                          "| qwe | 11 |\n"
                          "+-----+----+\n"
                          "+------+------+-----+------+-------+\n"
                          "| col1 | true | nil | 2023 | false |\n"
                          "+------+------+-----+------+-------+\n"
                          "+------+----+----+----+\n"
                          "| col1 | 10 | 30 | 50 |\n"
                          "+------+----+----+----+\n"
                          "| col2 | 20 | 40 |    |\n"
                          "+------+----+----+----+\n")

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_ttable_output_format_for_tuples_no_format(tt_cmd, tmpdir_with_cfg):
    skip_if_tuple_format_supported(tt_cmd, tmpdir_with_cfg)

    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="box.space.customers:select()",
                                              opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+-----------+------+-------+---------+"
                          "------+---------+-----------+\n"
                          "| col1 | 1         | 2    | 3     | 4       |"
                          " 5    | 6       | 7         |\n"
                          "+------+-----------+------+-------+---------+"
                          "------+---------+-----------+\n"
                          "| col2 | Elizabeth | Mary | David | William |"
                          " Jack | William | Elizabeth |\n"
                          "+------+-----------+------+-------+---------+"
                          "------+---------+-----------+\n"
                          "| col3 | 12        | 46   | 33    | 81      |"
                          " 35   | 25      | 18        |\n"
                          "+------+-----------+------+-------+---------+"
                          "------+---------+-----------+\n")


def test_ttable_output_format_for_tuples(tt_cmd, tmpdir_with_cfg):
    skip_if_tuple_format_unsupported(tt_cmd, tmpdir_with_cfg)

    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="box.space.customers:select()",
                                              opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+-----------+------+-------+---------+------+"
                          "---------+-----------+\n"
                          "| id   | 1         | 2    | 3     | 4       | 5    |"
                          " 6       | 7         |\n"
                          "+------+-----------+------+-------+---------+------+"
                          "---------+-----------+\n"
                          "| name | Elizabeth | Mary | David | William | Jack |"
                          " William | Elizabeth |\n"
                          "+------+-----------+------+-------+---------+------+"
                          "---------+-----------+\n"
                          "| age  | 12        | 46   | 33    | 81      | 35   |"
                          " 25      | 18        |\n"
                          "+------+-----------+------+-------+---------+------+"
                          "---------+-----------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+-------+\n"
                          "| id   | 1     |\n"
                          "+------+-------+\n"
                          "| age  | 21    |\n"
                          "+------+-------+\n"
                          "| name | Flint |\n"
                          "+------+-------+\n"
                          "| col1 | true  |\n"
                          "+------+-------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({12,187,'Flint',false},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'height','number'},{'name','string'}})}),"
                   "2002,{1,2,3},box.space.customers:select()"),
            opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+-------+\n"
                          "| id   | 1     |\n"
                          "+------+-------+\n"
                          "| age  | 21    |\n"
                          "+------+-------+\n"
                          "| name | Flint |\n"
                          "+------+-------+\n"
                          "| col1 | true  |\n"
                          "+------+-------+\n"
                          "+--------+-------+\n"
                          "| id     | 12    |\n"
                          "+--------+-------+\n"
                          "| height | 187   |\n"
                          "+--------+-------+\n"
                          "| name   | Flint |\n"
                          "+--------+-------+\n"
                          "| col1   | false |\n"
                          "+--------+-------+\n"
                          "+------+------+\n"
                          "| col1 | 2002 |\n"
                          "+------+------+\n"
                          "+------+---+\n"
                          "| col1 | 1 |\n"
                          "+------+---+\n"
                          "| col2 | 2 |\n"
                          "+------+---+\n"
                          "| col3 | 3 |\n"
                          "+------+---+\n"
                          "+------+-----------+------+-------+---------+------+"
                          "---------+-----------+\n"
                          "| id   | 1         | 2    | 3     | 4       | 5    |"
                          " 6       | 7         |\n"
                          "+------+-----------+------+-------+---------+------+"
                          "---------+-----------+\n"
                          "| name | Elizabeth | Mary | David | William | Jack |"
                          " William | Elizabeth |\n"
                          "+------+-----------+------+-------+---------+------+"
                          "---------+-----------+\n"
                          "| age  | 12        | 46   | 33    | 81      | 35   |"
                          " 25      | 18        |\n"
                          "+------+-----------+------+-------+---------+------+"
                          "---------+-----------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,32,'Sparrow','Jack','cpt'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "{1,2,3},box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})"),
            opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+-------+---------+\n"
                          "| id   | 1     | 2       |\n"
                          "+------+-------+---------+\n"
                          "| age  | 21    | 32      |\n"
                          "+------+-------+---------+\n"
                          "| name | Flint | Sparrow |\n"
                          "+------+-------+---------+\n"
                          "| col1 | true  | Jack    |\n"
                          "+------+-------+---------+\n"
                          "| col2 |       | cpt     |\n"
                          "+------+-------+---------+\n"
                          "+------+---+\n"
                          "| col1 | 1 |\n"
                          "+------+---+\n"
                          "| col2 | 2 |\n"
                          "+------+---+\n"
                          "| col3 | 3 |\n"
                          "+------+---+\n"
                          "+------+--------+\n"
                          "| id   | 3      |\n"
                          "+------+--------+\n"
                          "| age  | 33     |\n"
                          "+------+--------+\n"
                          "| name | Morgan |\n"
                          "+------+--------+\n"
                          "| col1 | true   |\n"
                          "+------+--------+\n")

        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("box.tuple.new({1,21,'Flint'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({2,187,'Sparrow'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'height','number'},{'name','string'}})}),"
                   "2002,{box.tuple.new({3,33,'Morgan',true},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})}),"
                   "box.tuple.new({4,35,'Blackbeard'},"
                   "{format=box.tuple.format.new({{'id','number'},"
                   "{'age','number'},{'name','string'}})})}"),
            opts={'-x': 'ttable'})
        assert ret
        assert output == ("+------+-------+\n"
                          "| id   | 1     |\n"
                          "+------+-------+\n"
                          "| age  | 21    |\n"
                          "+------+-------+\n"
                          "| name | Flint |\n"
                          "+------+-------+\n"
                          "+--------+---------+\n"
                          "| id     | 2       |\n"
                          "+--------+---------+\n"
                          "| height | 187     |\n"
                          "+--------+---------+\n"
                          "| name   | Sparrow |\n"
                          "+--------+---------+\n"
                          "+------+------+\n"
                          "| col1 | 2002 |\n"
                          "+------+------+\n"
                          "+------+--------+------------+\n"
                          "| id   | 3      | 4          |\n"
                          "+------+--------+------------+\n"
                          "| age  | 33     | 35         |\n"
                          "+------+--------+------------+\n"
                          "| name | Morgan | Blackbeard |\n"
                          "+------+--------+------------+\n"
                          "| col1 | true   |            |\n"
                          "+------+--------+------------+\n")


def test_output_format_round_switching(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri,
                                              stdin="\n \\x \n\n \\x \n\n \\x \n\n \\x \n\n")
        assert ret
        assert output == ("---\n"
                          "...\n"
                          "\n"
                          ";\n"
                          "+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "|      |\n"
                          "+------+\n"
                          "+------+--+\n"
                          "| col1 |  |\n"
                          "+------+--+\n"
                          "---\n"
                          "...\n"
                          "\n")

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_output_format_short_named_selecting(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin="\n \\xl \n\n \\xt \n\n \\xT \n\n \\xy \n\n")
        assert ret
        assert output == ("---\n"
                          "...\n"
                          "\n"
                          ";\n"
                          "+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "|      |\n"
                          "+------+\n"
                          "+------+--+\n"
                          "| col1 |  |\n"
                          "+------+--+\n"
                          "---\n"
                          "...\n"
                          "\n")

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_output_format_full_named_selecting(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("\n \\set output lua \n\n \\set output table \n\n"
                   " \\set output ttable \n\n \\set output yaml \n\n"
                   "\\set output \n"))
        assert ret
        assert output == ("---\n"
                          "...\n"
                          "\n"
                          ";\n"
                          "+------+\n"
                          "| col1 |\n"
                          "+------+\n"
                          "|      |\n"
                          "+------+\n"
                          "+------+--+\n"
                          "| col1 |  |\n"
                          "+------+--+\n"
                          "---\n"
                          "...\n"
                          "\n"
                          "   ⨯ the command expects one of: lua, table, ttable, yaml\n"
                          "\n")

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_output_format_tables_pseudo_graphic_disable(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=("\\xg \n \\xt \n \\xg \n"
                   "{10,20,30}, {40,50,60}, {70, 80}, {box.NULL, 1000000000} \n"
                   "\\xT \n"
                   "{10,20,30}, {40,50,60}, {70, 80}, {box.NULL, 1000000000} \n"
                   "\\xG \n\n")
                   )
        assert ret
        assert output == (" col1  col2        col3 \n"
                          " 10    20          30   \n"
                          " 40    50          60   \n"
                          " 70    80               \n"
                          " nil   1000000000       \n"
                          "\n"
                          " col1  10  40  70  nil        \n"
                          " col2  20  50  80  1000000000 \n"
                          " col3  30  60                 \n"
                          "\n"
                          "+------+--+\n"
                          "| col1 |  |\n"
                          "+------+--+\n")

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_output_format_tables_width_option(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        # Execute stdin.
        ret, output = try_execute_on_instance(
            tt_cmd, tmpdir, uri,
            stdin=(
                '\\set table_column_width 0 \n'
                '{"1234567890","123456","12345","1234"},{"1234567890","123456","12345","1234"}\n'
                '\\xw 5 \n'
                '{"1234567890","123456","12345","1234"},{"1234567890","123456","12345","1234"}\n'
                '\\xT \n'
                '{"1234567890","123456","12345","1234"},{"1234567890","123456","12345","1234"}\n'
                '\\xw -1\n'
                '\\xy \n'
                '\\set table_column_width 10 \n'
                ), opts={'-x': 'table'}
            )
        assert ret
        print(output)
        assert output == ("+------------+--------+-------+------+\n"
                          "| col1       | col2   | col3  | col4 |\n"
                          "+------------+--------+-------+------+\n"
                          "| 1234567890 | 123456 | 12345 | 1234 |\n"
                          "+------------+--------+-------+------+\n"
                          "| 1234567890 | 123456 | 12345 | 1234 |\n"
                          "+------------+--------+-------+------+\n"
                          "+-------+-------+-------+------+\n"
                          "| col1  | col2  | col3  | col4 |\n"
                          "+-------+-------+-------+------+\n"
                          "| 12345 | 12345 | 12345 | 1234 |\n"
                          "| +6789 | +6    |       |      |\n"
                          "| +0    |       |       |      |\n"
                          "+-------+-------+-------+------+\n"
                          "| 12345 | 12345 | 12345 | 1234 |\n"
                          "| +6789 | +6    |       |      |\n"
                          "| +0    |       |       |      |\n"
                          "+-------+-------+-------+------+\n"
                          "+------+-------+-------+\n"
                          "| col1 | 12345 | 12345 |\n"
                          "|      | +6789 | +6789 |\n"
                          "|      | +0    | +0    |\n"
                          "+------+-------+-------+\n"
                          "| col2 | 12345 | 12345 |\n"
                          "|      | +6    | +6    |\n"
                          "+------+-------+-------+\n"
                          "| col3 | 12345 | 12345 |\n"
                          "+------+-------+-------+\n"
                          "| col4 | 1234  | 1234  |\n"
                          "+------+-------+-------+\n"
                          "   ⨯ the command expects one unsigned number\n"
                          "\n")

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_output_format_tables_dialects(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_output_format_app",
                                                            "test_app.lua")
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")

    # Check for start.
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'ready', [])
    assert file != ""

    # Connect to the instance.
    uris = ["localhost:3013"]
    for uri in uris:
        # Connect to the instance.
        uris = ["localhost:3013", "tcp://localhost:3013"]
        for uri in uris:
            # Execute stdin.
            ret, output = try_execute_on_instance(
                tt_cmd, tmpdir, uri,
                stdin=('\\xw 5 \n \\set table_format markdown \n'
                       '{10,20,30}, {40,50,60}, {70, 80}, {box.NULL, 1000000000}\n'
                       '\\xw 0 \n'
                       '\\xT \n'
                       '{10,20,30}, {40,50,60}, {70, 80}, {box.NULL, 1000000000}\n'
                       '\\set table_format jira \n'
                       '{10,20,30}, {40,50,60}, {70, 80}, {box.NULL, 1000000000}\n'
                       '\\xt \n'
                       '{10,20,30}, {40,50,60}, {70, 80}, {box.NULL, 1000000000}\n'
                       '\\xy \n'
                       '\\set table_format jira \n'
                       ), opts={'-x': 'table'}
                )
            assert ret
            print(output)
            assert output == ("| | | |\n"
                              "|-|-|-|\n"
                              "| col1 | col2 | col3 |\n"
                              "| 10 | 20 | 30 |\n"
                              "| 40 | 50 | 60 |\n"
                              "| 70 | 80 |  |\n"
                              "| nil | 10000+0000+0 |  |\n"
                              "\n"
                              "| | | | | |\n"
                              "|-|-|-|-|-|\n"
                              "| col1 | 10 | 40 | 70 | nil |\n"
                              "| col2 | 20 | 50 | 80 | 1000000000 |\n"
                              "| col3 | 30 | 60 |  |  |\n"
                              "\n"
                              "| col1 | 10 | 40 | 70 | nil |\n"
                              "| col2 | 20 | 50 | 80 | 1000000000 |\n"
                              "| col3 | 30 | 60 |  |  |\n"
                              "\n"
                              "| col1 | col2 | col3 |\n"
                              "| 10 | 20 | 30 |\n"
                              "| 40 | 50 | 60 |\n"
                              "| 70 | 80 |  |\n"
                              "| nil | 1000000000 |  |\n"
                              "\n")

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")


def test_connect_to_single_instance_app_binary(tt_cmd):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported by test")
    tmpdir = tempfile.mkdtemp()
    create_tt_config(tmpdir, "")
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_single_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app", True)

    # Check for start.
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"),
                     BINARY_PORT_NAME, [])
    assert file != ""
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'configured', [])
    assert file != ""

    # Remove console socket
    os.remove(os.path.join(tmpdir, "test_app", run_path, "test_app", "tarantool.control"))

    # Connect to the instance and execute a script.
    try:
        connect_cmd = [tt_cmd, "connect", "test_app", "--binary", "-u", "test", "-p", "password",
                       "-f", empty_file]
        instance_process = subprocess.run(
            connect_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        assert instance_process.returncode == 0

        # Connect to the instance and execute stdin.
        connect_cmd = [tt_cmd, "connect", "test_app", "--binary", "-u", "test", "-p", "password"]
        instance_process = subprocess.run(
            connect_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
            input="2+2"
        )
        assert instance_process.returncode == 0
        assert instance_process.stdout == "---\n- 4\n...\n\n"
    finally:
        # Stop the Instance.
        stop_app(tt_cmd, tmpdir, "test_app")
        shutil.rmtree(tmpdir)


def test_connect_to_multi_instances_app_binary(tt_cmd):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported by test")
    tmpdir = tempfile.mkdtemp()
    create_tt_config(tmpdir, "")
    app_name = "test_multi_app"
    empty_file = "empty.lua"
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), app_name)
    tmp_app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(test_app_path, tmp_app_path)
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [empty_file_path])

    # Start instances.
    start_app(tt_cmd, tmpdir, app_name, True)
    try:
        # Check for start.
        instances = ['master', 'replica', 'router']
        for instance in instances:
            master_run_path = os.path.join(tmpdir, app_name, run_path, instance)
            file = wait_file(master_run_path, control_socket, [])
            assert file != ""
            file = wait_file(master_run_path, BINARY_PORT_NAME, [])
            assert file != ""
            file = wait_file(os.path.join(tmpdir, app_name), instance, [])
            assert file != ""

        # Connect to the instance and execute stdin.
        connect_cmd = [tt_cmd, "connect", app_name + ":master", "--binary",
                       "-u", "test", "-p", "password"]
        instance_process = subprocess.run(
            connect_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
            input="2+2"
        )
        assert instance_process.returncode == 0
        assert instance_process.stdout == "---\n- 4\n...\n\n"
    finally:
        # Stop the Instance.
        stop_app(tt_cmd, tmpdir, app_name)
        shutil.rmtree(tmpdir)


def test_connect_to_instance_binary_missing_port(tt_cmd):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported by test")
    tmpdir = tempfile.mkdtemp()
    create_tt_config(tmpdir, "")
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_single_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app", True)

    # Check for start.
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"),
                     BINARY_PORT_NAME, [])
    assert file != ""
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'configured', [])
    assert file != ""

    # Remove binary port.
    os.remove(os.path.join(tmpdir, "test_app", run_path, "test_app", BINARY_PORT_NAME))

    try:
        # Connect to the instance and execute stdin.
        connect_cmd = [tt_cmd, "connect", "test_app", "--binary", "-u", "test", "-p", "password"]
        instance_process = subprocess.run(
            connect_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
            input="2+2"
        )
        assert instance_process.returncode == 1
    finally:
        # Stop the Instance.
        stop_app(tt_cmd, tmpdir, "test_app")
        shutil.rmtree(tmpdir)


def test_connect_to_instance_binary_port_is_broken(tt_cmd):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported by test")
    tmpdir = tempfile.mkdtemp()
    create_tt_config(tmpdir, "")
    empty_file = "empty.lua"
    # The test application file.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_single_app", "test_app.lua")
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path, empty_file_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app", True)

    # Check for start.
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"),
                     BINARY_PORT_NAME, [])
    assert file != ""
    file = wait_file(os.path.join(tmpdir, 'test_app'), 'configured', [])
    assert file != ""

    # Remove binary port.
    os.remove(os.path.join(tmpdir, "test_app", run_path, "test_app", BINARY_PORT_NAME))
    # Create fake binary port.
    fake_binary_port = open(os.path.join(tmpdir, "test_app", run_path,
                                         "test_app", BINARY_PORT_NAME), "a")
    fake_binary_port.write("I am totally not a binary port.")
    fake_binary_port.close()

    try:
        # Connect to the instance and execute stdin.
        connect_cmd = [tt_cmd, "connect", "test_app", "--binary", "-u", "test", "-p", "password"]
        instance_process = subprocess.run(
            connect_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
            input="2+2"
        )
        assert instance_process.returncode == 1
    finally:
        # Stop the Instance.
        stop_app(tt_cmd, tmpdir, "test_app")
        shutil.rmtree(tmpdir)


def test_connect_to_cluster_app(tt_cmd):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported by test")
    tmpdir = tempfile.mkdtemp()
    create_tt_config(tmpdir, "")
    skip_if_cluster_app_unsupported(tt_cmd, tmpdir)

    empty_file = "empty.lua"
    app_name = "test_simple_cluster_app"
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), app_name)
    tmp_app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(test_app_path, tmp_app_path)
    # The test file.
    empty_file_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    # Copy test data into temporary directory.
    copy_data(tmpdir, [empty_file_path])

    # Start instances.
    start_app(tt_cmd, tmpdir, app_name, True)
    try:
        # Check for start.
        instances = ['master']
        for instance in instances:
            master_run_path = os.path.join(tmpdir, app_name, run_path, instance)
            file = wait_file(master_run_path, control_socket, [])
            assert file != ""
            file = wait_file(master_run_path, BINARY_PORT_NAME, [])
            assert file != ""
            file = wait_file(os.path.join(tmpdir, app_name), 'configured', [])
            assert file != ""

        # Connect to the instance and execute stdin.
        connect_cmd = [tt_cmd, "connect", app_name + ":master", "--binary"]
        instance_process = subprocess.run(
            connect_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
            input="2+2"
        )
        assert instance_process.returncode == 0
        assert instance_process.stdout == "---\n- 4\n...\n\n"
    finally:
        # Stop the Instance.
        stop_app(tt_cmd, tmpdir, app_name)
        shutil.rmtree(tmpdir)


@pytest.mark.parametrize(
    "instance, opts, ready_file",
    (
        ("test_app", None, Path(run_path, "test_app", control_socket)),
        (
            "localhost:3013",
            {"-u": "test", "-p": "password"},
            Path("ready"),
        ),
    ),
)
def test_set_delimiter(
    tt_cmd, tmpdir_with_cfg, instance: str, opts: None | dict, ready_file: Path
):
    input = """local a=1
a = a + 1
return a
"""
    delimiter = "</br>"
    tmpdir = Path(tmpdir_with_cfg)

    # The test application file.
    test_app_path = Path(__file__).parent / "test_localhost_app" / "test_app.lua"
    # Copy test data into temporary directory.
    copy_data(tmpdir, [test_app_path])

    # Start an instance.
    start_app(tt_cmd, tmpdir, "test_app")
    # Check for start.
    file = wait_file(tmpdir / "test_app" / ready_file.parent, ready_file.name, [])
    assert file != ""

    # Without delimiter should get an error.
    ret, output = try_execute_on_instance(
        tt_cmd,
        tmpdir,
        instance,
        opts=opts,
        stdin=input,
    )
    assert ret
    assert "attempt to perform arithmetic on global" in output

    # With delimiter expecting correct responses.
    input = f"\\set delimiter {delimiter}\n{input}{delimiter}\n"
    ret, output = try_execute_on_instance(
        tt_cmd, tmpdir, instance, opts=opts, stdin=input
    )
    assert ret
    assert (
        output
        == """---
- 2
...

"""
    )

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, "test_app")
