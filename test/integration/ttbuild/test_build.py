import os
import shutil
import subprocess
import tempfile

import pytest

from utils import config_name


def test_build_no_options(tt_cmd, tmpdir_with_cfg):
    app_name = "app1"
    app_dir = shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps", app_name),
        os.path.join(tmpdir_with_cfg, app_name),
    )

    cmd = [tt_cmd, "build"]
    p = subprocess.run(
        cmd,
        cwd=app_dir,
    )
    assert p.returncode == 0
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))


def test_build_with_tt_hooks(tt_cmd, tmpdir_with_cfg):
    app_name = "app1"
    app_dir = shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps", app_name),
        os.path.join(tmpdir_with_cfg, app_name),
    )
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps/tt_hooks"),
        os.path.join(tmpdir_with_cfg, app_name),
        dirs_exist_ok=True,
    )

    cmd = [tt_cmd, "build"]
    p = subprocess.run(
        cmd,
        cwd=app_dir,
    )
    assert p.returncode == 0
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))
    assert os.path.exists(os.path.join(app_dir, "tt-pre-build-invoked"))
    assert os.path.exists(os.path.join(app_dir, "tt-post-build-invoked"))


def test_build_with_cartridge_hooks(tt_cmd, tmpdir_with_cfg):
    app_name = "app1"
    app_dir = shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps", app_name),
        os.path.join(tmpdir_with_cfg, app_name),
    )
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps/cartridge_hooks"),
        os.path.join(tmpdir_with_cfg, app_name),
        dirs_exist_ok=True,
    )

    cmd = [tt_cmd, "build"]
    p = subprocess.run(
        cmd,
        cwd=app_dir,
    )
    assert p.returncode == 0
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))
    assert os.path.exists(os.path.join(app_dir, "cartridge-pre-build-invoked"))
    assert os.path.exists(os.path.join(app_dir, "cartridge-post-build-invoked"))


def test_build_app_name_set(tt_cmd, tmpdir_with_cfg):
    app_name = "app1"
    app_dir = shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps", app_name),
        os.path.join(tmpdir_with_cfg, app_name),
    )

    cmd = [tt_cmd, "build", app_name]
    p = subprocess.run(
        cmd,
        cwd=tmpdir_with_cfg,
    )
    assert p.returncode == 0
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))


def test_build_absolute_path(tt_cmd, tmpdir_with_cfg):
    app_name = "app1"
    app_dir = shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps", app_name),
        os.path.join(tmpdir_with_cfg, app_name),
    )

    with tempfile.TemporaryDirectory() as tmpWorkDir:
        cmd = [tt_cmd, "--cfg", os.path.join(tmpdir_with_cfg, config_name), "build", app_dir]
        p = subprocess.run(
            cmd,
            cwd=tmpWorkDir,
        )
        assert p.returncode == 0
        assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
        assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))


def test_build_error_omit_stdout(tt_cmd, tmpdir_with_cfg):
    cmd = [tt_cmd, "build"]
    p = subprocess.run(
        cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.PIPE,
        stdout=subprocess.DEVNULL,
        text=True,
    )
    print(p.stderr)
    assert p.returncode != 0
    assert "please specify a rockspec to use on current directory" in p.stderr


def test_build_missing_rockspec(tt_cmd, tmpdir_with_cfg):
    cmd = [tt_cmd, "build"]
    p = subprocess.run(
        cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    print(p.stdout)
    assert p.returncode != 0
    assert "please specify a rockspec to use on current directory" in p.stdout


def test_build_missing_app_dir(tt_cmd, tmpdir_with_cfg):
    app_name = "app1"
    cmd = [tt_cmd, "build", app_name]
    p = subprocess.run(
        cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    print(p.stdout)
    assert p.returncode != 0
    assert f"{app_name}: no such file or directory" in p.stdout


def test_build_multiple_paths(tt_cmd, tmpdir_with_cfg):
    cmd = [tt_cmd, "build", "app1", "app2"]
    p = subprocess.run(
        cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    print(p.stdout)
    assert p.returncode != 0
    assert "Error: accepts at most 1 arg(s), received 2" in p.stdout


def test_build_spec_file_set(tt_cmd, tmpdir_with_cfg):
    app_name = "app1"
    app_dir = shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps", app_name),
        os.path.join(tmpdir_with_cfg, app_name),
    )

    cmd = [tt_cmd, "build", app_name, "--spec", "app1-scm-1.rockspec"]
    p = subprocess.run(
        cmd,
        cwd=tmpdir_with_cfg,
    )
    assert p.returncode == 0
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "checks.lua"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "rocks"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "metrics"))
    assert os.path.exists(os.path.join(app_dir, ".rocks", "share", "tarantool", "cartridge"))


@pytest.mark.parametrize("flag", [None, "-V"])
def test_build_app_by_name(tt_cmd, tmp_path, flag):
    cmd = [tt_cmd, "init"]
    p = subprocess.run(
        cmd,
        cwd=tmp_path,
    )
    assert p.returncode == 0

    app_name = "app1"
    os.mkdir(os.path.join(tmp_path, "appdir"))
    app_dir = shutil.copytree(
        os.path.join(os.path.dirname(__file__), "apps", app_name),
        os.path.join(tmp_path, "appdir", app_name),
    )
    os.symlink(
        os.path.join("../appdir", app_name),
        os.path.join(tmp_path, "instances.enabled", app_name),
        True,
    )

    cmd = [tt_cmd]
    if flag:
        cmd.append(flag)
    cmd.extend(["build", app_name])
    p = subprocess.run(
        cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    print(p.stdout)
    assert p.returncode == 0
    assert "Application was successfully built" in p.stdout
    assert os.path.exists(os.path.join(app_dir, ".rocks"))


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_build_app_local_tarantool(tt_cmd, tmpdir_with_tarantool):
    app_name = "app1"
    app_dir = os.path.join(tmpdir_with_tarantool, app_name)

    cmd = [tt_cmd, "create", "cartridge", "--name", app_name, "--non-interactive"]
    p = subprocess.run(
        cmd,
        cwd=tmpdir_with_tarantool,
    )
    assert p.returncode == 0
    assert os.path.exists(app_dir)

    cmd = [tt_cmd, "build", app_name]
    p = subprocess.run(
        cmd,
        cwd=tmpdir_with_tarantool,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    print(p.stdout)
    assert p.returncode == 0
    assert "Application was successfully built" in p.stdout
    assert os.path.exists(os.path.join(app_dir, ".rocks"))
