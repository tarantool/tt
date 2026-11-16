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
    assert "use file1:<INSTANCE> or file1:*" in output


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


def test_check_validates_config(tt_cmd, tmp_path):
    # tt instance check validates the cluster config against the schema of
    # Tarantool, as tt instance start does: a typo fails both.
    app_dir = prepare_project(tmp_path, "typo", "correct_syntax.lua")
    config = app_dir / cluster_config_file

    rc, output = run_command_and_get_output([tt_cmd, "instance", "check"], cwd=app_dir)
    assert rc == 0, output
    assert f"Result of check: the cluster config {config} is valid" in output

    good = config.read_text()
    config.write_text(good + "memtx:\n  memroy: 100\n")

    rc, output = run_command_and_get_output([tt_cmd, "instance", "check"], cwd=app_dir)
    assert rc == 1, output
    assert f'application "typo": the cluster config {config} is invalid:' in output
    assert "unknown option memtx.memroy" in output
    assert "is OK" not in output

    # start refuses the config before it starts anything; --no-validate
    # starts the instance all the same, which Tarantool refuses then.
    rc, output = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_dir)
    assert rc == 1, output
    assert "unknown option memtx.memroy" in output
    assert "pass --no-validate to start all the same" in output
    assert "Starting an instance" not in output

    try:
        rc, output = run_command_and_get_output(
            [tt_cmd, "instance", "start", "--no-validate"],
            cwd=app_dir,
        )
        assert rc == 1, output
        assert "Starting an instance [typo:storage]..." in output
        assert 'Unexpected field "memroy"' in output
    finally:
        run_command_and_get_output([tt_cmd, "instance", "stop", "-y"], cwd=app_dir)


def test_check_built_in_templates(tt_cmd, tmp_path):
    # The config of every built-in template is valid.
    for template in ["single_instance", "cluster", "vshard_cluster", "config_storage"]:
        name = template.replace("_", "-")
        rc, output = run_command_and_get_output(
            [tt_cmd, "create", template, "--name", name, "-s"],
            cwd=tmp_path,
        )
        assert rc == 0, output

        rc, output = run_command_and_get_output([tt_cmd, "instance", "check"], cwd=tmp_path / name)
        assert rc == 0, output
        assert "the cluster config" in output and "is valid" in output
