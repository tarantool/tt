import os
import re
import shutil
import subprocess
import tempfile

import pytest
import yaml

from utils import (config_name, extract_status, get_tarantool_version,
                   pid_file, run_command_and_get_output, wait_event, wait_file)

tt_config_text = '''env:
  instances_enabled: test.instances.enabled
app:
  run_dir: .
  log_dir: .
templates:
  - path: ./templates'''


rendered_text = '''#! /usr/bin/env tarantool

cluster_cookie = {cookie}
user_name = {user_name}
conf_path = '/etc/{user_name}/conf.lua'
password={pwd}
attempts={retry_count}
'''


tarantool_major_version, tarantool_minor_version = get_tarantool_version()


def check_file_text(filepath, text):
    with open(filepath) as f:
        assert f.read() == text


def check_file_contains(filepath, text):
    with open(filepath) as f:
        assert text in f.read()


def create_tnt_env_in_dir(tmpdir):
    # Create env file.
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write(tt_config_text.format(tmpdir))

    os.mkdir(os.path.join(tmpdir, "test.instances.enabled"))

    # Copy templates to tmp dir.
    shutil.copytree(os.path.join(os.path.dirname(__file__), "templates"),
                    os.path.join(tmpdir, "templates"))


def test_create_basic_functionality(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    with tempfile.TemporaryDirectory(dir=tmpdir) as tmpdirname:
        create_cmd = [tt_cmd, "create", "basic", "--name", "app1"]
        tt_process = subprocess.Popen(
            create_cmd,
            cwd=tmpdirname,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            stdin=subprocess.PIPE,
            text=True
        )
        tt_process.stdin.writelines(["\n", "\n", "weak_pwd\n", "\n"])
        tt_process.stdin.close()
        tt_process.wait()
        assert tt_process.returncode == 0

        app_path = os.path.join(tmpdirname, "app1")
        # Read rendered template.
        check_file_text(os.path.join(app_path, "config.lua"),
                        rendered_text.format(cookie="cookie", user_name="admin",
                        pwd="weak_pwd", retry_count=3))

        # Make sure template file is removed.
        assert os.path.exists(os.path.join(app_path, "config.lua.tt.template")) is False

        # Check pre/post scripts were invoked.
        assert os.path.exists(os.path.join(app_path, "pre-script-invoked"))
        assert os.path.exists(os.path.join(app_path, "post-script-invoked"))

        # Check template manifest file is removed.
        assert os.path.exists(os.path.join(app_path, "MANIFEST.yaml")) is False

        # Check instantiated file name.
        assert os.path.exists(os.path.join(app_path, "admin.txt"))

        # Check origin file is removed.
        assert os.path.exists(os.path.join(app_path, "{{.user_name}}.txt")) is False

        # Check hooks directory is removed.
        assert os.path.exists(os.path.join(app_path, "hooks")) is False

        # Check temporary files is removed.
        assert os.path.exists(os.path.join(app_path, "tmp_config.cfg")) is False

        # Check "--name" value is used in file name.
        assert os.path.exists(os.path.join(app_path, "app1.cfg"))

        # Check symlink to application is created in instances enabled directory.
        assert os.path.exists(os.path.join(tmpdir, "test.instances.enabled", "app1"))
        assert os.readlink(os.path.join(tmpdir, "test.instances.enabled", "app1")) == "../" + \
            os.path.basename(tmpdirname) + "/app1"

        # Check output.
        out_lines = tt_process.stdout.readlines()

        expected_lines = [
            'Creating application in',
            'Using template from',
            'Cluster cookie (default: cookie): User name (default: admin): '
            'Password: Retry count (default: 3):    • Executing pre-hook '
            './hooks/pre-gen.sh\n',
            'Executing post-hook ./hooks/post-gen.sh\n'
        ]

    for i in range(len(expected_lines)):
        assert out_lines[i].find(expected_lines[i]) != -1


def test_vars_passed_from_cli(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    with tempfile.TemporaryDirectory(dir=tmpdir) as tmpdirname:
        create_cmd = [tt_cmd, "create", "basic", "--var", "user_name=user2", "--var",
                      "retry_count=number", "--name", "basic"]
        tt_process = subprocess.Popen(
            create_cmd,
            cwd=tmpdirname,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            stdin=subprocess.PIPE,
            text=True
        )
        tt_process.stdin.writelines(["\n", "weak_pwd\n", "5\n"])
        tt_process.stdin.close()
        tt_process.wait()
        assert tt_process.returncode == 0

        app_path = os.path.join(tmpdirname, "basic")
        # Read rendered template.
        check_file_text(os.path.join(app_path, "config.lua"),
                        rendered_text.format(cookie="cookie", user_name="user2",
                        pwd="weak_pwd", retry_count=5))

        # Make sure template file is removed.
        assert os.path.exists(os.path.join(app_path, "config.lua.tt.template")) is False

        # Check pre/post scripts were invoked.
        assert os.path.exists(os.path.join(app_path, "pre-script-invoked"))
        assert os.path.exists(os.path.join(app_path, "post-script-invoked"))

        # Check template manifest file is removed.
        assert os.path.exists(os.path.join(app_path, "MANIFEST.yaml")) is False

        # Check instantiated file name.
        assert os.path.exists(os.path.join(app_path, "user2.txt"))

        # Check origin file is removed.
        assert os.path.exists(os.path.join(app_path, "user2.txt"))

        # Check output.
        out_lines = tt_process.stdout.readlines()
        expected_lines = [
            'Creating application in',
            'Using template from',
            'Cluster cookie (default: cookie): Password: '
            'Invalid format of retry_count variable.\n', 'Retry count (default: 3):'
            '    • Executing pre-hook ./hooks/pre-gen.sh\n',
            'Executing post-hook ./hooks/post-gen.sh\n'
        ]

        for i in range(len(expected_lines)):
            assert out_lines[i].find(expected_lines[i]) != -1


def test_noninteractive_mode(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    with tempfile.TemporaryDirectory(dir=tmpdir) as tmpdirname:
        create_cmd = [tt_cmd, "create", "basic", "--var", "password=weak_pwd",
                      "--non-interactive", "--name", "basic"]
        tt_process = subprocess.Popen(
            create_cmd,
            cwd=tmpdirname,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            stdin=subprocess.PIPE,
            text=True
        )
        tt_process.stdin.close()
        tt_process.wait()

        app_path = os.path.join(tmpdirname, "basic")
        # Read rendered template.
        check_file_text(os.path.join(app_path, "config.lua"),
                        rendered_text.format(cookie="cookie", user_name="admin",
                        pwd="weak_pwd", retry_count=3))

        # Check output.
        out_lines = tt_process.stdout.readlines()
        expected_lines = [
            'Creating application in',
            'Using template from',
            'Executing pre-hook ./hooks/pre-gen.sh\n',
            'Executing post-hook ./hooks/post-gen.sh\n'
        ]

        for i in range(len(expected_lines)):
            assert out_lines[i].find(expected_lines[i]) != -1


def test_app_already_exist(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)
    os.mkdir(os.path.join(tmpdir, "app1"))
    file_in_app = os.path.join(tmpdir, "app1", "file.txt")
    with open(file_in_app, "w") as f:
        f.write("text")

    create_cmd = [tt_cmd, "create", "basic", "--name", "app1", "--var", "password=weak_pwd",
                  "--name", "app1"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1
    out_lines = tt_process.stdout.readline()
    assert out_lines.find("⨯ application app1 already exists: ") != -1

    # Check file is still there.
    assert os.path.exists(file_in_app)

    app_path = os.path.join(tmpdir, "app1")

    # Run the same create command but with force mode enabled.
    create_cmd.append("--force")
    create_cmd.append("--non-interactive")

    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    # Check file is removed.
    assert os.path.exists(file_in_app) is False

    # Read rendered template.
    check_file_text(os.path.join(app_path, "config.lua"),
                    rendered_text.format(cookie="cookie", user_name="admin",
                    pwd="weak_pwd", retry_count=3))


def run_and_check_non_interactive(tt_cmd, tmpdir, template_path, template_name, app_dir, *args):
    create_cmd = [tt_cmd, "create", template_name, "--var", "password=weak_pwd",
                  "--non-interactive", *args]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()

    app_path = app_dir
    # Read rendered template.
    check_file_text(os.path.join(app_path, "config.lua"),
                    rendered_text.format(cookie="cookie", user_name="admin",
                    pwd="weak_pwd", retry_count=3))

    # Check output.
    out_lines = tt_process.stderr.readlines()
    expected_lines = [
        'Creating application in',
        'Using template from {}'.format(template_path),
        'Executing pre-hook ./hooks/pre-gen.sh\n',
        'Executing post-hook ./hooks/post-gen.sh\n'
    ]

    for i in range(len(expected_lines)):
        assert out_lines[i].find(expected_lines[i]) != -1

    assert len(tt_process.stdout.readlines()) == 0


def test_template_as_archive(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    pack_template_cmd = ["tar", "-czvf", "../luakit.tgz", "./"]
    tar_process = subprocess.Popen(
        pack_template_cmd,
        cwd=os.path.join(tmpdir, "templates", "basic"),
    )
    tar_process.wait()
    shutil.copy(os.path.join(tmpdir, "templates", "luakit.tgz"),
                os.path.join(tmpdir, "templates", "cartridge.tar.gz"))

    run_and_check_non_interactive(tt_cmd, tmpdir, os.path.join(tmpdir, "templates", "luakit"),
                                  "luakit", os.path.join(tmpdir, "app1"), "--name", "app1")
    run_and_check_non_interactive(tt_cmd, tmpdir, os.path.join(tmpdir, "templates", "cartridge"),
                                  "cartridge", os.path.join(tmpdir, "app2"), "--name", "app2", "-s")


def test_template_search_paths(tt_cmd, tmpdir):
    # Create env file.
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write('''app:
  instances_enabled: ./any-dir
  run_dir: .
  log_dir: .
templates:
  - path: ./templates
  - path: ./templates2
  - path: ./templates3''')

    # Copy templates to tmp dir.
    shutil.copytree(os.path.join(os.path.dirname(__file__), "templates"),
                    os.path.join(tmpdir, "templates"))
    os.mkdir(os.path.join(tmpdir, "templates2"))
    os.mkdir(os.path.join(tmpdir, "templates3"))

    pack_template_cmd = ["tar", "-czvf", os.path.join(tmpdir, "templates3", "luakit.tgz"), "./"]
    tar_process = subprocess.Popen(
        pack_template_cmd,
        cwd=os.path.join(tmpdir, "templates", "basic"),
    )
    tar_process.wait()

    with tempfile.TemporaryDirectory(dir=tmpdir) as tmpdirname:
        run_and_check_non_interactive(tt_cmd, tmpdirname, os.path.join(
                                      tmpdir, "templates3", "luakit"), "luakit",
                                      os.path.join(tmpdirname, "app1"), "--name", "app1")


def test_vars_file_support(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    vars_file = os.path.join(tmpdir, "vars.txt")
    with open(vars_file, "w") as f:
        f.write("""password=my_pwd
user_name=admin
retry_count=6""")

    create_cmd = [tt_cmd, "create", "basic", "--var", "user_name=user2",
                  "--vars-file", vars_file, "--non-interactive",
                  "--name", "basic"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    app_path = os.path.join(tmpdir, "basic")
    # Check if the data is taken from vars file.
    check_file_text(os.path.join(app_path, "config.lua"),
                    rendered_text.format(cookie="cookie", user_name="user2",
                    pwd="my_pwd", retry_count=6))

    # Make sure template file is removed.
    assert os.path.exists(os.path.join(app_path, "config.lua.tt.template")) is False

    # Check pre/post scripts were invoked.
    assert os.path.exists(os.path.join(app_path, "pre-script-invoked"))
    assert os.path.exists(os.path.join(app_path, "post-script-invoked"))

    # Check template manifest file is removed.
    assert os.path.exists(os.path.join(app_path, "MANIFEST.yaml")) is False

    # Check instantiated file name.
    assert os.path.exists(os.path.join(app_path, "user2.txt"))

    # Check origin file is removed.
    assert os.path.exists(os.path.join(app_path, "user2.txt"))

    # Check output.
    out_lines = tt_process.stdout.readlines()
    expected_lines = [
        'Creating application in',
        f'Using template from {os.path.join(tmpdir, "templates", "basic")}\n',
        'Executing pre-hook ./hooks/pre-gen.sh\n',
        'Executing post-hook ./hooks/post-gen.sh\n'
    ]

    for i in range(len(expected_lines)):
        assert out_lines[i].find(expected_lines[i]) != -1


def test_create_app_in_specified_path(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    with tempfile.TemporaryDirectory(dir=tmpdir) as tmpdirname:
        run_and_check_non_interactive(tt_cmd, tmpdirname, os.path.join(
                                      tmpdir, "templates", "basic"), "basic",
                                      os.path.join(tmpdirname, "app1"), "--dst",
                                      tmpdirname, "--name", "app1")


def test_app_create_missing_required_args(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    create_cmd = [tt_cmd, "create"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1
    first_out_line = tt_process.stdout.readline()
    assert first_out_line.find("Error: requires template name argument") != -1

    create_cmd = [tt_cmd, "create", "basic"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1
    first_out_line = tt_process.stdout.readline()
    assert first_out_line.find('application name is required') != -1


def test_default_var_can_be_overwritten(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    create_cmd = [tt_cmd, "create", "basic", "--var", "password=pwd", "--non-interactive",
                  "--name", "app1", "--var", "name=my_name"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    app_path = os.path.join(tmpdir, "app1")
    assert os.path.exists(os.path.join(app_path, "my_name.cfg"))


def test_app_dir_is_not_removed_on_interrupt(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    app_path = os.path.join(tmpdir, "app1", "subdir")
    os.makedirs(os.path.join(app_path, "subdir"))

    create_cmd = [tt_cmd, "create", "basic", "--var", "user_name=user2", "--var",
                  "retry_count=number", "--name", "basic"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.writelines(["\n", "pwd\n"])
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1

    assert os.path.exists(os.path.join(app_path, "subdir"))
    assert not os.path.exists(os.path.join(app_path, "hooks"))


def test_create_basic_functionality_with_yml_manifest(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    os.rename(os.path.join(tmpdir, "templates", "basic", "MANIFEST.yaml"),
              os.path.join(tmpdir, "templates", "basic", "MANIFEST.yml"))

    with tempfile.TemporaryDirectory(dir=tmpdir) as tmpdirname:
        create_cmd = [tt_cmd, "create", "basic", "--name", "app1"]
        tt_process = subprocess.Popen(
            create_cmd,
            cwd=tmpdirname,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            stdin=subprocess.PIPE,
            text=True
        )
        tt_process.stdin.writelines(["\n", "\n", "weak_pwd\n", "\n"])
        tt_process.stdin.close()
        tt_process.wait()
        assert tt_process.returncode == 0

        app_path = os.path.join(tmpdirname, "app1")
        # Read rendered template.
        check_file_text(os.path.join(app_path, "config.lua"),
                        rendered_text.format(cookie="cookie", user_name="admin",
                        pwd="weak_pwd", retry_count=3))

        # Make sure template file is removed.
        assert os.path.exists(os.path.join(app_path, "config.lua.tt.template")) is False

        # Check pre/post scripts were invoked.
        assert os.path.exists(os.path.join(app_path, "pre-script-invoked"))
        assert os.path.exists(os.path.join(app_path, "post-script-invoked"))

        # Check template manifest file is removed.
        assert os.path.exists(os.path.join(app_path, "MANIFEST.yaml")) is False
        assert os.path.exists(os.path.join(app_path, "MANIFEST.yml")) is False

        # Check instantiated file name.
        assert os.path.exists(os.path.join(app_path, "admin.txt"))

        # Check origin file is removed.
        assert os.path.exists(os.path.join(app_path, "{{.user_name}}.txt")) is False

        # Check hooks directory is removed.
        assert os.path.exists(os.path.join(app_path, "hooks")) is False

        # Check temporary files is removed.
        assert os.path.exists(os.path.join(app_path, "tmp_config.cfg")) is False

        # Check "--name" value is used in file name.
        assert os.path.exists(os.path.join(app_path, "app1.cfg"))

        # Check output.
        out_lines = tt_process.stdout.readlines()

        expected_lines = [
            'Creating application in',
            'Using template from',
            'Cluster cookie (default: cookie): User name (default: admin): '
            'Password: Retry count (default: 3):    • Executing pre-hook '
            './hooks/pre-gen.sh\n',
            'Executing post-hook ./hooks/post-gen.sh\n'
        ]

    for i in range(len(expected_lines)):
        assert out_lines[i].find(expected_lines[i]) != -1


def test_create_ambiguous_manifest(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    shutil.copy(os.path.join(tmpdir, "templates", "basic", "MANIFEST.yaml"),
                os.path.join(tmpdir, "templates", "basic", "MANIFEST.yml"))

    with tempfile.TemporaryDirectory(dir=tmpdir) as tmpdirname:
        create_cmd = [tt_cmd, "create", "basic", "--name", "app1"]
        tt_process = subprocess.Popen(
            create_cmd,
            cwd=tmpdirname,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            stdin=subprocess.PIPE,
            text=True
        )
        tt_process.wait()
        assert tt_process.returncode == 1

        # Make sure template file is removed.
        assert os.path.exists(os.path.join(tmpdirname, "app1")) is False

        # Check output.
        out_lines = tt_process.stdout.readlines()

        expected_lines = [
            'Creating application in',
            'Using template from',
            '⨯ more than one YAML files are found:'
        ]

    for i in range(len(expected_lines)):
        assert out_lines[i].find(expected_lines[i]) != -1


def test_create_app_from_builtin_cartridge_template(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write(tt_config_text.format(tmpdir))

    create_cmd = [tt_cmd, "create", "cartridge", "--name", "app1"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.writelines(["foo\n"])
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()
    assert output.find("Application 'app1' created successfully") != -1

    app_path = os.path.join(tmpdir, "app1")
    assert os.path.exists(os.path.join(app_path, "init.lua"))
    assert os.access(os.path.join(app_path, "init.lua"), os.X_OK)
    cluster_cookie = "cluster_cookie = 'foo'"
    check_file_contains(os.path.join(app_path, "init.lua"), cluster_cookie)

    assert os.path.exists(os.path.join(app_path, "app1-scm-1.rockspec"))

    assert os.path.exists(os.path.join(app_path, "tt.pre-build"))
    assert os.access(os.path.join(app_path, "tt.pre-build"), os.X_OK)
    assert os.path.exists(os.path.join(app_path, "tt.post-build"))
    assert os.access(os.path.join(app_path, "tt.post-build"), os.X_OK)

    assert os.path.exists(os.path.join(app_path, "app", "roles", "custom.lua"))

    assert os.path.exists(os.path.join(app_path, "instances.yml"))
    assert not os.access(os.path.join(app_path, "instances.yml"), os.X_OK)
    assert os.access(os.path.join(app_path, "instances.yml"), os.W_OK)
    with open(os.path.join(app_path, "instances.yml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app1-stateboard"]["password"] == "passwd"
        assert data_loaded["app1.router"]["http_port"] == 8081


def test_create_app_from_builtin_cartridge_template_noninteractive(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write(tt_config_text.format(tmpdir))

    create_cmd = [tt_cmd, "create", "cartridge", "--name", "app1", "-s"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()
    assert output.find("Application 'app1' created successfully") != -1

    app_path = os.path.join(tmpdir, "app1")
    cluster_cookie = "cluster_cookie = 'secret-cluster-cookie'"
    check_file_contains(os.path.join(app_path, "init.lua"), cluster_cookie)


@pytest.mark.slow
@pytest.mark.skipif(tarantool_major_version >= 3,
                    reason="skip cartridge app tests for Tarantool 3.0")
def test_create_app_from_builtin_cartridge_template_with_dst_specified(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write(tt_config_text.format(tmpdir))

    create_cmd = [tt_cmd, "create", "cartridge", "--name", "app1", "--dst", "appdir"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.writelines(["\n"])
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()
    assert output.find("Build and start 'app1' application") != -1
    assert output.find("$ tt build app1") != -1
    assert output.find("$ tt start app1") != -1
    assert output.find("tt cartridge replicasets setup --bootstrap-vshard --name app1 " +
                       "--run-dir ./app1") != -1

    assert os.path.exists(os.path.join(tmpdir, "appdir"))

    app_path = os.path.join(tmpdir, "appdir", "app1")
    assert os.path.exists(os.path.join(app_path, "init.lua"))
    assert os.access(os.path.join(app_path, "init.lua"), os.X_OK)

    assert os.path.exists(os.path.join(app_path, "app1-scm-1.rockspec"))

    assert os.path.exists(os.path.join(app_path, "tt.pre-build"))
    assert os.access(os.path.join(app_path, "tt.pre-build"), os.X_OK)
    assert os.path.exists(os.path.join(app_path, "tt.post-build"))
    assert os.access(os.path.join(app_path, "tt.post-build"), os.X_OK)

    assert os.path.exists(os.path.join(app_path, "app", "roles", "custom.lua"))

    assert os.path.exists(os.path.join(app_path, "instances.yml"))
    assert not os.access(os.path.join(app_path, "instances.yml"), os.X_OK)
    assert os.access(os.path.join(app_path, "instances.yml"), os.W_OK)
    with open(os.path.join(app_path, "instances.yml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app1-stateboard"]["password"] == "passwd"
        assert data_loaded["app1.router"]["http_port"] == 8081

    target = os.readlink(os.path.join(tmpdir, "test.instances.enabled", "app1"))
    assert target == "../appdir/app1"

    create_cmd = [tt_cmd, "build", "app1"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    build_output = tt_process.stdout.readlines()
    assert "Application was successfully built" in build_output[len(build_output)-1]

    # Start cartridge app.
    start_cmd = [tt_cmd, "start", "app1"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readlines()
    for line in start_output:
        assert "Starting an instance" in line
    for inst in ["router", "s1-master", "s1-replica", "s2-master", "s2-replica"]:
        file = wait_file(os.path.join(tmpdir, "test.instances.enabled", "app1", inst),
                         pid_file, [])
        assert file != ""

    # Check status.
    status_cmd = [tt_cmd, "status", "app1"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    status_info = extract_status(status_out)
    for key in status_info.keys():
        assert status_info[key]["STATUS"] == "RUNNING"

    # Stop the cartridge app.
    stop_cmd = [tt_cmd, "stop", "app1"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"The Instance app1:\w+ \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0


@pytest.mark.parametrize("var", [
    "bucket_count=str",
    "bucket_count=-1",
    "bucket_count=0",
    "replicasets_count=0",
    "replicas_count=1",
    "routers_count=0",
])
def test_create_app_from_builtin_cartridge_template_errors(tt_cmd, tmpdir, var):
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write(tt_config_text.format(tmpdir))

    create_cmd = [tt_cmd, "create", "vshard_cluster", "--name", "app1",
                  "--non-interactive", "--var", var]
    rc, _ = run_command_and_get_output(create_cmd, cwd=tmpdir)
    assert rc != 0
    assert not os.path.exists(os.path.join(tmpdir, "app1"))


@pytest.mark.slow
@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
def test_create_app_from_builtin_vshard_cluster_template(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write(tt_config_text.format(tmpdir))

    create_cmd = [tt_cmd, "create", "vshard_cluster", "--name", "app1"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.writelines(["3000\n", "2\n", "2\n", "1\n"])
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()

    expected_lines = [
        "Build and start 'app1' application",
        "$ tt build app1",
        "$ tt start app1",
        "Application 'app1' created successfully",
        "Pay attention that default passwords were generated,",
        "you can change it in the config.yaml."
    ]
    for line in expected_lines:
        assert output.find(line) != -1

    app_path = os.path.join(tmpdir, "app1")
    assert os.path.exists(os.path.join(app_path, "storage.lua"))
    assert os.path.exists(os.path.join(app_path, "router.lua"))

    assert os.path.exists(os.path.join(app_path, "app1-scm-1.rockspec"))
    assert os.path.exists(os.path.join(app_path, "instances.yaml"))

    build_cmd = [tt_cmd, "build", "app1"]
    tt_process = subprocess.Popen(
        build_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    build_output = tt_process.stdout.readlines()
    assert "Application was successfully built" in build_output[len(build_output)-1]

    # Start vshard cluster app.
    start_cmd = [tt_cmd, "start", "app1"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readlines()
    for line in start_output:
        assert "Starting an instance" in line
    instances = ["storage-001-a", "storage-001-b", "storage-002-a",
                 "storage-002-b", "router-001-a"]
    for inst in instances:
        file = wait_file(os.path.join(tmpdir, "test.instances.enabled", "app1", inst),
                         pid_file, [])
        assert file != ""

    # Check status.
    status_cmd = [tt_cmd, "status", "app1"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    status_info = extract_status(status_out)
    for key in status_info.keys():
        assert status_info[key]["STATUS"] == "RUNNING"

    def eval_cmd_func(inst, cmd):
        connect_process = subprocess.Popen(
            [tt_cmd, "connect", f"app1:{inst}", "-f-"],
            cwd=tmpdir,
            stdin=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        connect_process.stdin.write(cmd)
        connect_process.stdin.close()
        connect_process.wait()
        return connect_process.stdout.read()

    def insert_data_func():
        out = eval_cmd_func("router-001-a", "put_sample_data()")
        return out == "---\n...\n\n"

    def select_data_func():
        out = eval_cmd_func("router-001-a", "get(1)")
        return out.find("[1, 'Elizabeth', 12]") != -1

    # Check that data can be inserted and selected.
    can_insert = wait_event(60, insert_data_func)
    can_select = False
    if can_insert:
        can_select = wait_event(60, select_data_func)

    # Print instances log to find out the reason in case of an assert fall.
    for inst in instances:
        with open(os.path.join(tmpdir, "app1", inst, "tt.log")) as f:
            print(inst, f.read())

    # Stop the vhsard_cluster app.
    stop_cmd = [tt_cmd, "stop", "app1"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    for inst in instances:
        assert re.search(rf"The Instance app1:{inst} \(PID = \d+\) has been terminated.",
                         stop_out)

    with open(os.path.join(app_path, "router-001-a", "tt.log")) as f:
        print("\n".join(f.readlines()))
    # Assert here to be sure that instances are stopped.
    assert can_insert, "can not insert data into the vshard cluster"
    assert can_select, "can not select data from the vhsard cluster"
