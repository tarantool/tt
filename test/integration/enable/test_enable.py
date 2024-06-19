import os
import re
import shutil
import subprocess

import yaml


def test_enable_application(tt_cmd, tmpdir):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "test_app")
    config_path = os.path.join(tmpdir, "tt.yaml")
    instances_enabled_path = os.path.join(tmpdir, "bar")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"instances_enabled": instances_enabled_path}}, f)
    os.makedirs(instances_enabled_path)
    test_app_path = os.path.join(tmpdir, "test_app")
    shutil.copytree(test_app_path_src, test_app_path)

    # Enable test application.
    enable_cmd = [tt_cmd, "enable", test_app_path]
    enable_process = subprocess.Popen(
        enable_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    enable_process.wait()

    # Check that symlink is created.
    link_path = os.path.join(instances_enabled_path, "test_app")
    os.path.islink(link_path)

    # Check that application is enabled.
    instances_cmd = [tt_cmd, "instances"]
    instance_process = subprocess.Popen(
        instances_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    instances_output = instance_process.stdout.read()
    assert re.search("test_app", instances_output)


def test_enable_script(tt_cmd, tmpdir):
    test_script_path_src = os.path.join(os.path.dirname(__file__), "test_script", "test.lua")
    config_path = os.path.join(tmpdir, "tt.yaml")
    instances_enabled_path = os.path.join(tmpdir, "bar")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"instances_enabled": instances_enabled_path}}, f)
    os.makedirs(instances_enabled_path)
    test_script_path = os.path.join(tmpdir, "test.lua")
    shutil.copyfile(test_script_path_src, test_script_path)

    # Enable test script.
    enable_cmd = [tt_cmd, "enable", test_script_path]
    enable_process = subprocess.Popen(
        enable_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    enable_process.wait()

    # Check that symlink is created.
    link_path = os.path.join(instances_enabled_path, "test.lua")
    os.path.islink(link_path)

    # Check that script is enabled.
    instances_cmd = [tt_cmd, "instances"]
    instance_process = subprocess.Popen(
        instances_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    instances_output = instance_process.stdout.read()
    assert re.search("test.lua", instances_output)


def test_enable_invalid_path(tt_cmd, tmpdir):
    test_path = os.path.join(tmpdir)
    config_path = os.path.join(test_path, "tt.yaml")
    instances_enabled_path = os.path.join(test_path, "bar")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"instances_enabled": instances_enabled_path}}, f)
    os.makedirs(instances_enabled_path)
    test_script_path = os.path.join(tmpdir, "test.lua")

    # Enable with incorrect path.
    enable_cmd = [tt_cmd, "enable", test_script_path]
    enable_process = subprocess.Popen(
        enable_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    enable_process.wait()
    enable_output = enable_process.stdout.read()
    assert re.search("cannot get info of", enable_output)


def test_enable_no_path(tt_cmd, tmpdir):
    test_path = os.path.join(tmpdir)
    config_path = os.path.join(test_path, "tt.yaml")
    instances_enabled_path = os.path.join(test_path, "bar")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"instances_enabled": instances_enabled_path}}, f)
    os.makedirs(instances_enabled_path)

    # Enable with incorrect path.
    enable_cmd = [tt_cmd, "enable"]
    enable_process = subprocess.Popen(
        enable_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    enable_process.wait()
    enable_output = enable_process.stdout.read()
    assert re.search("provide the path to a script or application directory", enable_output)


def test_enable_instances_enable_not_exist(tt_cmd, tmpdir):
    test_script_path_src = os.path.join(os.path.dirname(__file__), "test_script", "test.lua")
    config_path = os.path.join(tmpdir, "tt.yaml")
    instances_enabled_path = os.path.join(tmpdir, "bar")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"instances_enabled": instances_enabled_path}}, f)
    test_script_path = os.path.join(tmpdir, "test.lua")
    shutil.copyfile(test_script_path_src, test_script_path)

    # Enable test script.
    enable_cmd = [tt_cmd, "enable", test_script_path]
    enable_process = subprocess.Popen(
        enable_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    enable_process.wait()
    enable_output = enable_process.stdout.read()
    assert re.search("Instances enabled directory is created", enable_output)

    # Check that symlink is created.
    link_path = os.path.join(instances_enabled_path, "test.lua")
    os.path.islink(link_path)

    # Check that script is enabled.
    instances_cmd = [tt_cmd, "instances"]
    instance_process = subprocess.Popen(
        instances_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    instances_output = instance_process.stdout.read()
    assert re.search("test.lua", instances_output)


def test_enable_invalid_script(tt_cmd, tmpdir):
    test_path = os.path.join(tmpdir)
    config_path = os.path.join(test_path, "tt.yaml")
    instances_enabled_path = os.path.join(test_path, "bar")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"instances_enabled": instances_enabled_path}}, f)
    os.makedirs(instances_enabled_path)
    test_script_path = os.path.join(tmpdir, "test")

    # Enable with incorrect script.
    enable_cmd = [tt_cmd, "enable", test_script_path]
    enable_process = subprocess.Popen(
        enable_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    enable_process.wait()
    enable_output = enable_process.stdout.read()
    assert re.search("cannot get info of", enable_output)


def test_enable_instances_enabled_dot(tt_cmd, tmpdir):
    test_path = os.path.join(tmpdir)
    config_path = os.path.join(test_path, "tt.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"instances_enabled": '.'}}, f)
    fakeApp = open(os.path.join(test_path, "init.lua"), "a")
    fakeApp.write("I am an app!")
    fakeApp.close()
    test_script_path = os.path.join(tmpdir, "test")

    # Enable with incorrect script.
    enable_cmd = [tt_cmd, "enable", test_script_path]
    enable_process = subprocess.Popen(
        enable_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    enable_process.wait()
    enable_output = enable_process.stdout.read()
    assert re.search("instances enabled '.' is not supported", enable_output)


def test_enable_application_dir_empty(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tt.yaml")
    instances_enabled_path = os.path.join(tmpdir, "bar")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"instances_enabled": instances_enabled_path}}, f)
    os.makedirs(instances_enabled_path)
    test_app_path = os.path.join(tmpdir, "notAppDir")
    os.makedirs(test_app_path)

    # Enable test application.
    enable_cmd = [tt_cmd, "enable", test_app_path]
    enable_process = subprocess.Popen(
        enable_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    enable_process.wait()

    enable_output = enable_process.stdout.read()
    assert re.search("is not an application", enable_output)
