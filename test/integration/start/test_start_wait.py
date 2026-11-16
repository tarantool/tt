import os
import time

import pytest

import utils

# tt instance start and restart wait until the instances they start in the
# background are up, or have failed, and say why.

CONFIG = """app:
  file: init.lua
groups:
  g:
    replicasets:
      r1:
        instances:
          i1: {}
      r2:
        instances:
          i2: {}
"""


def write_app(app_path, instances, init):
    """Lay out at path the project app: a cluster config of two instances,
    each a replicaset of its own that listens on the unix socket tt gives it,
    so the tests choose no ports, running init.lua."""
    template = app_path / ".templates" / "default"
    template.mkdir(parents=True, exist_ok=True)
    (template / "config.yaml").write_text(CONFIG)
    (template / "instances.yaml").write_text(instances)
    (app_path / "init.lua").write_text(init)


def run_tt(tt_cmd, app_path, *args):
    return utils.run_command_and_get_output([tt_cmd, *args], cwd=app_path)


def stop_all(tt_cmd, app_path):
    run_tt(tt_cmd, app_path, "instance", "stop", "-y")


def status_of(tt_cmd, app_path):
    rc, out = run_tt(tt_cmd, app_path, "instance", "status")
    assert rc == 0, out
    return utils.extract_status(out)


def test_start_crashing_instance_fails(tt_cmd, tmp_path):
    # An instance that fails as it starts fails tt instance start, which
    # says how it ended and quotes the end of its tt.log.
    app_path = tmp_path / "app"
    write_app(app_path, "i1:\n", 'error("boom on start")\n')

    try:
        rc, out = run_tt(tt_cmd, app_path, "instance", "start", "app:i1")
        assert rc != 0, out
        assert "Starting an instance [app:i1]..." in out
        assert "instance app:i1 did not start: it exited with code 1" in out
        log = os.path.join(utils.inst_log_dir(app_path, "app", "i1"), utils.log_file)
        assert f"The last lines of {log}:" in out
        assert "boom on start" in out
        assert status_of(tt_cmd, app_path)["app:i1"]["STATUS"] == "NOT RUNNING"
    finally:
        stop_all(tt_cmd, app_path)


@pytest.mark.skipif(os.geteuid() == 0, reason="root writes into a read-only directory anyway")
@pytest.mark.parametrize("wait", [[], ["--no-wait"], ["--foreground"]])
def test_start_unwritable_layout_fails(tt_cmd, tmp_path, wait):
    # What the watchdog needs before it has a log, the directories of the
    # instance and its tt.log, is made and checked by tt instance start,
    # which fails with code 2 and says why, also when it does not wait: a
    # watchdog in the background would have nowhere to say so.
    app_path = tmp_path / "app"
    write_app(app_path, "i1:\n", "")
    log_dir = utils.inst_log_dir(app_path, "app", "i1")
    log = os.path.join(log_dir, utils.log_file)

    try:
        # A project that cannot be written into.
        os.chmod(app_path, 0o555)
        rc, out = run_tt(tt_cmd, app_path, "instance", "start", *wait)
        assert rc == 2, out
        assert "instance app:i1: failed to create a directory" in out
        assert "permission denied" in out
        os.chmod(app_path, 0o755)

        # A log that cannot be written.
        os.makedirs(log_dir)
        with open(log, "w"):
            pass
        os.chmod(log, 0o444)
        rc, out = run_tt(tt_cmd, app_path, "instance", "start", *wait)
        assert rc == 2, out
        assert f'instance app:i1: cannot open the log file "{log}"' in out
        assert "permission denied" in out
        assert status_of(tt_cmd, app_path)["app:i1"]["STATUS"] == "NOT RUNNING"
    finally:
        os.chmod(app_path, 0o755)
        if os.path.exists(log):
            os.chmod(log, 0o644)
        stop_all(tt_cmd, app_path)


def test_start_crashing_instance_restarted_fails(tt_cmd, tmp_path):
    # A restart policy does not hide the failure: the watchdog starts the
    # instance again, and the start still fails, saying so.
    app_path = tmp_path / "app"
    write_app(app_path, "i1: {restart: on-failure}\n", 'error("boom on start")\n')

    try:
        rc, out = run_tt(tt_cmd, app_path, "instance", "start", "app:i1")
        assert rc != 0, out
        assert "instance app:i1 did not start: it exited with code 1" in out
        assert "its watchdog starts it again, as its restart policy says" in out
        assert "boom on start" in out
    finally:
        stop_all(tt_cmd, app_path)


def test_start_no_wait(tt_cmd, tmp_path):
    # --no-wait starts the watchdog and returns at once, as tt always did.
    app_path = tmp_path / "app"
    write_app(app_path, "i1:\n", 'error("boom on start")\n')

    try:
        rc, out = run_tt(tt_cmd, app_path, "instance", "start", "--no-wait", "app:i1")
        assert rc == 0, out
        assert "Starting an instance [app:i1]..." in out
        assert "is up" not in out
        assert "did not start" not in out
    finally:
        stop_all(tt_cmd, app_path)


SLOW_INIT = 'require("fiber").sleep(tonumber(os.getenv("SLOW_START") or "0"))\n'


def test_start_slow_instances_within_the_timeout(tt_cmd, tmp_path):
    # Instances that take a while to come up are waited for, together: two
    # that take 2 seconds each start in about 2 seconds, and are up once
    # tt returns.
    app_path = tmp_path / "app"
    write_app(
        app_path,
        "i1: {env: {SLOW_START: '2'}}\ni2: {env: {SLOW_START: '2'}}\n",
        SLOW_INIT,
    )

    try:
        started = time.monotonic()
        rc, out = run_tt(tt_cmd, app_path, "instance", "start", "--timeout", "30s")
        took = time.monotonic() - started
        assert rc == 0, out
        assert "The instance app:i1 is up." in out
        assert "The instance app:i2 is up." in out
        assert took >= 2, out
        assert took < 3.9, "the instances were waited for in turn"

        status = status_of(tt_cmd, app_path)
        for name in ("app:i1", "app:i2"):
            assert status[name]["STATUS"] == "RUNNING"
            assert status[name]["CONFIG"] == "ready"
            assert status[name]["BOX"] == "running"
    finally:
        stop_all(tt_cmd, app_path)


def test_start_slow_instance_times_out(tt_cmd, tmp_path):
    # An instance not up in time fails the start, which says how far it got,
    # and is left to its watchdog; the other one is up, and the start exits
    # with the code of a partial failure.
    app_path = tmp_path / "app"
    write_app(app_path, "i1: {env: {SLOW_START: '30'}}\ni2:\n", SLOW_INIT)

    try:
        rc, out = run_tt(tt_cmd, app_path, "instance", "start", "--timeout", "2s")
        assert rc == 3, out
        assert "The instance app:i2 is up." in out
        assert "instance app:i1 is not up in time: it runs" in out
        assert "after 2s; it is left to its watchdog" in out
        assert status_of(tt_cmd, app_path)["app:i1"]["STATUS"] == "RUNNING"
    finally:
        stop_all(tt_cmd, app_path)


def test_start_timeout_must_be_positive(tt_cmd, tmp_path):
    app_path = tmp_path / "app"
    write_app(app_path, "i1:\n", "")

    rc, out = run_tt(tt_cmd, app_path, "instance", "start", "--timeout", "0s")
    assert rc != 0
    assert "--timeout must be positive" in out


def test_start_external_process(tt_cmd, tmp_path):
    # An external process is up once it has run for a second; one that exits
    # before that has failed.
    app_path = tmp_path / "app"
    template = app_path / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text(
        "lasting:\n"
        "  binary: sh\n"
        '  args: [-c, "exec sleep 1000"]\n'
        "failing:\n"
        "  binary: sh\n"
        '  args: [-c, "echo failing says bye; exit 4"]\n',
    )

    try:
        rc, out = run_tt(tt_cmd, app_path, "instance", "start")
        assert rc == 3, out
        assert "The instance app:lasting is up." in out
        assert "instance app:failing did not start: it exited with code 4" in out
        assert "failing says bye" in out
    finally:
        stop_all(tt_cmd, app_path)


def test_restart_waits(tt_cmd, tmp_path):
    # tt instance restart waits as tt instance start does.
    app_path = tmp_path / "app"
    write_app(app_path, "i1:\n", "")

    try:
        rc, out = run_tt(tt_cmd, app_path, "instance", "start", "app:i1")
        assert rc == 0, out
        assert "The instance app:i1 is up." in out

        (app_path / "init.lua").write_text('error("boom on restart")\n')

        rc, out = run_tt(tt_cmd, app_path, "instance", "restart", "-y", "app:i1")
        assert rc != 0, out
        assert "instance app:i1 did not start: it exited with code 1" in out
        assert "boom on restart" in out

        (app_path / "init.lua").write_text("")

        rc, out = run_tt(tt_cmd, app_path, "instance", "restart", "-y", "app:i1")
        assert rc == 0, out
        assert "The instance app:i1 is up." in out
    finally:
        stop_all(tt_cmd, app_path)
