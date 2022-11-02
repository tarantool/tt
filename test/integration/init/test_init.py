import os
import shutil
import subprocess

import yaml


def check_env_dirs(dir, instances_enabled):
    assert os.path.isdir(os.path.join(dir, "bin"))
    assert os.path.isdir(os.path.join(dir, "modules"))
    assert os.path.isdir(os.path.join(dir, "install"))
    assert os.path.isdir(os.path.join(dir, "include"))
    assert os.path.isdir(os.path.join(dir, "templates"))
    assert os.path.isdir(os.path.join(dir, instances_enabled))


def test_init_basic_functionality(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
                os.path.join(tmpdir, ".cartridge.yml"))

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert "Found existing config '.cartridge.yml'" in tt_process.stdout.readline()
    assert "Environment config is written to 'tarantool.yaml'" in tt_process.stdout.readline()

    with open(os.path.join(tmpdir, "tarantool.yaml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["tt"]["app"]["run_dir"] == "my_run_dir"
        assert data_loaded["tt"]["app"]["log_dir"] == "my_log_dir"
        assert data_loaded["tt"]["app"]["data_dir"] == "my_data_dir"
        assert data_loaded["tt"]["app"]["instances_enabled"] == "instances.enabled"

    check_env_dirs(tmpdir, "instances.enabled")


def test_init_missing_configs(tt_cmd, tmpdir):
    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert "Environment config is written to 'tarantool.yaml'" in tt_process.stdout.readline()

    with open(os.path.join(tmpdir, "tarantool.yaml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["tt"]["app"]["run_dir"] == "var/run"
        assert data_loaded["tt"]["app"]["log_dir"] == "var/log"
        assert data_loaded["tt"]["app"]["data_dir"] == "var/lib"
        assert data_loaded["tt"]["app"]["instances_enabled"] == "instances.enabled"
        assert data_loaded["tt"]["app"]["log_maxsize"] == 100
        assert data_loaded["tt"]["app"]["log_maxage"] == 8
        assert data_loaded["tt"]["app"]["log_maxbackups"] == 10
        assert data_loaded["tt"]["modules"]["directory"] == "modules"
        assert data_loaded["tt"]["app"]["bin_dir"] == "bin"
        assert data_loaded["tt"]["templates"][0]["path"] == "templates"
        assert data_loaded["tt"]["repo"]["distfiles"] == "install"
    check_env_dirs(tmpdir, "instances.enabled")


def test_init_invalid_config_file(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, ".cartridge.yml"), 'w') as stream:
        stream.write("hello")

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 1
    assert "Found existing config '.cartridge.yml'" in tt_process.stdout.readline()
    assert "failed to parse cartridge app configuration" in tt_process.stdout.readline()


def test_init_skip_config(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
                os.path.join(tmpdir, ".cartridge.yml"))

    tt_process = subprocess.Popen(
        [tt_cmd, "init", "--skip-config"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert "Environment config is written to 'tarantool.yaml'" in tt_process.stdout.readline()

    with open(os.path.join(tmpdir, "tarantool.yaml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["tt"]["app"]["run_dir"] == "var/run"
        assert data_loaded["tt"]["app"]["log_dir"] == "var/log"
        assert data_loaded["tt"]["app"]["data_dir"] == "var/lib"
        assert data_loaded["tt"]["app"]["instances_enabled"] == "instances.enabled"
    check_env_dirs(tmpdir, "instances.enabled")


def test_init_in_app_dir(tt_cmd, tmpdir):
    app_dir = os.path.join(tmpdir, "app1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "apps", "multi_inst_app"),
                    app_dir)

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert "Environment config is written to 'tarantool.yaml'" in tt_process.stdout.readline()

    with open(os.path.join(app_dir, "tarantool.yaml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["tt"]["app"]["run_dir"] == "var/run"
        assert data_loaded["tt"]["app"]["log_dir"] == "var/log"
        assert data_loaded["tt"]["app"]["data_dir"] == "var/lib"
        assert data_loaded["tt"]["app"]["instances_enabled"] == "."

    assert not os.path.exists(os.path.join(app_dir, "instances.enabled"))
    check_env_dirs(app_dir, ".")


def test_init_existing_tt_env_conf_overwrite(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
                os.path.join(tmpdir, ".cartridge.yml"))

    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as f:
        f.write('''tt:
  app:''')

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.writelines(["yes\n"])
    tt_process.stdin.close()
    tt_process.wait()

    assert tt_process.returncode == 0
    line = tt_process.stdout.readline()
    assert "tarantool.yaml already exists. Overwrite? [y/n]:" in line
    line = tt_process.stdout.readline()
    assert "Environment config is written to 'tarantool.yaml'" in line

    with open(os.path.join(tmpdir, "tarantool.yaml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["tt"]["app"]["run_dir"] == "my_run_dir"
        assert data_loaded["tt"]["app"]["log_dir"] == "my_log_dir"
        assert data_loaded["tt"]["app"]["data_dir"] == "my_data_dir"
        assert data_loaded["tt"]["app"]["instances_enabled"] == "instances.enabled"

    check_env_dirs(tmpdir, "instances.enabled")


def test_init_existing_tt_env_conf_dont_overwrite(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as f:
        f.write('''tt:
  app:''')

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.writelines(["no\n"])
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0
    line = tt_process.stdout.readline()
    assert "tarantool.yaml already exists. Overwrite? [y/n]:" in line and \
           "Environment config is written to 'tarantool.yaml'" not in line

    with open(os.path.join(tmpdir, "tarantool.yaml"), 'r') as stream:
        assert len(stream.readlines()) == 2


def test_init_existing_tt_env_conf_overwrite_force(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
                os.path.join(tmpdir, ".cartridge.yml"))

    with open(os.path.join(tmpdir, "tarantool.yaml"), "w") as f:
        f.write('''tt:
  app:''')

    tt_process = subprocess.Popen(
        [tt_cmd, "init", "-f"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()

    assert tt_process.returncode == 0
    lines = tt_process.stdout.readlines()
    assert lines[0] == "   • Found existing config '.cartridge.yml'\n"
    assert lines[1] == "   • Environment config is written to 'tarantool.yaml'\n"

    with open(os.path.join(tmpdir, "tarantool.yaml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["tt"]["app"]["run_dir"] == "my_run_dir"
        assert data_loaded["tt"]["app"]["log_dir"] == "my_log_dir"
        assert data_loaded["tt"]["app"]["data_dir"] == "my_data_dir"
        assert data_loaded["tt"]["app"]["instances_enabled"] == "instances.enabled"

    check_env_dirs(tmpdir, "instances.enabled")


def test_init_basic_tarantoolctl_app(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "configs", "tarantoolctl.lua"),
                os.path.join(tmpdir, ".tarantoolctl"))

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert "Found existing config '.tarantoolctl'" in tt_process.stdout.readline()
    assert "Environment config is written to 'tarantool.yaml'" in tt_process.stdout.readline()

    with open(os.path.join(tmpdir, "tarantool.yaml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["tt"]["app"]["run_dir"] == "/opt/run"
        assert data_loaded["tt"]["app"]["log_dir"] == "/opt/log"
        assert data_loaded["tt"]["app"]["data_dir"] == "/opt/lib"
        assert data_loaded["tt"]["app"]["instances_enabled"] == "instances.enabled"

    check_env_dirs(tmpdir, "instances.enabled")


def test_init_tarantoolctl_app_no_read_permissions(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "configs", "tarantoolctl.lua"),
                os.path.join(tmpdir, ".tarantoolctl"))

    os.chmod(os.path.join(tmpdir, ".tarantoolctl"), 0o222)
    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 1
    assert "Found existing config '.tarantoolctl'" in tt_process.stdout.readline()
    assert "⨯ tarantoolctl config loading error: LuajitError: cannot open " \
        ".tarantoolctl: Permission denied" in tt_process.stdout.readline()


def test_init_multiple_existing_configs(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "configs", "tarantoolctl.lua"),
                os.path.join(tmpdir, ".tarantoolctl"))
    shutil.copy(os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
                os.path.join(tmpdir, ".cartridge.yml"))

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    # Second config (.tarantoolctl) is skipped.
    assert "Found existing config '.cartridge.yml'" in tt_process.stdout.readline()
    assert "Environment config is written to 'tarantool.yaml'" in tt_process.stdout.readline()
