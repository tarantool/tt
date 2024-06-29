import os

from utils import run_command_and_get_output


def test_bash_completion_loaded(tt_cmd, tmp_path):
    rc, output = run_command_and_get_output(
            [tt_cmd, "completion", "bash"],
            cwd=tmp_path, env=dict(os.environ, PWD=tmp_path.as_posix()))
    assert rc == 0
    fp = open(tmp_path / 'bash.completion', "a")
    fp.write(output)
    fp.close()

    rc, output = run_command_and_get_output(
            ["bash", "-c", f"source {tmp_path / 'bash.completion'}"],
            cwd=tmp_path, env=dict(os.environ, PWD=tmp_path.as_posix()))
    assert rc == 0
