import os
import shutil
import subprocess
import tempfile

import yaml

tt_config_text = '''tt:
  app:
    instances_enabled: test.instances.enabled
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


def check_file_text(filepath, text):
    with open(filepath) as f:
        assert f.read() == text


def create_tnt_env_in_dir(tmpdir):
    # Create env file.
    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as tnt_env_file:
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

        # Check default Dockerfile is created.
        assert os.path.exists(os.path.join(app_path, "Dockerfile.build.tt"))

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
        stderr=subprocess.STDOUT,
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
    out_lines = tt_process.stdout.readlines()
    expected_lines = [
        'Creating application in',
        'Using template from {}'.format(template_path),
        'Executing pre-hook ./hooks/pre-gen.sh\n',
        'Executing post-hook ./hooks/post-gen.sh\n'
    ]

    for i in range(len(expected_lines)):
        assert out_lines[i].find(expected_lines[i]) != -1


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
                                  "cartridge", os.path.join(tmpdir, "app2"), "--name", "app2")


def test_template_search_paths(tt_cmd, tmpdir):
    # Create env file.
    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as tnt_env_file:
        tnt_env_file.write('''tt:
  app:
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
    assert first_out_line.find('Error: required flag(s) "name" not set') != -1


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

        # Check default Dockerfile is created.
        assert os.path.exists(os.path.join(app_path, "Dockerfile.build.tt"))

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
    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as tnt_env_file:
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
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()
    assert output.find("Build and start 'app1' application") != -1
    assert output.find("./app1") != -1
    assert output.find("tt cartridge replicasets setup --bootstrap-vshard") != -1

    app_path = os.path.join(tmpdir, "app1")
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
