import os
import re
import shutil
import subprocess

import pytest
from utils import run_command_and_get_output, wait_file


def copy_data(dst, file_paths):
    for path in file_paths:
        shutil.copy(path, dst)


def start_app(tt_cmd, tmpdir_with_cfg, app_name):
    # Start an instance.
    start_cmd = [tt_cmd, "start", app_name]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)


def stop_app(tt_cmd, tmpdir, app_name):
    stop_cmd = [tt_cmd, "stop", app_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)


def try_execute_on_instance(tt_cmd, tmpdir, instance, file_path, opts=None):
    connect_cmd = [tt_cmd, "connect", instance, "-f", file_path]
    if opts is not None:
        for k, v in opts.items():
            connect_cmd.append(k)
            connect_cmd.append(v)

    instance_process = subprocess.run(
        connect_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
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
    file = wait_file(tmpdir + "/run/test_app/", 'test_app.control', [])
    assert file != ""
    return "test_app", lua_file, sql_file


def get_version(tt_cmd, tmpdir, app_name):
    run_cmd = [tt_cmd, "run", app_name, "-v"]
    instance_process = subprocess.run(
        run_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    if instance_process.returncode == 0:
        stdout = instance_process.stdout
        full = stdout.splitlines()[1]
        for word in re.split(r'\s', full):
            matched = re.match(r'^\d+\.\d+\.\d+', word)
            if matched:
                print("Matched:")
                print(matched)
                version = re.split(r'\.', matched.group(0))
                return True, int(version[0]), int(version[1]), int(version[2])
    return False, 0, 0, 0


def is_language_supported(tt_cmd, tmpdir, test_app):
    ok, major, minor, patch = get_version(tt_cmd, tmpdir, test_app)
    assert ok
    return major >= 2


def skip_if_language_unsupported(tt_cmd, tmpdir, test_app):
    if not is_language_supported(tt_cmd, tmpdir, test_app):
        pytest.skip("/set language is unsupported")


def skip_if_language_supported(tt_cmd, tmpdir, test_app):
    if is_language_supported(tt_cmd, tmpdir, test_app):
        pytest.skip("/set language is supported")


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
    file = wait_file(tmpdir, 'ready', [])
    assert file != ""

    # Connect to a wrong instance.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:6666", empty_file)
    assert not ret
    assert re.search(r"   ⨯ Unable to establish connection", output)

    # Connect to the instance.
    uris = ["localhost:3013", "tcp:localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri, empty_file)
        assert ret

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
    file = wait_file(tmpdir + "/run/test_app/", 'test_app.control', [])
    assert file != ""

    # Connect to a wrong instance.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "any_app", empty_file)
    assert not ret
    assert re.search(r"   ⨯ Can't find an application init file", output)

    # Connect to the instance.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "test_app", empty_file)
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
        master_run_path = os.path.join(tmpdir, "run", app_name, instance)
        file = wait_file(master_run_path, instance + ".control", [])
        assert file != ""

    # Connect to a non-exist instance.
    non_exist = app_name + ":" + "any_name"
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, non_exist, empty_file)
    assert not ret
    assert re.search(r"   ⨯ Can't find an application init file: instance\(s\) not found", output)

    # Connect to instances.
    for instance in instances:
        full_name = app_name + ":" + instance
        ret, _ = try_execute_on_instance(tt_cmd, tmpdir, full_name, empty_file)
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
    assert re.search(r"   ⨯ Unsupported language: invalid", output)

    # Execute SQL-code.
    opts = {"-l": "invalid"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, sql_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ Unsupported language: invalid", output)

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
    assert re.search(r"   ⨯ Unable to change a language: Unexpected response:", output)

    # Execute SQL-code.
    opts = {"-l": "sql"}
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, test_app, sql_file, opts=opts)
    assert not ret
    assert re.search(r"   ⨯ Unable to change a language: Unexpected response:", output)

    # Stop the Instance.
    stop_app(tt_cmd, tmpdir, test_app)
