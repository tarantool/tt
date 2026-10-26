import os
import re
import shutil

from utils import cluster_config_file, instances_file, run_command_and_get_output


def prepare_project(tmp_path, app_name, script_name=None):
    """Lay out a project with one instance, storage, whose config runs
    script_name, a file of test_app, as its app.file; or no app.file when
    script_name is None."""
    app_dir = tmp_path / app_name
    os.makedirs(app_dir / os.path.dirname(instances_file))

    with open(app_dir / instances_file, "w") as f:
        f.write("storage:\n")

    config = "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n          storage:\n"
    if script_name is None:
        config += "            {}\n"
    else:
        source = os.path.join(os.path.dirname(__file__), "test_app", script_name)
        shutil.copy(source, app_dir / script_name)
        config += f"            app:\n              file: {script_name}\n"

    with open(app_dir / cluster_config_file, "w") as f:
        f.write(config)

    return app_dir


def test_check_bare_name(tt_cmd, tmp_path):
    # A bare name is an application, not an instance target.
    cmd = [tt_cmd, "instance", "check", "file1"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert "use file1:<instance> or file1:*" in output


def test_check_unknown_app(tt_cmd, tmp_path):
    cmd = [tt_cmd, "instance", "check", "path-to-non-existent-app:*"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert "application not found" in output
    assert '"path-to-non-existent-app" is not installed' in output


def test_check_incorrect_syntax_file(tt_cmd, tmp_path):
    app_dir = prepare_project(tmp_path, "incorrect_syntax", "incorrect_syntax.lua")

    # Testing application file with incorrect syntax.
    cmd = [tt_cmd, "instance", "check", "incorrect_syntax:storage"]
    rc, output = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 1
    assert re.search(r"syntax errors detected:", output)


def test_check_correct_syntax_file(tt_cmd, tmp_path):
    app_dir = prepare_project(tmp_path, "correct_syntax", "correct_syntax.lua")

    # Testing application file with correct syntax, every instance of the
    # project the command runs in.
    cmd = [tt_cmd, "instance", "check"]
    rc, output = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 0
    assert re.search(r"is OK", output)
    assert os.path.join(str(app_dir), "correct_syntax.lua") in output


def test_check_no_app_file(tt_cmd, tmp_path):
    app_dir = prepare_project(tmp_path, "no_file")

    cmd = [tt_cmd, "instance", "check", "no_file:*"]
    rc, output = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 0
    assert "no_file:storage: the config names no application file (app.file)" in output
