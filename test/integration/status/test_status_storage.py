import os
import socket
import subprocess

from utils import extract_status, run_command_and_get_output

# tt instance status reads what it can: with "*" and a config storage that
# is out of reach, it lists the instances known on this host and warns that
# the list may be incomplete, rather than failing.


def dead_endpoint():
    """Return the URL of a port nothing listens on: one just bound and
    closed again."""
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    return f"http://127.0.0.1:{port}"


def status_of(tt_cmd, app_path):
    """Run tt instance status: its rows, from the standard output, and its
    warnings, from the standard error."""
    p = subprocess.run(
        [tt_cmd, "instance", "status"],
        cwd=app_path,
        capture_output=True,
        text=True,
        timeout=60,
    )
    assert p.returncode == 0, p.stdout + p.stderr
    return extract_status(p.stdout), p.stderr


def write_app(app_path, instances, config):
    template = app_path / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text(instances)
    (template / "config.yaml").write_text(config)


def storage_config(endpoint):
    return (
        "config:\n"
        "  etcd:\n"
        f"    endpoints: [{endpoint}]\n"
        "    prefix: /app\n"
        "    http:\n"
        "      request:\n"
        "        timeout: 0.5\n"
    )


TOPOLOGY = """groups:
  g:
    replicasets:
      r:
        instances:
          storage-1: {}
          storage-2: {}
"""


def test_status_wildcard_storage_down(tt_cmd, tmp_path):
    endpoint = dead_endpoint()
    app_path = tmp_path / "app"
    write_app(app_path, '"*": {}\nlegacy:\n  script: init.lua\n', storage_config(endpoint))
    (app_path / "init.lua").write_text("box.cfg{}\n")

    # An instance that has run here, which only its data directory tells.
    os.makedirs(app_path / ".tarantool" / "var" / "lib" / "app" / "storage-3")

    status, warnings = status_of(tt_cmd, app_path)
    assert f'the config storage etcd {endpoint}, prefix "/app" cannot be read' in warnings
    assert "the list may be incomplete" in warnings
    assert set(status) == {"app:legacy", "app:storage-3"}
    for name in status:
        assert status[name]["STATUS"] == "NOT RUNNING"

    # The topology the config file declares by itself counts as well.
    (app_path / ".templates" / "default" / "config.yaml").write_text(
        storage_config(endpoint) + TOPOLOGY,
    )
    status, _ = status_of(tt_cmd, app_path)
    assert set(status) == {
        "app:legacy",
        "app:storage-1",
        "app:storage-2",
        "app:storage-3",
    }

    # stop reads what it can too; start needs the config, and says which
    # storage it cannot read.
    rc, out = run_command_and_get_output([tt_cmd, "instance", "stop", "-y"], cwd=app_path)
    assert rc == 0, out
    assert "the list may be incomplete" in out

    rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
    assert rc != 0
    assert f'the config storage etcd {endpoint}, prefix "/app" cannot be read' in out


def test_status_wildcard_storage_down_nothing_known(tt_cmd, tmp_path):
    # With nothing known on this host there are no rows to show: the error
    # names the storage and where tt looked.
    endpoint = dead_endpoint()
    app_path = tmp_path / "app"
    write_app(app_path, '"*": {}\n', storage_config(endpoint))

    rc, out = run_command_and_get_output([tt_cmd, "instance", "status"], cwd=app_path)
    assert rc != 0
    assert f'the config storage etcd {endpoint}, prefix "/app" cannot be read' in out
    assert "nor is any instance known on this host" in out
