"""End-to-end coverage of tt package switch: an installed application of the
user scope is rolled back to an earlier installed version while its
instances run, and they move to it on tt instance restart.

myapp is packed without dependencies, so that it packs and installs offline,
and runs on a tarantool installed into the scope by hand (scopes_helpers).
"""

import json
import os

from integration.scopes.scopes_helpers import (
    install_tarantool,
    make_project,
    pack,
    running,
    tarantool_version,
    wait,
)


def install_two(tt_cmd, tmp_path, scope):
    """Install myapp 1.0.0, then 2.0.0 with --upgrade, as an upgrade leaves
    them: both in versions/, 2.0.0 active. Return the tarantool they run on."""
    first = pack(tt_cmd, make_project(tmp_path / "1.0.0", "1.0.0"))
    second = pack(tt_cmd, make_project(tmp_path / "2.0.0", "2.0.0"))
    tarantool = install_tarantool(scope.root, tarantool_version())

    scope.install(first)
    rc, _, err = scope.tt("package", "install", "--scope", "user", "--upgrade", str(second))
    assert rc == 0, err
    assert active(scope) == "myapp_2.0.0"
    return tarantool


def active(scope):
    """Return the version directory apps/myapp selects."""
    return os.path.basename(os.readlink(scope.root / "apps" / "myapp"))


def runs(scope, inst, version, count=1):
    """Return whether the instance inst of myapp has logged that it runs
    version of myapp from its version directory count times: the Tarantool
    instance logs it from app.lua, the external process from worker.sh."""
    version_dir = os.path.realpath(scope.version_dir(version))
    if inst == "storage":
        marker = f"myapp {version} runs in {version_dir}"
    else:
        marker = f"worker of myapp {version} runs"
    return scope.log(inst).count(marker) >= count


def test_switch_rolls_back_while_running(tt_cmd, tmp_path, user_scope):
    install_two(tt_cmd, tmp_path, user_scope)

    rc, _, err = user_scope.tt("instance", "start", "--scope", "user", "myapp:*")
    assert rc == 0, err
    assert wait(10, lambda: running(user_scope, "storage", "worker"))
    assert wait(10, lambda: runs(user_scope, "storage", "2.0.0"))
    assert wait(10, lambda: runs(user_scope, "worker", "2.0.0"))
    watchdogs = {inst: user_scope.pid(inst) for inst in ["storage", "worker"]}

    status = user_scope.status()
    assert status["myapp:storage"]["version"] == "2.0.0"
    assert "restart_pending" not in status["myapp:storage"]

    # The rollback while the instances run: the link moves, the instances
    # stay up on the version they were started from, and switch says so.
    rc, out, err = user_scope.tt("package", "switch", "myapp", "1.0.0", "--scope", "user")
    assert rc == 0, err
    assert "switched myapp from 2.0.0 to 1.0.0 in user scope" in out
    assert "2 instances of myapp run 2.0.0: storage, worker" in err
    assert "to move them to 1.0.0: tt instance restart myapp:storage,myapp:worker" in err
    assert active(user_scope) == "myapp_1.0.0"
    assert (user_scope.version_dir("2.0.0") / "app.manifest.toml").is_file()

    assert running(user_scope, "storage", "worker")
    assert {inst: user_scope.pid(inst) for inst in watchdogs} == watchdogs
    assert not runs(user_scope, "storage", "1.0.0")

    status = user_scope.status()
    for inst in ["storage", "worker"]:
        assert status[f"myapp:{inst}"]["version"] == "2.0.0"
        assert status[f"myapp:{inst}"]["restart_pending"] is True
    rc, out, err = user_scope.tt("instance", "status", "--scope", "user", "myapp:*", "--details")
    assert rc == 0, err
    assert "Version of myapp:storage: 2.0.0, restart pending: 1.0.0 is active" in out

    rc, out, err = user_scope.tt("package", "list", "--scope", "user", "-o", "json")
    assert rc == 0, err
    [app] = json.loads(out)["packages"]
    assert app["version"] == "1.0.0"
    assert app["versions"] == ["1.0.0", "2.0.0"]

    # Switching to the active version does nothing, and still tells which
    # instances do not run it yet.
    rc, out, err = user_scope.tt("package", "switch", "myapp", "1.0.0", "--scope", "user")
    assert rc == 0, err
    assert "myapp 1.0.0 is already active in user scope" in out
    assert "2 instances of myapp run 2.0.0" in err

    rc, _, err = user_scope.tt("instance", "restart", "--scope", "user", "myapp:*", "--yes")
    assert rc == 0, err
    assert wait(10, lambda: running(user_scope, "storage", "worker"))
    assert wait(10, lambda: runs(user_scope, "storage", "1.0.0"))
    assert wait(10, lambda: runs(user_scope, "worker", "1.0.0"))
    status = user_scope.status()
    for inst in ["storage", "worker"]:
        assert status[f"myapp:{inst}"]["version"] == "1.0.0"
        assert "restart_pending" not in status[f"myapp:{inst}"]

    # Forward again with a plain install of a newer version: install
    # activates it, and tells that the instances still run 1.0.0.
    third = pack(tt_cmd, make_project(tmp_path / "3.0.0", "3.0.0"))
    rc, out, err = user_scope.tt("package", "install", "--scope", "user", str(third))
    assert rc == 0, err
    assert "installed myapp 3.0.0 into user scope" in out
    assert "2 instances of myapp run 1.0.0: storage, worker" in err
    assert active(user_scope) == "myapp_3.0.0"

    rc, _, err = user_scope.tt("instance", "stop", "--scope", "user", "myapp:*", "--yes")
    assert rc == 0, err
    assert not list(user_scope.root.glob("var/run/myapp/*/app.dir")), "the watchdogs clean up"

    # With nothing running there is nothing to tell.
    rc, out, err = user_scope.tt("package", "switch", "myapp", "2.0.0", "--scope", "user")
    assert rc == 0, err
    assert "switched myapp from 3.0.0 to 2.0.0 in user scope" in out
    assert "instance" not in err


def test_switch_refusals(tt_cmd, tmp_path, user_scope):
    install_two(tt_cmd, tmp_path, user_scope)

    # A version that is not installed: the error lists those that are.
    rc, _, err = user_scope.tt("package", "switch", "myapp", "9.9.9", "--scope", "user")
    assert rc == 1
    assert "version is not installed: myapp 9.9.9 in user scope" in err
    assert "installed versions: 1.0.0, 2.0.0 (active)" in err

    # An application that is not installed.
    rc, _, err = user_scope.tt("package", "switch", "other", "1.0.0", "--scope", "user")
    assert rc == 1
    assert "package is not installed" in err

    # No version, and no terminal to choose one on: the installed versions
    # are listed, the active one marked, and nothing changes.
    rc, out, err = user_scope.tt("package", "switch", "myapp", "--scope", "user")
    assert rc == 1
    assert out.splitlines() == ["installed versions of myapp in user scope:", "  1.0.0", "* 2.0.0"]
    assert "no version to switch to" in err
    assert "tt package switch myapp VERSION --scope user" in err

    # The project scope has no versions to switch between; it is the default.
    for scope_args in [[], ["--scope", "project"]]:
        rc, _, err = user_scope.tt("package", "switch", "myapp", "1.0.0", *scope_args)
        assert rc == 1
        assert "tt package switch applies to the user and system scopes only" in err
        assert "tt package install --force" in err

    # A version whose bundled tarantool is gone could not start: it is not
    # made active, and the error tells what to install.
    lock = user_scope.version_dir("1.0.0") / "app.manifest.lock"
    lock.write_text("bundled_tarantool_version = '9.9.9'\n" + lock.read_text())
    rc, _, err = user_scope.tt("package", "switch", "myapp", "1.0.0", "--scope", "user")
    assert rc == 1
    assert "cannot switch myapp to 1.0.0: the version could not start" in err
    assert "tt binary install tarantool 9.9.9 --scope user" in err

    assert active(user_scope) == "myapp_2.0.0"
