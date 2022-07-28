import os
import shutil
import subprocess

tt_config_text = '''tt:
  app:
    instances_available: {}
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

    # Copy templates to tmp dir.
    shutil.copytree(os.path.join(os.path.dirname(__file__), "templates"),
                    os.path.join(tmpdir, "templates"))


def test_create_basic_functionality(tt_cmd, tmpdir):
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
    tt_process.stdin.writelines(["\n", "\n", "weak_pwd\n", "\n"])
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    app_path = os.path.join(tmpdir, "basic")
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

    # Check output.
    out_lines = tt_process.stdout.readlines()
    expected_lines = [
        'Creating application in',
        'Using template from templates/basic\n',
        'Cluster cookie (default: cookie): User name (default: admin): '
        'Password: Retry count (default: 3):    • Executing pre-hook '
        './hooks/pre-gen.sh\n',
        'Executing post-hook ./hooks/post-gen.sh\n'
    ]

    for i in range(len(expected_lines)):
        assert out_lines[i].find(expected_lines[i]) != -1


def test_vars_passed_from_cli(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    create_cmd = [tt_cmd, "create", "--var", "user_name=user2", "--var", "retry_count=number"]
    tt_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.writelines(["\n", "weak_pwd\n", "5\n"])
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    app_path = os.path.join(tmpdir, "basic")
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
        'Using template from templates/basic\n',
        'Cluster cookie (default: cookie): Password: '
        'Invalid format of retry_count variable.\n', 'Retry count (default: 3):'
        '    • Executing pre-hook ./hooks/pre-gen.sh\n',
        'Executing post-hook ./hooks/post-gen.sh\n'
    ]

    for i in range(len(expected_lines)):
        assert out_lines[i].find(expected_lines[i]) != -1


def test_noninteractive_mode(tt_cmd, tmpdir):
    create_tnt_env_in_dir(tmpdir)

    create_cmd = [tt_cmd, "create", "--var", "password=weak_pwd", "--non-interactive"]
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

    app_path = os.path.join(tmpdir, "basic")
    # Read rendered template.
    check_file_text(os.path.join(app_path, "config.lua"),
                    rendered_text.format(cookie="cookie", user_name="admin",
                    pwd="weak_pwd", retry_count=3))

    # Check output.
    out_lines = tt_process.stdout.readlines()
    expected_lines = [
        'Creating application in',
        'Using template from templates/basic\n',
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

    create_cmd = [tt_cmd, "create", "--name", "app1", "--var", "password=weak_pwd"]
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
    assert out_lines.find("⨯ Application app1 already exists: ") != -1

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


def run_and_check_non_interactive(tt_cmd, tmpdir, template_path, template_name):
    create_cmd = [tt_cmd, "create", template_name, "--var", "password=weak_pwd", "--non-interactive"]
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

    app_path = os.path.join(tmpdir, template_name)
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

    run_and_check_non_interactive(tt_cmd, tmpdir, "templates/luakit", "luakit")
    run_and_check_non_interactive(tt_cmd, tmpdir, "templates/cartridge", "cartridge")


def test_template_search_paths(tt_cmd, tmpdir):
    # Create env file.
    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as tnt_env_file:
        tnt_env_file.write('''tt:
  app:
    instances_available: {}
    run_dir: .
    log_dir: .
  templates:
    - path: ./templates
    - path: ./templates2
    - path: ./templates3'''.format(tmpdir))

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

    run_and_check_non_interactive(tt_cmd, tmpdir, "templates3/luakit", "luakit")
