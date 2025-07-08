import os
import shutil
import subprocess

import pytest
import yaml

from utils import config_name


def check_env_dirs(dir, instances_enabled):
    assert os.path.isdir(os.path.join(dir, "bin"))
    assert os.path.isdir(os.path.join(dir, "modules"))
    assert os.path.isdir(os.path.join(dir, "distfiles"))
    assert os.path.isdir(os.path.join(dir, "include"))
    assert os.path.isdir(os.path.join(dir, "templates"))
    assert os.path.isdir(os.path.join(dir, instances_enabled))


def test_init_basic_functionality(tt_cmd, tmp_path):
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
        os.path.join(tmp_path, ".cartridge.yml"),
    )

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert "Found existing config '.cartridge.yml'" in tt_process.stdout.readline()
    assert f"Environment config is written to '{config_name}'" in tt_process.stdout.readline()

    with open(tmp_path / config_name, "r") as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app"]["run_dir"] == "my_run_dir"
        assert data_loaded["app"]["log_dir"] == "my_log_dir"
        assert data_loaded["app"]["wal_dir"] == "my_data_dir"
        assert data_loaded["app"]["vinyl_dir"] == "my_data_dir"
        assert data_loaded["app"]["memtx_dir"] == "my_data_dir"
        assert data_loaded["env"]["instances_enabled"] == "instances.enabled"
        assert not data_loaded["env"]["tarantoolctl_layout"]

    check_env_dirs(tmp_path, "instances.enabled")


def test_init_missing_configs(tt_cmd, tmp_path):
    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert f"Environment config is written to '{config_name}'" in tt_process.stdout.readline()

    with open(tmp_path / config_name, "r") as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app"]["run_dir"] == "var/run"
        assert data_loaded["app"]["log_dir"] == "var/log"
        assert data_loaded["app"]["wal_dir"] == "var/lib"
        assert data_loaded["app"]["vinyl_dir"] == "var/lib"
        assert data_loaded["app"]["memtx_dir"] == "var/lib"
        assert data_loaded["env"]["instances_enabled"] == "instances.enabled"
        assert not data_loaded["env"]["tarantoolctl_layout"]
        assert data_loaded["modules"]["directory"] == ["modules"]
        assert data_loaded["env"]["bin_dir"] == "bin"
        assert data_loaded["templates"][0]["path"] == "templates"
        assert data_loaded["repo"]["distfiles"] == "distfiles"
    check_env_dirs(tmp_path, "instances.enabled")


def test_init_invalid_config_file(tt_cmd, tmp_path):
    with open(tmp_path / ".cartridge.yml", "w") as stream:
        stream.write("hello")

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 1
    assert "Found existing config '.cartridge.yml'" in tt_process.stdout.readline()
    assert "failed to parse cartridge app configuration" in tt_process.stdout.readline()


def test_init_skip_config(tt_cmd, tmp_path):
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
        tmp_path / ".cartridge.yml",
    )

    tt_process = subprocess.Popen(
        [tt_cmd, "init", "--skip-config"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert f"Environment config is written to '{config_name}'" in tt_process.stdout.readline()

    with open(tmp_path / config_name, "r") as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app"]["run_dir"] == "var/run"
        assert data_loaded["app"]["log_dir"] == "var/log"
        assert data_loaded["app"]["wal_dir"] == "var/lib"
        assert data_loaded["app"]["vinyl_dir"] == "var/lib"
        assert data_loaded["app"]["memtx_dir"] == "var/lib"
        assert data_loaded["env"]["instances_enabled"] == "instances.enabled"
    check_env_dirs(tmp_path, "instances.enabled")


def test_init_in_app_dir(tt_cmd, tmp_path):
    app_dir = tmp_path / "app1"
    shutil.copytree(os.path.join(os.path.dirname(__file__), "apps", "multi_inst_app"), app_dir)

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert f"Environment config is written to '{config_name}'" in tt_process.stdout.readline()

    with open(os.path.join(app_dir, config_name), "r") as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app"]["run_dir"] == "var/run"
        assert data_loaded["app"]["log_dir"] == "var/log"
        assert data_loaded["app"]["wal_dir"] == "var/lib"
        assert data_loaded["app"]["vinyl_dir"] == "var/lib"
        assert data_loaded["app"]["memtx_dir"] == "var/lib"
        assert data_loaded["env"]["instances_enabled"] == "."

    assert not os.path.exists(os.path.join(app_dir, "instances.enabled"))
    check_env_dirs(app_dir, ".")


def test_init_existing_tt_env_conf_overwrite(tt_cmd, tmp_path):
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
        os.path.join(tmp_path, ".cartridge.yml"),
    )

    with open(os.path.join(tmp_path, config_name), "w") as f:
        f.write("""app:""")

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.stdin.writelines(["yes\n"])
    tt_process.stdin.close()
    tt_process.wait()

    assert tt_process.returncode == 0
    line = tt_process.stdout.readline()
    assert f"{config_name} already exists. Overwrite? [y/n]:" in line
    line = tt_process.stdout.readline()
    assert f"Environment config is written to '{config_name}'" in line

    with open(os.path.join(tmp_path, "tt.yaml"), "r") as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app"]["run_dir"] == "my_run_dir"
        assert data_loaded["app"]["log_dir"] == "my_log_dir"
        assert data_loaded["app"]["wal_dir"] == "my_data_dir"
        assert data_loaded["app"]["vinyl_dir"] == "my_data_dir"
        assert data_loaded["app"]["memtx_dir"] == "my_data_dir"
        assert data_loaded["env"]["instances_enabled"] == "instances.enabled"

    check_env_dirs(tmp_path, "instances.enabled")


def test_init_existing_tt_env_conf_dont_overwrite(tt_cmd, tmp_path):
    with open(os.path.join(tmp_path, config_name), "w") as f:
        f.write("""app:""")

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.stdin.writelines(["no\n"])
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0
    line = tt_process.stdout.readline()
    assert (
        f"{config_name} already exists. Overwrite? [y/n]:" in line
        and f"Environment config is written to '{config_name}'" not in line
    )

    with open(os.path.join(tmp_path, "tt.yaml"), "r") as stream:
        assert len(stream.readlines()) == 1


def test_init_existing_tt_env_conf_overwrite_force(tt_cmd, tmp_path):
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
        os.path.join(tmp_path, ".cartridge.yml"),
    )

    with open(os.path.join(tmp_path, config_name), "w") as f:
        f.write("""app:""")

    tt_process = subprocess.Popen(
        [tt_cmd, "init", "-f"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()

    assert tt_process.returncode == 0
    lines = tt_process.stdout.readlines()
    assert lines[0] == "   • Found existing config '.cartridge.yml'\n"
    assert lines[1] == f"   • Environment config is written to '{config_name}'\n"

    with open(os.path.join(tmp_path, config_name), "r") as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app"]["run_dir"] == "my_run_dir"
        assert data_loaded["app"]["log_dir"] == "my_log_dir"
        assert data_loaded["app"]["wal_dir"] == "my_data_dir"
        assert data_loaded["app"]["vinyl_dir"] == "my_data_dir"
        assert data_loaded["app"]["memtx_dir"] == "my_data_dir"
        assert data_loaded["env"]["instances_enabled"] == "instances.enabled"

    check_env_dirs(tmp_path, "instances.enabled")


def test_init_basic_tarantoolctl_cfg(tt_cmd, tmp_path):
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "tarantoolctl.lua"),
        os.path.join(tmp_path, ".tarantoolctl"),
    )

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert "Found existing config '.tarantoolctl'" in tt_process.stdout.readline()
    assert f"Environment config is written to '{config_name}'" in tt_process.stdout.readline()

    with open(os.path.join(tmp_path, config_name), "r") as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app"]["run_dir"] == "/opt/run"
        assert data_loaded["app"]["log_dir"] == "/opt/log"
        assert data_loaded["app"]["wal_dir"] == "/opt/wal"
        assert data_loaded["app"]["vinyl_dir"] == "/opt/vinyl"
        assert data_loaded["app"]["memtx_dir"] == "/opt/snap"
        assert data_loaded["env"]["instances_enabled"] == "instances.enabled"
        assert data_loaded["env"]["tarantoolctl_layout"]

    check_env_dirs(tmp_path, "instances.enabled")


# cSpell:words ttctl
def test_tarantoolctl_cfg_from_doc(tt_cmd, tmp_path):
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "default_ttctl_cfg_from_doc.lua"),
        os.path.join(tmp_path, ".tarantoolctl"),
    )

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    assert "Found existing config '.tarantoolctl'" in tt_process.stdout.readline()
    assert f"Environment config is written to '{config_name}'" in tt_process.stdout.readline()

    with open(os.path.join(tmp_path, config_name), "r") as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["app"]["run_dir"] == "./run/tarantool"
        assert data_loaded["app"]["log_dir"] == "./log/tarantool"
        assert data_loaded["app"]["wal_dir"] == "./lib/tarantool"
        assert data_loaded["app"]["vinyl_dir"] == "./lib/tarantool"
        assert data_loaded["app"]["memtx_dir"] == "./lib/tarantool"
        assert data_loaded["env"]["instances_enabled"] == "./instances"
        assert data_loaded["env"]["tarantoolctl_layout"]

    check_env_dirs(tmp_path, "instances")


@pytest.mark.skipif(
    os.getuid() == 0,
    reason="Skipping the test, it shouldn't run as root",
)
def test_init_tarantoolctl_app_no_read_permissions(tt_cmd, tmp_path):
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "tarantoolctl.lua"),
        os.path.join(tmp_path, ".tarantoolctl"),
    )

    os.chmod(os.path.join(tmp_path, ".tarantoolctl"), 0o222)
    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 1
    assert "Found existing config '.tarantoolctl'" in tt_process.stdout.readline()
    assert (
        "⨯ tarantoolctl config loading error: LuajitError: cannot open "
        ".tarantoolctl: Permission denied" in tt_process.stdout.readline()
    )


def test_init_multiple_existing_configs(tt_cmd, tmp_path):
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "tarantoolctl.lua"),
        os.path.join(tmp_path, ".tarantoolctl"),
    )
    shutil.copy(
        os.path.join(os.path.dirname(__file__), "configs", "valid_cartridge.yml"),
        os.path.join(tmp_path, ".cartridge.yml"),
    )

    tt_process = subprocess.Popen(
        [tt_cmd, "init"],
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 0
    # Second config (.tarantoolctl) is skipped.
    assert "Found existing config '.cartridge.yml'" in tt_process.stdout.readline()
    assert f"Environment config is written to '{config_name}'" in tt_process.stdout.readline()
