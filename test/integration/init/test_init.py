import os
import shutil
import subprocess

import yaml


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
    assert "Environment config is written to 'tarantool.yaml'" in tt_process.stdout.readline()

    with open(os.path.join(tmpdir, "tarantool.yaml"), 'r') as stream:
        data_loaded = yaml.safe_load(stream)
        assert data_loaded["tt"]["app"]["run_dir"] == "my_run_dir"
        assert data_loaded["tt"]["app"]["log_dir"] == "my_log_dir"
        assert data_loaded["tt"]["app"]["data_dir"] == "my_data_dir"


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
        assert data_loaded["tt"]["app"]["instances_enabled"] == "."
        assert data_loaded["tt"]["app"]["log_maxsize"] == 64
        assert data_loaded["tt"]["app"]["log_maxage"] == 8
        assert data_loaded["tt"]["app"]["log_maxbackups"] == 64
        assert data_loaded["tt"]["modules"]["directory"] == "env/modules"
        assert data_loaded["tt"]["app"]["bin_dir"] == os.path.dirname(shutil.which("tarantool"))


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
