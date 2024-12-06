import os
import shutil
import subprocess

from utils import config_name


def test_cfg_dump_default(tt_cmd, tmp_path):
    shutil.copy(os.path.join(os.path.dirname(__file__), "tt_cfg.yaml"),
                os.path.join(tmp_path, config_name))

    buid_cmd = [tt_cmd, "cfg", "dump"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()
    assert "bin_dir: /usr/bin" in output
    assert "run_dir: /var/run" in output
    assert f"wal_dir: {os.path.join('lib', 'wal')}" in output
    assert f"memtx_dir: {os.path.join('lib', 'memtx')}" in output
    assert f"vinyl_dir: {os.path.join('lib', 'vinyl')}" in output
    assert f"log_dir: {os.path.join('./var', 'log')}" in output
    assert f"inc_dir: {os.path.join(tmp_path, 'include')}" in output
    assert f"modules:\n  directory: {os.path.join(tmp_path, 'new_modules')}" in output
    assert f"distfiles: {os.path.join(tmp_path, 'distfiles')}" in output
    assert f"instances_enabled: {tmp_path}" in output
    assert f"templates:\n- path: {os.path.join(tmp_path, 'templates')}" in output
    assert 'credential_path: ""' in output


def test_cfg_dump_raw(tt_cmd, tmp_path):
    shutil.copy(os.path.join(os.path.dirname(__file__), "tt_cfg.yaml"),
                os.path.join(tmp_path, config_name))

    buid_cmd = [tt_cmd, "cfg", "dump", "--raw"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()
    assert output == f"""{os.path.join(tmp_path, config_name)}:
modules:
  directory: new_modules
app:
  run_dir: /var/run
  log_dir: ./var/log
  wal_dir: lib/wal
  vinyl_dir: lib/vinyl
  memtx_dir: lib/memtx
env:
  bin_dir: /usr/bin
"""


def test_cfg_dump_no_config(tt_cmd, tmp_path):
    buid_cmd = [tt_cmd, "cfg", "dump", "--raw"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1

    output = tt_process.stdout.read()
    assert "tt configuration file is not found" in output


def test_cfg_dump_default_no_config(tt_cmd, tmp_path):
    dump_cmd = [tt_cmd, "cfg", "dump"]
    tt_process = subprocess.Popen(
        dump_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()
    print(output)
    assert f"bin_dir: {os.path.join(tmp_path, 'bin')}" in output
    assert f"run_dir: {os.path.join('var', 'run')}" in output
    assert f"wal_dir: {os.path.join('var', 'lib')}" in output
    assert f"vinyl_dir: {os.path.join('var', 'lib')}" in output
    assert f"memtx_dir: {os.path.join('var', 'lib')}" in output
    assert f"log_dir: {os.path.join('var', 'log')}" in output
    assert f"inc_dir: {os.path.join(tmp_path, 'include')}" in output
    assert f"modules:\n  directory: {os.path.join(tmp_path, 'modules')}" in output
    assert f"distfiles: {os.path.join(tmp_path, 'distfiles')}" in output
    assert f"instances_enabled: {tmp_path}" in output
    assert f"templates:\n- path: {os.path.join(tmp_path, 'templates')}" in output
    assert 'credential_path: ""' in output

    # Create init.lua in current dir making it an application.

    script_path = os.path.join(tmp_path, "init.lua")
    with open(script_path, "w") as f:
        f.write('print("hello")')

    dump_cmd = [tt_cmd, "cfg", "dump"]
    tt_process = subprocess.Popen(
        dump_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    output = tt_process.stdout.read()
    print(output)
    assert f"bin_dir: {os.path.join(tmp_path, 'bin')}" in output
    assert f"run_dir: {os.path.join('var', 'run')}" in output
    assert f"wal_dir: {os.path.join('var', 'lib')}" in output
    assert f"vinyl_dir: {os.path.join('var', 'lib')}" in output
    assert f"memtx_dir: {os.path.join('var', 'lib')}" in output
    assert f"log_dir: {os.path.join('var', 'log')}" in output
    assert f"inc_dir: {os.path.join(tmp_path, 'include')}" in output
    assert f"modules:\n  directory: {os.path.join(tmp_path, 'modules')}" in output
    assert f"distfiles: {os.path.join(tmp_path, 'distfiles')}" in output
    assert "instances_enabled: ." in output
    assert f"templates:\n- path: {os.path.join(tmp_path, 'templates')}" in output
    assert 'credential_path: ""' in output
