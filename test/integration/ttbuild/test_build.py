import os
import shutil
import subprocess
import tempfile

import pytest

from utils import config_name


def test_build_no_options(tt_cmd, tmpdir_with_cfg):
    app_dir = shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/app1"),
                              os.path.join(tmpdir_with_cfg, "app1"))

    buid_cmd = [tt_cmd, "build"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    print(tt_process.stdout.read())
    assert tt_process.returncode == 0

    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))


def test_build_with_tt_hooks(tt_cmd, tmpdir_with_cfg):
    app_dir = shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/app1"),
                              os.path.join(tmpdir_with_cfg, "app1"))
    shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/tt_hooks"),
                    os.path.join(tmpdir_with_cfg, "app1"), dirs_exist_ok=True)

    buid_cmd = [tt_cmd, "build"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))
    assert os.path.exists(os.path.join(app_dir, "tt-pre-build-invoked"))
    assert os.path.exists(os.path.join(app_dir, "tt-post-build-invoked"))


def test_build_with_cartridge_hooks(tt_cmd, tmpdir_with_cfg):
    app_dir = shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/app1"),
                              os.path.join(tmpdir_with_cfg, "app1"))
    shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/cartridge_hooks"),
                    os.path.join(tmpdir_with_cfg, "app1"), dirs_exist_ok=True)

    buid_cmd = [tt_cmd, "build"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))
    assert os.path.exists(os.path.join(app_dir, "cartridge-pre-build-invoked"))
    assert os.path.exists(os.path.join(app_dir, "cartridge-post-build-invoked"))


def test_build_app_name_set(tt_cmd, tmpdir_with_cfg):
    app_dir = shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/app1"),
                              os.path.join(tmpdir_with_cfg, "app1"))

    buid_cmd = [tt_cmd, "build", "app1"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))


def test_build_absolute_path(tt_cmd, tmpdir_with_cfg):
    app_dir = shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/app1"),
                              os.path.join(tmpdir_with_cfg, "app1"))

    with tempfile.TemporaryDirectory() as tmpWorkDir:
        buid_cmd = [tt_cmd, "--cfg",  os.path.join(tmpdir_with_cfg, config_name),
                    "build", app_dir]
        tt_process = subprocess.Popen(
            buid_cmd,
            cwd=tmpWorkDir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            stdin=subprocess.PIPE,
            text=True
        )
        tt_process.stdin.close()
        tt_process.wait()
        assert tt_process.returncode == 0

        assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
        assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))


def test_build_missing_rockspec(tt_cmd, tmpdir_with_cfg):
    buid_cmd = [tt_cmd, "build"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1

    tt_process.stdout.readline()  # Skip empty line.
    assert tt_process.stdout.readline().find(
        "please specify a rockspec to use on current directory") != -1


def test_build_missing_app_dir(tt_cmd, tmpdir_with_cfg):
    buid_cmd = [tt_cmd, "build", "app1"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1

    assert tt_process.stdout.readline().find("app1: no such file or directory") != -1


def test_build_multiple_paths(tt_cmd, tmpdir_with_cfg):
    buid_cmd = [tt_cmd, "build", "app1", "app2"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 1

    assert tt_process.stdout.readline().find("Error: accepts at most 1 arg(s), received 2") != -1


def test_build_spec_file_set(tt_cmd, tmpdir_with_cfg):
    app_dir = shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/app1"),
                              os.path.join(tmpdir_with_cfg, "app1"))

    buid_cmd = [tt_cmd, "build", "app1", "--spec", "app1-scm-1.rockspec"]
    tt_process = subprocess.Popen(
        buid_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    tt_process.stdin.close()
    tt_process.wait()
    assert tt_process.returncode == 0

    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "metrics"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "cartridge"))


def test_build_app_by_name(tt_cmd, tmpdir):
    init_cmd = [tt_cmd, "init"]
    tt_process = subprocess.Popen(
        init_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0

    os.mkdir(os.path.join(tmpdir, "appdir"))
    app_dir = shutil.copytree(os.path.join(os.path.dirname(__file__), "apps/app1"),
                              os.path.join(tmpdir, "appdir", "app1"))

    os.symlink("../appdir/app1", os.path.join(tmpdir, "instances.enabled", "app1"), True)
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
    assert os.path.exists(os.path.join(app_dir, ".rocks"))


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_build_app_local_tarantool(tt_cmd, tmpdir_with_tarantool):
    build_cmd = [tt_cmd, "create", "cartridge", "--name", "app1", "--non-interactive"]
    tt_process = subprocess.Popen(
        build_cmd,
        cwd=tmpdir_with_tarantool,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0

    app_dir = os.path.join(tmpdir_with_tarantool, "app1")

    assert os.path.exists(app_dir)

    build_cmd = [tt_cmd, "build", "app1"]
    tt_process = subprocess.Popen(
        build_cmd,
        cwd=tmpdir_with_tarantool,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0

    build_output = tt_process.stdout.readlines()
    assert "Application was successfully built" in build_output[len(build_output)-1]
    assert os.path.exists(os.path.join(app_dir, ".rocks"))
