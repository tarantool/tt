import os
import re
import shutil
import subprocess
import tempfile

import yaml


def test_instances_enabled_apps(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_app")

    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "multi_app")
        shutil.copytree(test_app_path_src, test_app_path)

        config_path = os.path.join(test_app_path, "tt.yaml")
        with open(config_path, "w") as f:
            yaml.dump({"tt": {"env": {"instances_enabled": "."}}}, f)

        # List all instances.
        start_cmd = [tt_cmd, "instances"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        start_output = instance_process.stdout.read()
        assert re.search("app1", start_output)
        assert re.search("router", start_output)
        assert re.search("master", start_output)
        assert re.search("replica", start_output)
        assert re.search("app2", start_output)


def test_instances_no_apps(tt_cmd):
    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir)

        config_path = os.path.join(test_app_path, "tt.yaml")
        with open(config_path, "w") as f:
            yaml.dump({"tt": {"env": {"instances_enabled": "."}}}, f)

        # List all instances.
        start_cmd = [tt_cmd, "instances"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        start_output = instance_process.stdout.read()
        assert re.search("there are no enabled applications", start_output)


def test_instances_missing_directory(tt_cmd):
    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir)
        config_path = os.path.join(test_app_path, "tt.yaml")
        with open(config_path, "w") as f:
            yaml.dump({"env": {"instances_enabled": "foo/bar"}}, f)
        # List all instances.
        start_cmd = [tt_cmd, "instances"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        start_output = instance_process.stdout.read()
        assert re.search("instances enabled directory doesn't exist", start_output)


def test_instances_dot_directory_with_app(tt_cmd):
    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path_src = os.path.join(os.path.dirname(__file__), "test_app")
        test_app_path = os.path.join(tmpdir, "test_app")
        shutil.copytree(test_app_path_src, test_app_path)
        test_app_path = os.path.join(tmpdir)
        config_path = os.path.join(test_app_path, "tt.yaml")
        with open(config_path, "w") as f:
            yaml.dump({"tt": {"env": {"instances_enabled": "."}}}, f)
        # List all instances.
        start_cmd = [tt_cmd, "instances"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        start_output = instance_process.stdout.read()
        assert re.search("test_app", start_output)


def test_instances_dot_directory_with_lua_file(tt_cmd):
    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_app", "app2.lua")
        test_app_path = os.path.join(tmpdir)
        shutil.copyfile(test_app_path_src, os.path.join(test_app_path, "app2.lua"))
        config_path = os.path.join(test_app_path, "tt.yaml")
        with open(config_path, "w") as f:
            yaml.dump({"tt": {"env": {"instances_enabled": "."}}}, f)
        # List all instances.
        start_cmd = [tt_cmd, "instances"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        start_output = instance_process.stdout.read()
        assert re.search("app2", start_output)
