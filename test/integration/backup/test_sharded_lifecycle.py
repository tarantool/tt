"""Restoring a sharded cluster to a cluster recovery point, end to end.

A vshard cluster keeps a replicaset a recovery point can never come from: the
router holds no data, writes no point, and is backed up all the same. Every
cluster-wide step then has to agree on which replicasets carry the point --
the chain that stitches it, the plan that resolves a target time against it,
and the apply that replays it -- and the only thing that shows they do is a
real cluster going through the whole procedure.

Nothing here is synthetic: the backups are taken off the running storages and
the router, the point is created through the recovery point manager role on
the router, and what `tt restore apply` is given comes out of the plan
document alone. The control run in the middle is the part that would have
passed before --replicasets existed: without it the router's empty shard keeps
the storages from stitching any cluster point at all, and the plan refuses.
"""

import json
import time
from pathlib import Path
from types import SimpleNamespace

import pytest
from backup_helpers import (
    archive_path_from_output,
    backup_plan,
    backup_upload,
    exec_split,
    finalize_backup,
    start_backup,
)
from sharded_helpers import (
    ROUTER_REPLICASET_UUID,
    ROUTER_URI,
    SHARDED_APP_NAME,
    STORAGE_REPLICASET_UUIDS,
    STORAGE_URIS,
    _wait_buckets_discovered,
    _wait_ready,
)
from storage_helpers import FileStorage

from utils import get_tarantool_version, is_tarantool_less

# Exit codes of tt restore plan, the numbers an orchestrator branches on.
PLAN_OK = 0
PLAN_NO_RECOVERY_POINT = 3

CONFIG_PATH = f"{SHARDED_APP_NAME}/config.yaml"

REPLICASET_UUIDS = {
    **STORAGE_REPLICASET_UUIDS,
    "router-001-a": ROUTER_REPLICASET_UUID,
}
# Every master of the cluster, storages and router alike: a backup covers the
# whole configured topology, and only the restore narrows it down.
BACKUP_URIS = {**STORAGE_URIS, "router-001-a": ROUTER_URI}

STORAGE_REPLICASETS = "storage-001,storage-002"
BUCKET_COUNT = 100

FULL_ID = "sharded-0001-full"
INC_ID = "sharded-0002-inc"
LABEL = "L1"

# Rows are written through the router, so `fill(n)` spreads ids 1..n over both
# storages. 1..50 land in the full backup, 51..70 in the increment ahead of the
# point, 71..90 behind it -- and those last twenty are what a restore to the
# point has to drop.
FULL_ROWS = 50
POINT_ROWS = 70
AFTER_ROWS = 90

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"sharded restore requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


def data_dir(app, instance):
    """The directory the instance keeps snapshots, journals and vinyl data in.

    sharded_app sets none of the three keys, so Tarantool puts all of them in
    the default var/lib/<instance name> under the application directory.
    """
    return Path(app.env_dir) / SHARDED_APP_NAME / "var" / "lib" / instance


def restore_marker(app, instance):
    """The marker apply writes beside the snapshot directory, named after it."""
    directory = data_dir(app, instance)

    return directory.parent / f"{directory.name}.restore_state.json"


def backup_round(app, storage, target, backup_id, tmp_path, plan_name):
    """plan -> start on every master -> one upload -> finalize, the way an
    orchestrator drives a sharded cluster: the plan is computed once, each node
    produces its own archive, and the manager stores them as one backup."""
    rc, out = backup_plan(app, target, storage.uri, config=CONFIG_PATH, fmt="json")
    assert rc == 0, f"tt backup plan --target={target} failed:\n{out}"

    plan = json.loads(out.strip())
    plan_path = tmp_path / plan_name
    plan_path.write_text(out.strip(), encoding="utf-8")

    archives, fragments = [], []
    for instance, uri in BACKUP_URIS.items():
        from_vclock = plan["replicasets"][REPLICASET_UUIDS[instance]].get("from_vclock")
        rc, out = start_backup(app, uri, backup_id, from_vclock=from_vclock)
        assert rc == 0, f"tt backup start on {instance} failed:\n{out}"

        archive = archive_path_from_output(out)
        archives.append(archive)
        fragments.append(archive.removesuffix(".tar.zst") + ".json")

    rc, out = backup_upload(
        app,
        storage.uri,
        archives=",".join(archives),
        fragments=",".join(fragments),
        plan=str(plan_path),
        backup_id=backup_id,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    for instance, uri in BACKUP_URIS.items():
        rc, out = finalize_backup(app, uri, backup_id)
        assert rc == 0, f"tt backup finalize on {instance} failed:\n{out}"

    return plan


def restore_plan(app, storage, target_time, dest, replicasets=None):
    """Run tt restore plan and return (exit code, parsed document).

    The document is the command's product whatever the verdict, so it is parsed
    for a refusing status too -- that is what the control run below reads.
    """
    args = [
        "restore",
        "plan",
        "--target-time",
        target_time,
        "--backup-storage",
        storage.uri,
        "-d",
        str(dest),
        "-c",
        CONFIG_PATH,
        "--format",
        "json",
    ]
    if replicasets is not None:
        args.extend(["--replicasets", replicasets])

    rc, out, err = exec_split(app, *args)
    assert out.strip(), f"tt restore plan printed no document (exit {rc}):\n{err}"

    return rc, json.loads(out)


def apply_shard(app, doc, replicaset_uuid):
    """Run tt restore apply on one replicaset with nothing but the plan.

    The archives, their order, the position to trim to, the point label and the
    UUID the node has to own afterwards all come out of the document; -c and
    --instance let apply read the instance's own three data directories out of
    the cluster configuration instead of being told where they are.
    """
    items = doc["download_plan"][replicaset_uuid]
    target = doc["restore_targets"][replicaset_uuid]

    return app.tt(
        "restore",
        "apply",
        "--archives",
        ",".join(item["artifact"] for item in items),
        "--checksums",
        ",".join(item["checksum_sha256"] for item in items),
        "--target-point",
        json.dumps(doc["recovery_point"]["trim_to_by_replicaset"][replicaset_uuid]),
        "--point-name",
        doc["recovery_point"]["label"],
        "--patch-uuid",
        target["patch_uuid"],
        "-c",
        CONFIG_PATH,
        "--instance",
        target["instance_name"],
        # The launch directory of the application, which is what the relative
        # var/lib/<instance name> of the configuration resolves against.
        "--work-dir",
        SHARDED_APP_NAME,
        assert_rc=False,
    )


def storage_state(app, instance):
    """What the storage holds: the rows of the memtx space, how many of them
    were written after the recovery point, and the instance UUID the restored
    files carry."""
    return app.eval(
        f"{SHARDED_APP_NAME}:{instance}",
        "return {"
        "  rows = box.space.backup_test:len(), "
        f" behind = #box.space.backup_test.index.pk:select({{{POINT_ROWS}}}, "
        "      {iterator = 'GT'}), "
        "  uuid = box.info.uuid, "
        "  status = box.info.status, "
        "}",
    )


def read_through_router(app, ids):
    """Read rows back the way an application does: the router picks the shard
    by bucket id, so this is the only check that the restored storages still
    own the buckets the router routes to."""
    id_list = ", ".join(str(row_id) for row_id in ids)

    return app.eval_router(
        "local vshard = require('vshard') "
        "local out = {} "
        f"for _, id in ipairs({{{id_list}}}) do "
        "  local bucket_id = vshard.router.bucket_id_mpcrc32({id}) "
        "  local rw = vshard.router.callrw(bucket_id, 'get', {id}) "
        "  local ro = vshard.router.callro(bucket_id, 'get', {id}) "
        "  out[tostring(id)] = {"
        "    rw = rw and rw[3] or 'MISSING', "
        "    ro = ro and ro[3] or 'MISSING', "
        "  } "
        "end "
        "return out",
    )


@pytest.fixture(scope="module")
def sharded_backups(sharded_app, tmp_path_factory):
    """A full and an incremental backup of the whole cluster, with a cluster
    recovery point taken between them.

    Module-scoped because both tests read the same storage: taking the backups
    means driving five commands over three instances twice, and only the first
    test changes the cluster afterwards.
    """
    tmp_path = tmp_path_factory.mktemp("bkp")
    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    # -- Rows 1..50 land in the full backup.
    sharded_app.eval_router(f"fill({FULL_ROWS})")
    for instance in STORAGE_URIS:
        sharded_app.eval(f"{SHARDED_APP_NAME}:{instance}", "return box.snapshot()")

    plan_full = backup_round(
        sharded_app,
        storage,
        "full",
        FULL_ID,
        tmp_path,
        "plan-full.json",
    )
    assert sorted(plan_full["replicasets"]) == sorted(REPLICASET_UUIDS.values()), (
        "a backup covers the whole configured topology, router included"
    )

    # A cluster point is recorded to the whole second, while the coverage of a
    # storage starts at the creation time of its earliest manifest, fraction and
    # all. A point taken in the same second as the full backup therefore names a
    # moment before the storage covers anything, and every plan against it is
    # out_of_range -- which is not the state this test is about.
    time.sleep(1.1)

    # -- Rows 51..70 precede the point, 71..90 must stay behind it.
    sharded_app.eval_router(f"fill({POINT_ROWS})")
    created = sharded_app.create_cluster_recovery_point(LABEL)
    sharded_app.eval_router(f"fill({AFTER_ROWS})")

    assert sorted(created) == ["storage-001", "storage-002"], created

    plan_inc = backup_round(
        sharded_app,
        storage,
        "incremental",
        INC_ID,
        tmp_path,
        "plan-inc.json",
    )
    assert plan_inc["previous_backup_id"] == FULL_ID

    return SimpleNamespace(
        storage=storage,
        label=LABEL,
        # The manager stamps one moment for the whole cluster; the second it
        # falls in is enough to name it.
        target_time=str(int(created["storage-001"][0]["timestamp"])),
    )


# Deliberately terse fixture directory names: they prefix every instance's
# console socket path, which has to fit into sun_path.
@pytest.mark.slow
@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
def test_sharded_restore_lifecycle(sharded_app, sharded_backups, tmp_path):
    """The whole PITR procedure on a live vshard cluster.

    What this pins that no single-instance test can: the point exists on the
    storages and nowhere else, so resolving it, downloading it, applying it and
    starting the cluster all have to agree that the router is not part of the
    restore. The cluster is asserted through its own router afterwards, because
    two self-consistent storages sitting on different states is exactly the
    failure a cluster point is meant to rule out.
    """
    storage = sharded_backups.storage
    target_time = sharded_backups.target_time

    # -- The control: this is the run that fails without --replicasets. The
    # router's shard carries no recovery point, so there is no label every
    # replicaset of the backup holds and no cluster point to resolve against.
    rc, control = restore_plan(
        sharded_app,
        storage,
        target_time,
        tmp_path / "restore-control",
    )
    assert rc == PLAN_NO_RECOVERY_POINT, control
    assert control["status"] == "no_recovery_point", control

    # -- Naming the storages is what makes the same moment resolvable.
    dest = tmp_path / "restore"
    rc, doc = restore_plan(
        sharded_app,
        storage,
        target_time,
        dest,
        replicasets=STORAGE_REPLICASETS,
    )

    assert rc == PLAN_OK, doc
    assert doc["recovery_point"]["label"] == LABEL
    assert doc["warnings"] == [
        'replicaset "router-001" is configured but not selected: '
        "it is not restored, bootstrap it fresh",
    ], doc

    storage_uuids = sorted(STORAGE_REPLICASET_UUIDS.values())
    assert sorted(doc["download_plan"]) == storage_uuids, doc
    assert sorted(doc["restore_targets"]) == storage_uuids, doc
    assert sorted(doc["recovery_point"]["trim_to_by_replicaset"]) == storage_uuids, doc

    for instance, replicaset_uuid in STORAGE_REPLICASET_UUIDS.items():
        items = doc["download_plan"][replicaset_uuid]
        assert [item["backup_id"] for item in items] == [FULL_ID, INC_ID], items
        assert items[-1]["trim_xlog"] is True, items
        assert doc["restore_targets"][replicaset_uuid]["instance_name"] == instance

    # -- Stop the storages and rebuild their data directories from the plan.
    # The router is left running: it keeps no data of its own, so there is
    # nothing on it to restore, and it rediscovers the buckets by itself once
    # the storages are back.
    for instance in STORAGE_URIS:
        sharded_app.tt("stop", f"{SHARDED_APP_NAME}:{instance}", "-y")

    for instance, replicaset_uuid in STORAGE_REPLICASET_UUIDS.items():
        rc, out = apply_shard(sharded_app, doc, replicaset_uuid)
        assert rc == 0, f"tt restore apply on {instance} failed:\n{out}"

    # -- The marker every node is compared by, beside the snapshot directory.
    for instance in STORAGE_URIS:
        marker_path = restore_marker(sharded_app, instance)
        assert marker_path.is_file(), f"no restore marker for {instance}: {marker_path}"

        marker = json.loads(marker_path.read_text(encoding="utf-8"))
        assert marker["point_name"] == LABEL, marker
        assert marker["instance_name"] == instance, marker

    for instance in STORAGE_URIS:
        sharded_app.tt("start", f"{SHARDED_APP_NAME}:{instance}")
    _wait_ready(sharded_app)
    _wait_buckets_discovered(sharded_app)

    # -- Each storage came up on the point, with the UUID the plan named.
    total = 0
    for instance, replicaset_uuid in STORAGE_REPLICASET_UUIDS.items():
        state = storage_state(sharded_app, instance)
        assert state["status"] == "running", (instance, state)
        assert state["behind"] == 0, (
            f"{instance} kept rows written after the recovery point: {state}"
        )
        assert state["uuid"] == doc["restore_targets"][replicaset_uuid]["patch_uuid"]
        total += state["rows"]

    # Ids 1..70 across both shards. 90 would mean the trim never happened, less
    # than 70 that part of a chain never replayed.
    assert total == POINT_ROWS

    # -- The cluster, as an application sees it: every bucket routable, no
    # alert, and the rows written before the point readable through the router.
    info = sharded_app.eval_router("return require('vshard').router.info()")
    assert info["alerts"] == [], info
    assert info["bucket"]["available_rw"] == BUCKET_COUNT, info
    assert info["bucket"]["unknown"] == 0, info

    before = [1, FULL_ROWS, POINT_ROWS]
    after = [POINT_ROWS + 1, AFTER_ROWS]
    rows = read_through_router(sharded_app, before + after)

    for row_id in before:
        assert rows[str(row_id)] == {
            "rw": f"row-{row_id}",
            "ro": f"row-{row_id}",
        }, (row_id, rows)

    for row_id in after:
        assert rows[str(row_id)] == {"rw": "MISSING", "ro": "MISSING"}, (row_id, rows)


@pytest.mark.slow
@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
def test_plan_selects_a_subset_of_the_storages(sharded_app, sharded_backups, tmp_path):
    """Half the storages is a plan like any other, and deliberately so.

    Restoring one shard of a sharded cluster and leaving the other on its
    current data is the operator's call to make, not tt's: the two shards are
    independent storages, and what a half restore means for the buckets between
    them is something only whoever asked for it knows. So the command plans it,
    says which replicasets it is leaving alone, and does not argue.
    """
    rc, doc = restore_plan(
        sharded_app,
        sharded_backups.storage,
        sharded_backups.target_time,
        tmp_path / "restore-subset",
        replicasets="storage-001",
    )

    assert rc == PLAN_OK, doc
    assert doc["recovery_point"]["label"] == LABEL
    assert doc["warnings"] == [
        'replicaset "router-001" is configured but not selected: '
        "it is not restored, bootstrap it fresh",
        'replicaset "storage-002" is configured but not selected: '
        "it is not restored, bootstrap it fresh",
    ], doc

    selected = STORAGE_REPLICASET_UUIDS["storage-001-a"]
    assert list(doc["restore_targets"]) == [selected], doc
    assert list(doc["download_plan"]) == [selected], doc
    assert list(doc["recovery_point"]["trim_to_by_replicaset"]) == [selected], doc
