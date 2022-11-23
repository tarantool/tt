import os
import shutil
import subprocess

from utils import run_command_and_get_output, run_path, wait_file


def app_cmd(tt_cmd, tmpdir_with_cfg, cmd, input):
    start_cmd = [tt_cmd, *cmd]
    tt_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )

    tt_process.stdin.writelines(input)
    tt_process.stdin.close()
    rc = tt_process.wait()
    assert rc == 0
    return tt_process.stdout.readlines()


def test_restart(tt_cmd, tmpdir_with_cfg):
    shutil.copy(os.path.join(os.path.dirname(__file__), "test_app.lua"), tmpdir_with_cfg)
    app_name = "test_app"
    start_output = app_cmd(tt_cmd, tmpdir_with_cfg, ["start", app_name], [])
    assert "Starting an instance" in start_output[0]
    wait_file(os.path.join(tmpdir_with_cfg, run_path, app_name), 'test_app.pid', [])
    prev_pid = current_pid = 0

    try:
        with open(os.path.join(tmpdir_with_cfg, run_path, app_name, 'test_app.pid')) as pid_file:
            prev_pid = int(pid_file.readline())

        # Test confirmed restart.
        restart_output = app_cmd(tt_cmd, tmpdir_with_cfg, ["restart", app_name], ["y\n"])
        assert "Confirm restart of 'test_app' [y/n]" in restart_output[0]
        assert "has been terminated" in restart_output[0]
        assert "Starting an instance" in restart_output[1]
        wait_file(os.path.join(tmpdir_with_cfg, run_path, app_name), 'test_app.pid', [])

        with open(os.path.join(tmpdir_with_cfg, run_path, app_name, 'test_app.pid')) as pid_file:
            current_pid = int(pid_file.readline())
            assert current_pid != prev_pid  # New pid after restart.
            prev_pid = current_pid

        # Test cancelled restart.
        restart_output = app_cmd(tt_cmd, tmpdir_with_cfg, ["restart", app_name], ["n\n"])
        assert "Confirm restart of 'test_app' [y/n]" in restart_output[0]
        assert "Restart is cancelled" in restart_output[0]
        wait_file(os.path.join(tmpdir_with_cfg, run_path, app_name), 'test_app.pid', [])

        with open(os.path.join(tmpdir_with_cfg, run_path, app_name, 'test_app.pid')) as pid_file:
            assert int(pid_file.readline()) == prev_pid  # No restart, pid is the same.

    finally:
        stop_cmd = [tt_cmd, "stop", app_name]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)


def test_restart_with_auto_yes(tt_cmd, tmpdir_with_cfg):
    shutil.copy(os.path.join(os.path.dirname(__file__), "test_app.lua"), tmpdir_with_cfg)
    app_name = "test_app"
    start_output = app_cmd(tt_cmd, tmpdir_with_cfg, ["start", app_name], [])
    assert "Starting an instance" in start_output[0]
    wait_file(os.path.join(tmpdir_with_cfg, run_path, app_name), 'test_app.pid', [])
    prev_pid = current_pid = 0

    try:
        with open(os.path.join(tmpdir_with_cfg, run_path, app_name, 'test_app.pid')) as pid_file:
            prev_pid = int(pid_file.readline())

        restart_output = app_cmd(tt_cmd, tmpdir_with_cfg, ["restart", "-y", app_name], [])
        assert "Confirm restart of 'test_app' [y/n]" not in restart_output[0]
        assert "has been terminated" in restart_output[0]
        assert "Starting an instance" in restart_output[1]
        wait_file(os.path.join(tmpdir_with_cfg, run_path, app_name), 'test_app.pid', [])

        with open(os.path.join(tmpdir_with_cfg, run_path, app_name, 'test_app.pid')) as pid_file:
            current_pid = int(pid_file.readline())
            assert current_pid != prev_pid  # New pid after restart.
            prev_pid = current_pid

        restart_output = app_cmd(tt_cmd, tmpdir_with_cfg, ["restart", "--yes", app_name], [])
        assert "Confirm restart of 'test_app' [y/n]" not in restart_output[0]
        assert "has been terminated" in restart_output[0]
        assert "Starting an instance" in restart_output[1]
        wait_file(os.path.join(tmpdir_with_cfg, run_path, app_name), 'test_app.pid', [])

        with open(os.path.join(tmpdir_with_cfg, run_path, app_name, 'test_app.pid')) as pid_file:
            current_pid = int(pid_file.readline())
            assert current_pid != prev_pid  # New pid after restart.
            prev_pid = current_pid

    finally:
        stop_cmd = [tt_cmd, "stop", app_name]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)
