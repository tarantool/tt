"""End-to-end lifecycle of the backup stack on one instance.

Every command here has tests of its own in this directory, but always on
inputs the test built by hand. This file wires the real pipeline end to end,
each stage consuming exactly what the previous stage produced:

    plan full -> start -> upload -> finalize -> verify -> last
    -> plan incremental -> start --from-vclock -> upload -> finalize
    -> verify -> gc -> download from storage -> restore apply -> boot

and the restored instance has to come up on the recovery point with exactly
the rows written before it. What this pins is the seams: the plan output is
fed to upload as a file, the uploaded manifest is what the incremental plan
reads, and the archives handed to restore are the storage's bytes -- not the
local copies upload started from, which it deletes.
"""

import json
import os
import subprocess

import pytest
import tt_helper
from backup_helpers import (
    app_instance,
    archive_path_from_output,
    backup_dir,
    backup_gc,
    backup_last,
    backup_plan,
    backup_upload,
    backup_verify,
    eval_yaml_app,
    finalize_backup,
    start_backup,
)
from storage_helpers import FileStorage

from utils import get_tarantool_version, is_tarantool_less

STORAGE_1_A = "storage-001-a"

# UUIDs pinned in restore_app/config.yaml.
APP_INSTANCE_UUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
REPLICASET_UUID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

FULL_ID = "lifecycle-0001-full"
INC_ID = "lifecycle-0002-inc"

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"the backup stack requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


def post_start_lifecycle_app(tt_app):
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(30, tt_app, tt_app.running_instances, ["running"])


# The memtx-only restore app: this test boots what it restores, and a
# restored instance with vinyl data cannot start (see test_restore_apply).
TT_LIFECYCLE_APP = dict(
    app_path="restore_app",
    app_name="app",
    instances=[STORAGE_1_A],
    running_targets=["app"],
    post_start=post_start_lifecycle_app,
)

INSPECT_LUA = """
local dir = os.getenv('TT_TEST_WORK_DIR')
box.cfg{work_dir = dir, memtx_dir = dir, wal_dir = dir, vinyl_dir = dir}
print('TT_TEST_RESULT ' .. require('json').encode({
    uuid = box.info.uuid,
    lsn = box.info.vclock[1],
    rows = box.space.restore_test:len(),
    max_id = box.space.restore_test.index.pk:max()[1],
}))
os.exit(0)
"""


def boot_restored(work_dir, tmp_path):
    """Start Tarantool on a restored work directory and report its state."""
    script = tmp_path / "inspect.lua"
    script.write_text(INSPECT_LUA, encoding="utf-8")

    proc = subprocess.run(
        ["tarantool", str(script)],
        capture_output=True,
        text=True,
        timeout=60,
        env=dict(os.environ, TT_TEST_WORK_DIR=str(work_dir)),
    )

    for line in proc.stdout.splitlines():
        if line.startswith("TT_TEST_RESULT "):
            return json.loads(line.removeprefix("TT_TEST_RESULT "))

    raise AssertionError(
        f"Tarantool did not start from {work_dir}:\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}",
    )


def insert_rows(tt, target, begin, end):
    eval_yaml_app(
        tt,
        target,
        f"for i = {begin}, {end} do box.space.restore_test:replace{{i, 'row-' .. i}} end "
        "return true",
    )


def read_fragment(archive_path):
    with open(archive_path.removesuffix(".tar.zst") + ".json", "r") as src:
        return json.load(src)


def plan_json(tt, target, storage_uri, config_path, tmp_path, name):
    """Run tt backup plan and keep its raw output as the file upload reads."""
    rc, out = backup_plan(tt, target, storage_uri, config=config_path, fmt="json")
    assert rc == 0, f"tt backup plan --target={target} failed:\n{out}"

    path = tmp_path / name
    path.write_text(out.strip(), encoding="utf-8")
    return json.loads(out.strip()), str(path)


def verify_healthy(tt, storage_uri, manifests):
    rc, out = backup_verify(tt, storage_uri, fmt="json")
    assert rc == 0, f"tt backup verify failed:\n{out}"

    report = json.loads(out)
    assert report["issues"] == []
    assert report["manifests_checked"] == manifests
    assert report["archives_checked"] == manifests
    return report


def backup_and_upload(tt, target, storage, backup_id, plan_path, from_vclock=None):
    """start -> upload -> finalize, the way the orchestrator drives a node."""
    rc, out = start_backup(tt, target, backup_id, from_vclock=from_vclock)
    assert rc == 0, f"tt backup start failed:\n{out}"

    archive = archive_path_from_output(out)
    fragment_path = archive.removesuffix(".tar.zst") + ".json"
    fragment = read_fragment(archive)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=backup_id,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"
    assert not os.path.exists(archive), "upload moves the archive off the node"

    rc, out = finalize_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup finalize failed:\n{out}"
    assert not os.path.exists(backup_dir(backup_id)), "finalize sweeps what upload left"

    return fragment


def download_chain(storage, last_id, dest_dir):
    """Resolve the chain by previous_backup_id and pull it out of storage."""
    manifests = []
    backup_id = last_id
    while backup_id:
        manifest = json.loads(storage.read(f"manifests/{backup_id}.json"))
        manifests.append(manifest)
        backup_id = manifest.get("previous_backup_id")
    manifests.reverse()

    os.makedirs(dest_dir, exist_ok=True)
    archives, checksums = [], []
    for manifest in manifests:
        artifact = manifest["shards"][REPLICASET_UUID]["instance"]["artifact"]
        local = os.path.join(dest_dir, os.path.basename(artifact["path"]))
        with open(local, "wb") as dst:
            dst.write(storage.read(artifact["path"]))
        archives.append(local)
        checksums.append(artifact["checksum_sha256"])

    return archives, checksums


# Deliberately terse name: it prefixes the app's unix socket paths through
# pytest's tmp_path, and the whole thing must fit into sun_path on macOS.
@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_LIFECYCLE_APP)
def test_lifecycle_pitr(tt, tt_app, tmp_path):
    target = app_instance(tt_app, STORAGE_1_A)
    config_path = tt_app.path("config.yaml")

    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    # -- Plan the full backup off the live topology.
    plan_full, plan_full_path = plan_json(
        tt,
        "full",
        storage.uri,
        config_path,
        tmp_path,
        "plan-full.json",
    )
    assert plan_full["mode"] == "full"
    replicaset = plan_full["replicasets"][REPLICASET_UUID]
    assert replicaset["master_instance_uuid"] == APP_INSTANCE_UUID
    assert "from_vclock" not in replicaset

    # -- Rows 11..30 land in the full backup (1..10 come with the app).
    eval_yaml_app(tt, target, "return box.snapshot()")
    insert_rows(tt, target, 11, 30)
    full_fragment = backup_and_upload(tt, target, storage, FULL_ID, plan_full_path)

    assert storage.keys() == [
        f"data/{FULL_ID}-{REPLICASET_UUID}.tar.zst",
        f"manifests/{FULL_ID}.json",
    ]

    verify_healthy(tt, storage.uri, manifests=1)

    rc, out = backup_last(tt, storage.uri, fmt="json")
    assert rc == 0, f"tt backup last failed:\n{out}"
    assert json.loads(out)["backup_id"] == FULL_ID

    # -- The incremental plan reads the manifest the upload just built.
    plan_inc, plan_inc_path = plan_json(
        tt,
        "incremental",
        storage.uri,
        config_path,
        tmp_path,
        "plan-inc.json",
    )
    assert plan_inc["mode"] == "incremental"
    assert plan_inc["previous_backup_id"] == FULL_ID
    assert plan_inc["base_full_backup_id"] == FULL_ID
    from_vclock = plan_inc["replicasets"][REPLICASET_UUID]["from_vclock"]
    assert from_vclock == full_fragment["vclock_end"]

    # -- Rows 31..40 precede the point, 41..50 must stay behind it.
    insert_rows(tt, target, 31, 40)
    point = eval_yaml_app(tt, target, "return {replica_id = 1, lsn = box.info.vclock[1]}")
    insert_rows(tt, target, 41, 50)

    inc_fragment = backup_and_upload(
        tt,
        target,
        storage,
        INC_ID,
        plan_inc_path,
        from_vclock=from_vclock,
    )
    assert inc_fragment["vclock_begin"] == from_vclock, (
        "the increment must continue where the plan said the chain ends"
    )

    verify_healthy(tt, storage.uri, manifests=2)

    rc, out = backup_last(tt, storage.uri, fmt="json")
    assert rc == 0, f"tt backup last failed:\n{out}"
    last = json.loads(out)
    assert last["backup_id"] == INC_ID
    assert last["previous_backup_id"] == FULL_ID

    # -- gc keeps the one live chain the pipeline produced.
    keys_before_gc = storage.keys()
    rc, out = backup_gc(tt, storage.uri, keep_full=1)
    assert rc == 0, f"tt backup gc failed:\n{out}"
    assert storage.keys() == keys_before_gc

    # -- Restore from the storage's bytes: the local archives are long gone.
    archives, checksums = download_chain(storage, INC_ID, tmp_path / "staging")
    assert len(archives) == 2, "the chain must resolve to full + increment"

    work_dir = tmp_path / "restored"
    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        ",".join(archives),
        "--checksums",
        ",".join(checksums),
        "--work-dir",
        str(work_dir),
        "--target-point",
        json.dumps(point),
        "--patch-uuid",
        APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    state = boot_restored(work_dir, tmp_path)

    assert state["lsn"] == point["lsn"], "the instance must come up on the point"
    # 40 rows: 1..10 from app start, 11..30 from the full backup, 31..40 from
    # the increment. 50 would mean the trim never happened, less than 40 that
    # part of the chain never replayed.
    assert state["rows"] == 40
    assert state["max_id"] == 40
    assert state["uuid"] == APP_INSTANCE_UUID
