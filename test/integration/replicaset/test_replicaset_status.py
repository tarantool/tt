import os
import re
import shutil
import tempfile
import time
from pathlib import Path

import pytest
from replicaset_helpers import copy_application, stop_application

from utils import get_tarantool_version, run_command_and_get_output, wait_file

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


@pytest.mark.parametrize(
    "case",
    [
        ["--config", "--custom"],
    ],
)
def test_status_orchestrators_force_mix(tt_cmd, tmpdir_with_cfg, case):
    status_cmd = [tt_cmd, "replicaset", "status", *case, "localhost:3013"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ only one type of orchestrator can be forced", out)


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize("flag", [None, "--config"])
def test_status_cconfig_uri(tt_cmd, tmpdir_with_cfg, flag):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = copy_application(tmpdir, app_name)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        file = wait_file(app_path, "ready-instance-001", [])
        assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd.extend(
            [
                "-u",
                "client",
                "-p",
                "secret",
                "./instance-001.iproto",
            ],
        )
        rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
        assert rc == 0
        assert (
            out
            == r"""Orchestrator:      centralized config
Replicasets state: bootstrapped

• replicaset-001
  Failover: off
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto unknown
    • instance-003 unix/:./instance-003.iproto unknown
"""
        )
    finally:
        stop_application(tt_cmd, app_name, app_path, [])


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
def test_status_cconfig_uri_force_custom(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = copy_application(tmpdir, app_name)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        file = wait_file(app_path, "ready-instance-001", [])
        assert file != ""

        status_cmd = [
            tt_cmd,
            "replicaset",
            "status",
            "--custom",
            "-u",
            "client",
            "-p",
            "secret",
            "./instance-001.iproto",
        ]
        rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
        assert rc == 0
        assert (
            out
            == r"""Orchestrator:      custom
Replicasets state: bootstrapped

• replicaset-001
  Failover: unknown
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto unknown
    • instance-003 unix/:./instance-003.iproto unknown
"""
        )
    finally:
        stop_application(tt_cmd, app_name, app_path, [])


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize("flag", [None, "--config"])
@pytest.mark.parametrize("target", ["test_ccluster_app", "test_ccluster_app:instance-001"])
def test_status_cconfig_app_and_instance(tt_cmd, tmpdir_with_cfg, flag, target):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = copy_application(tmpdir, app_name)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        for i in range(1, 6):
            file = wait_file(app_path, f"ready-instance-00{i}", [])
            assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd.append(target)

        rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
        assert rc == 0
        assert (
            out
            == r"""Orchestrator:      centralized config
Replicasets state: bootstrapped

• replicaset-001
  Failover: off
  Master:   single
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto read
    • instance-003 unix/:./instance-003.iproto read
• replicaset-002
  Failover: off
  Master:   multi
    • instance-004 unix/:./instance-004.iproto rw
    • instance-005 unix/:./instance-005.iproto rw
"""
        )
    finally:
        stop_application(tt_cmd, app_name, app_path, [])


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize("target", ["test_ccluster_app", "test_ccluster_app:instance-001"])
def test_status_cconfig_app_and_instance_force_custom(tt_cmd, tmpdir_with_cfg, target):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = copy_application(tmpdir, app_name)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        for i in range(1, 6):
            file = wait_file(app_path, f"ready-instance-00{i}", [])
            assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status", "--custom", target]

        rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
        assert rc == 0
        assert (
            out
            == r"""Orchestrator:      custom
Replicasets state: bootstrapped

• replicaset-001
  Failover: unknown
  Master:   single
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto read
    • instance-003 unix/:./instance-003.iproto read
• replicaset-002
  Failover: unknown
  Master:   multi
    • instance-004 unix/:./instance-004.iproto rw
    • instance-005 unix/:./instance-005.iproto rw
"""
        )
    finally:
        stop_application(tt_cmd, app_name, app_path, [])


USER_APP_MANIFEST = """manifest_version = "0.1"

[package]
name = "myapp"

[platform]
tarantool = ">=3.0.0"
"""

USER_APP_LOCK = """lock_version = "0.1"
manifest_version = "0.1"
generated_by = "tt 3.0.0"
manifest_hash = "sha256:0"
"""

USER_APP_CONFIG = """credentials:
  users:
    guest:
      roles: [super]

groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          storage-1: {}
"""


def install_user_app(root):
    """Install myapp of one Tarantool instance, storage-1, into the user root
    root the way tt package install lays an application out, with the
    tarantool of PATH as the tarantool of the scope."""
    version = root / "versions" / "myapp" / "myapp_1.0.0"
    pair = version / ".templates" / "default"
    pair.mkdir(parents=True)
    (version / "app.manifest.toml").write_text(USER_APP_MANIFEST)
    (version / "app.manifest.lock").write_text(USER_APP_LOCK)
    (pair / "instances.yaml").write_text("storage-1:\n")
    (pair / "config.yaml").write_text(USER_APP_CONFIG)
    (root / "apps").mkdir()
    os.symlink(os.path.join("..", "versions", "myapp", "myapp_1.0.0"), root / "apps" / "myapp")

    _, tarantool_version = run_command_and_get_output(["tarantool", "--version"])
    match = re.match(r"Tarantool\s+(Enterprise\s+)?(\d+\.\d+\.\d+)", tarantool_version)
    assert match is not None, tarantool_version
    version_id = match.group(2)
    installation = root / "versions" / "tarantool" / f"tarantool_{version_id}"
    installation.mkdir(parents=True)
    os.symlink(shutil.which("tarantool"), installation / "tarantool")


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
def test_status_user_scope(tt_cmd, tmp_path):
    # --scope user reaches an application installed in the user scope, as it
    # does for the instance commands that start it; without it the
    # application is looked for in the system scope.
    #
    # The scope root is under /tmp: the console socket of an installed
    # instance is bound by its full path, which must fit a unix socket address.
    with tempfile.TemporaryDirectory(dir="/tmp") as tmpdir:
        user = Path(tmpdir) / "user"
        install_user_app(user)
        env = dict(os.environ, TT_USER_ROOT=str(user), TT_SYSTEM_ROOT=str(Path(tmpdir) / "system"))

        try:
            rc, out = run_command_and_get_output(
                [tt_cmd, "instance", "start", "--scope", "user", "myapp:*"],
                cwd=tmp_path,
                env=env,
            )
            assert rc == 0, out
            run_dir = user / "var" / "run" / "myapp" / "storage-1"
            assert wait_file(run_dir, "tarantool.control", []) != ""

            status_cmd = [tt_cmd, "replicaset", "status", "--scope", "user", "myapp"]
            deadline = time.time() + 10
            while True:
                rc, out = run_command_and_get_output(status_cmd, cwd=tmp_path, env=env)
                if (rc == 0 and "bootstrapped" in out) or time.time() > deadline:
                    break
                time.sleep(0.2)

            assert rc == 0, out
            assert "Orchestrator:      centralized config" in out
            assert "• replicaset-001" in out
            assert "• storage-1 " in out

            rc, out = run_command_and_get_output(
                [tt_cmd, "replicaset", "status", "myapp"],
                cwd=tmp_path,
                env=env,
            )
            assert rc != 0
            assert '"myapp" is not installed in the system scope' in out
        finally:
            run_command_and_get_output(
                [tt_cmd, "instance", "stop", "--scope", "user", "--yes", "myapp:*"],
                cwd=tmp_path,
                env=env,
            )


@pytest.mark.parametrize("flag", [None, "--custom"])
def test_status_custom_app(tt_cmd, tmpdir_with_cfg, flag):
    # The application runs an instance that configures itself in its script,
    # and no instance of a cluster config: the custom orchestrator acts on it
    # without --custom, whatever the version of tarantool.
    tmpdir = tmpdir_with_cfg
    app_name = "test_custom_app"
    app_path = copy_application(tmpdir, app_name)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        # Check for start.
        file = wait_file(app_path, "ready", [])
        assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd.append("test_custom_app")

        rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
        assert rc == 0
        assert re.search(
            r"""Orchestrator:      custom
Replicasets state: bootstrapped

• .*
  Failover: unknown
  Master:   single
""",
            out,
        )
    finally:
        stop_application(tt_cmd, app_name, app_path, [])
