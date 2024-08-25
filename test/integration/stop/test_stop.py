import os
import shutil

from utils import pid_file, run_command_and_get_output, run_path, wait_file


def test_stop(tt_cmd, tmpdir_with_cfg):
    shutil.copy(os.path.join(os.path.dirname(__file__), "test_app.lua"), tmpdir_with_cfg)
    app_name = "test_app"
    start_cmd = [tt_cmd, "start", app_name]
    rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir_with_cfg)
    assert "Starting an instance" in out
    assert wait_file(os.path.join(tmpdir_with_cfg, app_name, run_path, app_name),
                     pid_file, []) != ""

    try:
        # Test confirmed stop.
        stop_cmd = [tt_cmd, "stop", app_name]
        rc, out = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg, input="y\n")
        assert f"Confirm stop of '{app_name}' [y/n]" in out
        assert "has been terminated" in out
        app_path = os.path.join(tmpdir_with_cfg, app_name, run_path, app_name, pid_file)
        assert not os.path.exists(app_path)

        start_cmd = [tt_cmd, "start", app_name]
        rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir_with_cfg)
        assert rc == 0
        assert "Starting an instance" in out
        assert wait_file(os.path.join(tmpdir_with_cfg, app_name, run_path, app_name),
                         pid_file, []) != ""

        # Test cancelled stop.
        stop_cmd = [tt_cmd, "stop", app_name]
        rc, out = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg, input="n\n")
        assert rc == 0
        assert f"Confirm stop of '{app_name}' [y/n]" in out
        assert "Stop is cancelled" in out
        assert os.path.exists(app_path)

    finally:
        stop_cmd = [tt_cmd, "stop", "-y", app_name]
        run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)


def test_stop_with_auto_yes(tt_cmd, tmpdir_with_cfg):
    shutil.copy(os.path.join(os.path.dirname(__file__), "test_app.lua"), tmpdir_with_cfg)
    app_name = "test_app"
    start_cmd = [tt_cmd, "start", app_name]
    rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir_with_cfg)
    assert rc == 0
    assert "Starting an instance" in out
    assert wait_file(os.path.join(tmpdir_with_cfg, app_name, run_path, app_name),
                     pid_file, []) != ""

    try:
        # Test auto-stop with -y flag.
        stop_cmd = [tt_cmd, "stop", "-y", app_name]
        rc, out = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)
        assert rc == 0
        assert f"Confirm stop of '{app_name}' [y/n]" not in out
        assert "has been terminated" in out
        app_path = os.path.join(tmpdir_with_cfg, app_name, run_path, app_name, pid_file)
        assert not os.path.exists(app_path)

        start_cmd = [tt_cmd, "start", app_name]
        rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir_with_cfg)
        assert rc == 0
        assert "Starting an instance" in out
        assert wait_file(os.path.join(tmpdir_with_cfg, app_name, run_path, app_name),
                         pid_file, []) != ""

        # Test auto-stop with --yes flag.
        stop_cmd = [tt_cmd, "stop", "--yes", app_name]
        rc, out = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)
        assert rc == 0
        assert f"Confirm stop of '{app_name}' [y/n]" not in out
        assert "has been terminated" in out
        assert not os.path.exists(app_path)

    finally:
        stop_cmd = [tt_cmd, "stop", "-y", app_name]
        run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)


def test_stop_no_args(tt_cmd, tmp_path):
    app_name = "multi_app"
    test_app_path_src = os.path.join(os.path.dirname(__file__), app_name)
    test_app_path = os.path.join(tmp_path, app_name)
    shutil.copytree(test_app_path_src, test_app_path)

    start_cmd = [tt_cmd, "start"]
    rc, out = run_command_and_get_output(start_cmd, cwd=test_app_path)
    assert rc == 0
    assert "Starting an instance" in out

    try:
        # Test confirmed stop of all instances.
        stop_cmd = [tt_cmd, "stop"]
        rc, out = run_command_and_get_output(stop_cmd, cwd=test_app_path, input="y\n")
        assert "Confirm stop of all instances [y/n]" in out

    finally:
        stop_cmd = [tt_cmd, "stop", "-y"]
        run_command_and_get_output(stop_cmd, cwd=test_app_path)


def test_stop_no_prompt(tt_cmd, tmpdir_with_cfg):
    shutil.copy(os.path.join(os.path.dirname(__file__), "test_app.lua"), tmpdir_with_cfg)
    app_name = "test_app"
    start_cmd = [tt_cmd, "start", app_name]
    rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir_with_cfg)
    assert rc == 0
    assert "Starting an instance" in out
    assert wait_file(os.path.join(tmpdir_with_cfg, app_name, run_path, app_name),
                     pid_file, []) != ""

    try:
        # Test stop with tt --no-prompt flag.
        stop_cmd = [tt_cmd, "--no-prompt", "stop", app_name]
        rc, out = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)
        assert f"Confirm stop of '{app_name}' [y/n]" not in out
        assert "has been terminated" in out
        app_path = os.path.join(tmpdir_with_cfg, app_name, run_path, app_name, pid_file)
        assert not os.path.exists(app_path)

    finally:
        stop_cmd = [tt_cmd, "stop", "-y", app_name]
        run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)
