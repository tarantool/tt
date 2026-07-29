import json
import shutil
import time
from pathlib import Path

from backup_helpers import parse_yaml

from utils import create_tt_config, run_command_and_get_output

SHARDED_APP_NAME = "sharded_app"
SHARDED_APP_SRC = Path(__file__).parent / SHARDED_APP_NAME

# Ports must match sharded_app/config.yaml. vshard needs a real advertise URI,
# so the sharded app listens on TCP instead of tt's default unix socket; that
# also means the backup commands address the storages by <URI>, not by
# <APP:INSTANCE>.
STORAGE_URIS = {
    "storage-001-a": "client:secret@localhost:3341",
    "storage-002-a": "client:secret@localhost:3342",
}
ROUTER_INSTANCE = f"{SHARDED_APP_NAME}:router-001-a"

# Replicaset UUIDs pinned in sharded_app/config.yaml, keyed by storage leader.
STORAGE_REPLICASET_UUIDS = {
    "storage-001-a": "11111111-1111-1111-1111-111111111111",
    "storage-002-a": "22222222-2222-2222-2222-222222222222",
}


class ShardedApp:
    """A built, started and vshard-bootstrapped sharded_app environment."""

    def __init__(self, tt_cmd, env_dir: Path) -> None:
        self.tt_cmd = tt_cmd
        self.env_dir = env_dir
        self.app_name = SHARDED_APP_NAME

    def exec(self, *args, **kwargs):
        """Same contract as tt_helper.Tt.exec, so that the backup_helpers
        wrappers (start_backup, finalize_backup, ...) accept a ShardedApp."""
        return run_command_and_get_output([self.tt_cmd, *args], cwd=self.env_dir, **kwargs)

    def tt(self, *args, input=None, assert_rc=True):
        rc, out = self.exec(*args, input=input)
        if assert_rc:
            assert rc == 0, f"tt {' '.join(args)} failed:\n{out}"
        return rc, out

    def eval(self, target, lua):
        _, out = self.tt("connect", target, "-f-", input=lua)
        return parse_yaml(out)

    def eval_router(self, lua):
        return self.eval(ROUTER_INSTANCE, lua)

    def create_cluster_recovery_point(self, label):
        """Create a recovery point on every storage master, going through the
        vshard-router backend of the recovery point manager role."""
        return self.eval_router(
            "return box.backup.recovery_point.managers.default:create_point("
            f"{{label = '{label}'}})",
        )

    def recovery_point_manager_info(self):
        return self.eval_router("return box.backup.recovery_point.managers.default:info()")

    def recovery_point_labels(self, instance):
        """Labels stored in _recovery_point on a storage master, oldest first."""
        return self.eval(
            f"{self.app_name}:{instance}",
            "local labels = {} "
            "for _, t in box.space._recovery_point:pairs() do "
            "    table.insert(labels, t[4].label) "
            "end "
            "return labels",
        )


def _wait(timeout, predicate, message):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        ok, last = predicate()
        if ok:
            return last
        time.sleep(0.5)
    raise AssertionError(f"{message}; last seen:\n{last}")


def _wait_ready(app, timeout=60):
    """Wait until tt can talk to every instance, not merely until the processes
    exist. A RUNNING status only means the watchdog wrote a pid file; `box` is
    filled in only once tt has connected over var/run/<instance>/tarantool.control,
    and that socket is what `tt replicaset vshard bootstrap` needs."""
    instances = [f"{app.app_name}:{name}" for name in STORAGE_URIS] + [ROUTER_INSTANCE]

    def check():
        _, out = app.tt("status", app.app_name, "--format", "json", assert_rc=False)
        try:
            status = json.loads(out)
        except json.JSONDecodeError:
            return False, out
        ok = all(
            status.get(inst, {}).get("status") == "RUNNING"
            and status.get(inst, {}).get("box") == "running"
            for inst in instances
        )
        return ok, out

    _wait(timeout, check, "sharded_app instances did not become ready")


def _wait_buckets_discovered(app, timeout=60):
    def check():
        info = app.eval_router(
            "local i = require('vshard').router.info() "
            "return {unknown = i.bucket.unknown, available_rw = i.bucket.available_rw}",
        )
        if not isinstance(info, dict):
            return False, info
        return info.get("unknown") == 0 and info.get("available_rw", 0) > 0, info

    _wait(timeout, check, "vshard router did not discover all buckets")


def start_sharded_app(tt_cmd, env_dir: Path) -> ShardedApp:
    """Create a tt environment with sharded_app, build it (this installs the
    pinned vshard rock), start it and bootstrap vshard."""
    app = ShardedApp(tt_cmd, env_dir)
    # A minimal tt.yaml instead of `tt init`, and the app straight in the
    # environment root instead of under instances.enabled/, the way the other
    # backup tests lay it out. Those 18 characters matter: `tt replicaset`
    # reaches instances over var/run/<instance>/tarantool.control, and the full
    # path has to stay under the 108-byte sun_path limit once pytest's own
    # temporary directory is prepended.
    create_tt_config(env_dir, "")
    shutil.copytree(SHARDED_APP_SRC, env_dir / SHARDED_APP_NAME)

    app.tt("build", SHARDED_APP_NAME)
    app.tt("start", SHARDED_APP_NAME)
    _wait_ready(app)
    app.tt("replicaset", "vshard", "bootstrap", SHARDED_APP_NAME)
    _wait_buckets_discovered(app)
    return app


def fragment_of(archive_path):
    """Read the manifest fragment written next to an archive."""
    with open(archive_path.removesuffix(".tar.zst") + ".json", "r") as src:
        return json.load(src)
