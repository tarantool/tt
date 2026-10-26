import os
import shutil
import subprocess

import pytest

import utils

# The external_app project: master, a Tarantool instance, and worker, an
# external process.
EXTERNAL_APP = os.path.join(os.path.dirname(__file__), "..", "start", "external_app")


@pytest.mark.parametrize(
    "command",
    ["start", "stop", "restart", "kill", "status", "log", "logs", "logrotate", "check", "clean"],
)
def test_old_top_level_commands_are_gone(tt_cmd, tmp_path, command):
    # The instance commands live under tt instance only, without shims.
    rc, out = utils.run_command_and_get_output([tt_cmd, command], cwd=tmp_path)
    assert rc != 0
    assert f"unknown command {command}" in out


def test_partial_success_exit_code(tt_cmd, tmp_path):
    # A command over a list of targets goes on after a target it fails for,
    # and exits 3 when it failed for some of them only, 1 when for all.
    app_path = tmp_path / "app"
    shutil.copytree(EXTERNAL_APP, app_path)

    worker = "app:worker"
    try:
        rc, _ = utils.run_command_and_get_output(
            [tt_cmd, "instance", "start", worker],
            cwd=app_path,
        )
        assert rc == 0
        run_dir = utils.inst_run_dir(app_path, "app", "worker")
        assert utils.wait_files(5, [os.path.join(run_dir, utils.pid_file)])

        # master does not run: it has no log to reopen.
        cmd = [tt_cmd, "instance", "logrotate", "app:master,app:worker"]
        rc, out = utils.run_command_and_get_output(cmd, cwd=app_path)
        assert rc == 3, out
        assert "app:worker (PID = " in out
        assert "logs has been rotated" in out
        assert "app:master: the instance is not running, it must be started" in out
        assert "logrotate failed for 1 of 2 instances: app:master" in out

        cmd = [tt_cmd, "instance", "logrotate", "app:master"]
        rc, out = utils.run_command_and_get_output(cmd, cwd=app_path)
        assert rc == 1, out
        assert "failed for" not in out

        cmd = [tt_cmd, "instance", "logrotate", "app:worker"]
        rc, out = utils.run_command_and_get_output(cmd, cwd=app_path)
        assert rc == 0, out

        # The failure of master does not keep worker from being killed.
        cmd = [tt_cmd, "instance", "kill", "-y", "app:master", "app:worker"]
        rc, out = utils.run_command_and_get_output(cmd, cwd=app_path)
        assert rc == 3, out
        assert "The instance app:worker (PID = " in out
        assert "kill failed for 1 of 2 instances: app:master" in out

        rc, out = utils.run_command_and_get_output(
            [tt_cmd, "instance", "status", worker],
            cwd=app_path,
        )
        assert rc == 0
        assert utils.extract_status(out)[worker]["STATUS"] == "NOT RUNNING"

        cmd = [tt_cmd, "instance", "kill", "-y", "app:*"]
        rc, out = utils.run_command_and_get_output(cmd, cwd=app_path)
        assert rc == 1, out
        assert "kill failed for 2 of 2 instances" in out
    finally:
        utils.run_command_and_get_output([tt_cmd, "instance", "stop", "-y", worker], cwd=app_path)


@pytest.mark.parametrize("command", ["stop", "restart", "kill"])
def test_confirmation_needs_terminal(tt_cmd, tmp_path, command):
    # A command that asks for a confirmation asks a terminal only: without
    # one and without --yes it fails at once, pointing at --yes, reads no
    # answer from its standard input and acts on nothing.
    app_path = tmp_path / "app"
    shutil.copytree(EXTERNAL_APP, app_path)

    worker = "app:worker"
    try:
        rc, _ = utils.run_command_and_get_output(
            [tt_cmd, "instance", "start", worker],
            cwd=app_path,
        )
        assert rc == 0
        pid_file = os.path.join(utils.inst_run_dir(app_path, "app", "worker"), utils.pid_file)
        assert utils.wait_files(5, [pid_file])
        with open(pid_file) as f:
            pid = f.read()

        rc, out = utils.run_command_and_get_output(
            [tt_cmd, "instance", command, worker],
            cwd=app_path,
            input="y\n",
        )
        assert rc == 1, out
        assert "the standard input is not a terminal; pass --yes" in out
        assert "[y/n]" not in out
        with open(pid_file) as f:
            assert f.read() == pid

        # A terminal is asked, and its no is no failure.
        rc, out = utils.run_command_with_tty(
            [tt_cmd, "instance", command, worker],
            "n\n",
            cwd=app_path,
        )
        assert rc == 0, out
        assert "[y/n]" in out
        with open(pid_file) as f:
            assert f.read() == pid
    finally:
        utils.run_command_and_get_output([tt_cmd, "instance", "stop", "-y", worker], cwd=app_path)


def test_clean_confirmation_needs_terminal(tt_cmd, tmp_path):
    # clean asks only when it has something to remove, which a running
    # instance never has, and then it asks a terminal only, as the other
    # commands do: without one it fails at once and removes nothing.
    app_path = tmp_path / "app"
    shutil.copytree(EXTERNAL_APP, app_path)

    worker = "app:worker"
    log_file = os.path.join(utils.inst_log_dir(app_path, "app", "worker"), utils.log_file)
    rc, _ = utils.run_command_and_get_output([tt_cmd, "instance", "start", worker], cwd=app_path)
    assert rc == 0
    assert utils.wait_files(5, [log_file])
    rc, _ = utils.run_command_and_get_output(
        [tt_cmd, "instance", "stop", "-y", worker],
        cwd=app_path,
    )
    assert rc == 0

    rc, out = utils.run_command_and_get_output(
        [tt_cmd, "instance", "clean", worker],
        cwd=app_path,
        input="y\n",
    )
    assert rc == 1, out
    assert "the standard input is not a terminal; pass --yes" in out
    assert "[y/n]" not in out
    assert os.path.exists(log_file)

    # A terminal is asked, and its no is no failure.
    rc, out = utils.run_command_with_tty([tt_cmd, "instance", "clean", worker], "n\n", cwd=app_path)
    assert rc == 0, out
    assert "[y/n]" in out
    assert os.path.exists(log_file)


def flags_of(help_text):
    """Return the FLAGS section of the help text of a command."""
    return help_text[help_text.index("FLAGS") :]


@pytest.mark.skipif(not utils.is_tarantool_3(), reason="the project runs on Tarantool 3")
def test_connect_is_instance_connect(tt_cmd, tmp_path):
    # tt connect is a facade of tt instance connect: the same command with
    # the same flags.
    rc, facade_help = utils.run_command_and_get_output([tt_cmd, "connect", "--help"])
    assert rc == 0
    rc, instance_help = utils.run_command_and_get_output([tt_cmd, "instance", "connect", "--help"])
    assert rc == 0
    assert flags_of(facade_help) == flags_of(instance_help)
    assert "--scope" in flags_of(facade_help)
    assert "--template" in flags_of(facade_help)

    app_path = tmp_path / "app"
    shutil.copytree(EXTERNAL_APP, app_path)

    master = "app:master"
    try:
        rc, _ = utils.run_command_and_get_output(
            [tt_cmd, "instance", "start", master],
            cwd=app_path,
        )
        assert rc == 0
        run_dir = utils.inst_run_dir(app_path, "app", "master")
        assert utils.wait_files(5, [os.path.join(run_dir, utils.control_socket)])

        outputs = []
        for connect in (["connect"], ["instance", "connect"]):
            p = subprocess.run(
                [tt_cmd, *connect, master, "--scope", "project"],
                cwd=app_path,
                input="2+2",
                capture_output=True,
                text=True,
            )
            assert p.returncode == 0, p.stderr
            outputs.append(p.stdout)

        assert outputs[0] == outputs[1] == "---\n- 4\n...\n\n"
    finally:
        utils.run_command_and_get_output([tt_cmd, "instance", "stop", "-y", master], cwd=app_path)


def complete(tt_cmd, cwd, env, *words):
    """Return the completions tt offers for the last of words, and the
    completion directive."""
    p = subprocess.run(
        [tt_cmd, "__complete", *words],
        cwd=cwd,
        env=env,
        capture_output=True,
        text=True,
    )
    assert p.returncode == 0, p.stderr
    lines = p.stdout.splitlines()
    assert lines[-1].startswith(":")
    return lines[:-1], int(lines[-1][1:])


def test_instance_target_completion(tt_cmd, tmp_path):
    # The target of an instance command completes as <APP>:<INSTANCE>: the
    # current project and the applications of the system scope, or of the
    # scope --scope names, then the instances of the pair of the application
    # named that the command can act on.
    system = tmp_path / "system"
    billing = system / "versions" / "billing" / "billing_1.0.0"
    (billing / ".templates" / "default").mkdir(parents=True)
    (billing / ".templates" / "default" / "instances.yaml").write_text("invoices:\n  binary: sh\n")
    (system / "apps").mkdir()
    os.symlink(billing, system / "apps" / "billing")

    project = tmp_path / "fg"
    (project / ".templates" / "default").mkdir(parents=True)
    (project / ".templates" / "default" / "instances.yaml").write_text(
        "job:\n  binary: sh\nworker:\n  binary: sh\n",
    )

    env = dict(os.environ, TT_SYSTEM_ROOT=str(system), TT_USER_ROOT=str(tmp_path / "user"))

    no_space, no_file = 2, 4

    got, directive = complete(tt_cmd, project, env, "instance", "start", "")
    assert got == ["fg:", "billing:"]
    assert directive == no_space | no_file

    got, directive = complete(tt_cmd, project, env, "instance", "start", "fg:")
    assert got == ["fg:job", "fg:worker"]
    assert directive == no_file

    got, _ = complete(tt_cmd, project, env, "instance", "status", "billing:")
    assert got == ["billing:invoices"]

    got, _ = complete(tt_cmd, project, env, "instance", "start", "fg:job,fg:")
    assert got == ["fg:job,fg:job", "fg:job,fg:worker"]

    # Nothing runs: there is nothing to stop.
    got, _ = complete(tt_cmd, project, env, "instance", "stop", "fg:")
    assert got == []

    got, _ = complete(tt_cmd, project, env, "instance", "start", "--scope", "system", "")
    assert got == ["billing:"]

    got, _ = complete(tt_cmd, project, env, "instance", "start", "--scope", "project", "")
    assert got == ["fg:"]

    got, _ = complete(tt_cmd, tmp_path, env, "instance", "start", "")
    assert got == ["billing:"]

    got, _ = complete(tt_cmd, tmp_path, env, "connect", "")
    assert got == ["billing:"]

    got, _ = complete(tt_cmd, tmp_path, env, "instance", "start", "--scope", "user", "")
    assert got == []
