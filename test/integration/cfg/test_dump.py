import os
import shutil
import subprocess

from utils import config_name


def test_cfg_dump_default(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "tt_cfg.yaml"),
                os.path.join(tmpdir, config_name))

    buid_cmd = [tt_cmd, "cfg", "dump"]
    tt_process = subprocess.Popen(
        buid_cmd,
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
    assert "bin_dir: /usr/bin" in output
    assert "run_dir: /var/run" in output
    assert f"wal_dir: {os.path.join('lib', 'wal')}" in output
    assert f"memtx_dir: {os.path.join('lib', 'memtx')}" in output
    assert f"vinyl_dir: {os.path.join('lib', 'vinyl')}" in output
    assert f"log_dir: {os.path.join('./var', 'log')}" in output
    assert f"inc_dir: {os.path.join(tmpdir, 'include')}" in output
    assert f"directory: {os.path.join(tmpdir, 'new_modules')}" in output
    assert f"distfiles: {os.path.join(tmpdir, 'distfiles')}" in output
    assert "log_maxsize: 100" in output
    assert "log_maxbackups: 12" in output
    assert f"instances_enabled: {tmpdir}" in output
    assert f"templates:\n- path: {os.path.join(tmpdir, 'templates')}" in output
    assert 'credential_path: ""' in output


def test_cfg_dump_raw(tt_cmd, tmpdir):
    shutil.copy(os.path.join(os.path.dirname(__file__), "tt_cfg.yaml"),
                os.path.join(tmpdir, config_name))

    buid_cmd = [tt_cmd, "cfg", "dump", "--raw"]
    tt_process = subprocess.Popen(
        buid_cmd,
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
    assert output == f"""{os.path.join(tmpdir, config_name)}:
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
  log_maxbackups: 12
"""


def test_cfg_dump_no_config(tt_cmd, tmpdir):
    buid_cmd = [tt_cmd, "cfg", "dump", "--raw"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmpdir,
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


def test_cfg_dump_default_no_config(tt_cmd, tmpdir):
    dump_cmd = [tt_cmd, "cfg", "dump"]
    tt_process = subprocess.Popen(
        dump_cmd,
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
    print(output)
    assert f"bin_dir: {os.path.join(tmpdir, 'bin')}" in output
    assert f"run_dir: {os.path.join('var', 'run')}" in output
    assert f"wal_dir: {os.path.join('var', 'lib')}" in output
    assert f"vinyl_dir: {os.path.join('var', 'lib')}" in output
    assert f"memtx_dir: {os.path.join('var', 'lib')}" in output
    assert f"log_dir: {os.path.join('var', 'log')}" in output
    assert f"inc_dir: {os.path.join(tmpdir, 'include')}" in output
    assert f"directory: {os.path.join(tmpdir, 'modules')}" in output
    assert f"distfiles: {os.path.join(tmpdir, 'distfiles')}" in output
    assert "log_maxsize: 100" in output
    assert "log_maxbackups: 10" in output
    assert f"instances_enabled: {tmpdir}" in output
    assert f"templates:\n- path: {os.path.join(tmpdir, 'templates')}" in output
    assert 'credential_path: ""' in output

    # Create init.lua in current dir making it an application.

    script_path = os.path.join(tmpdir, "init.lua")
    with open(script_path, "w") as f:
        f.write('print("hello")')

    dump_cmd = [tt_cmd, "cfg", "dump"]
    tt_process = subprocess.Popen(
        dump_cmd,
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
    print(output)
    assert f"bin_dir: {os.path.join(tmpdir, 'bin')}" in output
    assert f"run_dir: {os.path.join('var', 'run')}" in output
    assert f"wal_dir: {os.path.join('var', 'lib')}" in output
    assert f"vinyl_dir: {os.path.join('var', 'lib')}" in output
    assert f"memtx_dir: {os.path.join('var', 'lib')}" in output
    assert f"log_dir: {os.path.join('var', 'log')}" in output
    assert f"inc_dir: {os.path.join(tmpdir, 'include')}" in output
    assert f"directory: {os.path.join(tmpdir, 'modules')}" in output
    assert f"distfiles: {os.path.join(tmpdir, 'distfiles')}" in output
    assert "log_maxsize: 100" in output
    assert "log_maxbackups: 10" in output
    assert "instances_enabled: ." in output
    assert f"templates:\n- path: {os.path.join(tmpdir, 'templates')}" in output
    assert 'credential_path: ""' in output
