import json
import os

import utils

# The watchdog of an instance runs what tt instance start resolved: a
# restart after a crash runs it again, whatever instances.yaml says by
# then; only an explicit tt instance restart takes the edits.


def write_description(app_path, description):
    """Write description as the instances.yaml of the default template of
    the project at app_path: external processes alone, which need no config."""
    template = app_path / ".templates" / "default"
    template.mkdir(parents=True, exist_ok=True)
    (template / "instances.yaml").write_text(description)


def crashing_job(mark, restart):
    """A job that writes a line with mark and with its argument into runs in
    its working directory, lives for a while and fails."""
    return (
        "job:\n"
        "  binary: sh\n"
        '  args: [-c, "echo \\"run $MARK $0\\" >> runs; sleep 3; exit 1", ' + mark + "]\n"
        "  env: {MARK: " + mark + "}\n"
        "  restart: " + restart + "\n"
    )


def read_runs(app_path):
    path = os.path.join(utils.inst_lib_dir(app_path, "app", "job"), "runs")
    if not os.path.exists(path):
        return []
    with open(path) as f:
        return f.read().splitlines()


def test_restart_after_crash_keeps_the_launch(tt_cmd, tmp_path):
    app_path = tmp_path / "app"
    write_description(app_path, crashing_job("one", "on-failure"))

    try:
        rc, out = utils.run_command_and_get_output(
            [tt_cmd, "instance", "start", "app:job"],
            cwd=app_path,
        )
        assert rc == 0, out
        assert utils.wait_event(10, lambda: read_runs(app_path) == ["run one one"])

        # The launch the watchdog runs is in the run directory, naming it.
        run_dir = utils.inst_run_dir(app_path, "app", "job")
        with open(os.path.join(run_dir, "launch.json")) as f:
            launch = json.load(f)
        with open(os.path.join(run_dir, utils.pid_file)) as f:
            assert launch["watchdog"] == int(f.read())
        assert launch["instance"]["Restart"] == "on-failure"
        assert "MARK=one" in launch["spec"]["env"]

        # Edited right after the start, the description does not reach the
        # watchdog: the crashed job is started again as it was, with its
        # environment, its arguments and its restart policy.
        write_description(app_path, crashing_job("two", "no"))

        assert utils.wait_event(
            20,
            lambda: len(read_runs(app_path)) >= 2,
        ), read_runs(app_path)
        assert read_runs(app_path)[:2] == ["run one one", "run one one"]

        # An explicit restart takes the edits.
        rc, out = utils.run_command_and_get_output(
            [tt_cmd, "instance", "restart", "-y", "app:job"],
            cwd=app_path,
        )
        assert rc == 0, out
        assert utils.wait_event(10, lambda: "run two two" in read_runs(app_path))
    finally:
        utils.run_command_and_get_output([tt_cmd, "instance", "stop", "-y"], cwd=app_path)

    assert not os.path.exists(
        os.path.join(utils.inst_run_dir(app_path, "app", "job"), "launch.json"),
    )
