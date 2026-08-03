"""tt restore plan: resolving a moment in time into a restore, at the CLI.

Resolving a target time against a chain is unit-tested in cli/backup/chain, and
the topology comparison in cli/restore. What only the command boundary shows is
what an orchestrator consumes: the exit code per status -- it branches on those
numbers, one per recovery procedure -- the JSON document it reads nearest_safe
and the topology diff out of, and the archives that have to be on disk and
verified before anyone stops a cluster.

Most of this runs on a synthetic storage, because the shapes that produce a
broken chain or a topology boundary cannot be produced by taking backups. The
last test is the real pipeline end to end -- plan, start, upload, finalize,
restore plan, restore apply, boot -- and it is the one that shows the command
replaces the chain walk test_lifecycle.py had to hand-roll in Python before it
existed.
"""

import hashlib
import json
import os
import subprocess
from datetime import datetime, timezone
from pathlib import Path

import pytest
import tt_helper
import yaml
from backup_helpers import (
    app_instance,
    archive_path_from_output,
    backup_dir,
    backup_plan,
    backup_upload,
    eval_yaml_app,
    exec_split,
    finalize_backup,
    start_backup,
)
from storage_helpers import (
    INSTANCE,
    INSTANCE_B,
    REPLICASET,
    REPLICASET_B,
    STORAGE_BACKENDS,
    FileStorage,
    archive_key,
    shard_instance,
    topology_instance,
    write_manifest,
)

from utils import get_tarantool_version, is_tarantool_less

# Exit codes of tt restore plan. An orchestrator branches on these numbers
# without reading the document, so they are asserted as numbers.
PLAN_OK = 0
PLAN_FAILED = 1
PLAN_TOPOLOGY_BOUNDARY = 2
PLAN_NO_RECOVERY_POINT = 3
PLAN_CHAIN_BROKEN = 4
PLAN_OUT_OF_RANGE = 5
PLAN_TOPOLOGY_MISMATCH = 6

# The synthetic timeline. The manifests are created at CREATED, so a target
# before it is outside the storage's coverage, while one between CREATED and
# EARLY is inside it with no point to land on -- two different statuses.
CREATED = datetime(2026, 1, 1, tzinfo=timezone.utc)
BEFORE_COVERAGE = datetime(2025, 12, 31, tzinfo=timezone.utc)
INSIDE_COVERAGE = datetime(2026, 1, 1, 5, 0, tzinfo=timezone.utc)
EARLY = datetime(2026, 1, 1, 10, 0, tzinfo=timezone.utc)
BETWEEN = datetime(2026, 1, 1, 11, 0, tzinfo=timezone.utc)
LATE = datetime(2026, 1, 1, 12, 0, tzinfo=timezone.utc)
AFTER_COVERAGE = datetime(2026, 1, 2, tzinfo=timezone.utc)

# The second, unrelated full backup used for the chain break.
NEXT_CREATED = datetime(2026, 2, 1, tzinfo=timezone.utc)
NEXT_POINT = datetime(2026, 2, 1, 10, 0, tzinfo=timezone.utc)

EARLY_LABEL = "rp-early"
LATE_LABEL = "rp-late"
NEXT_LABEL = "rp-next"

FULL_ID = "2026-01-01-full"
INC_ID = "2026-01-02-inc"
NEXT_FULL_ID = "2026-02-01-full"

# Instance names of the synthetic cluster. The topology comparison goes by
# these and never by UUID, so they are what the cluster configs declare too.
STORAGE_1_A = "storage-001-a"
STORAGE_1_B = "storage-001-b"
STORAGE_2_A = "storage-002-a"
REPLICASET_1 = "storage-001"
REPLICASET_2 = "storage-002"

# A second instance of REPLICASET, so that a manifest can hand the master role
# over and make the topology of two adjacent manifests differ.
INSTANCE_2 = "aaaaaaaa-0000-0000-0000-000000000002"

# The restore_app fixture: memtx only, because this test boots what it
# restores and a restored instance with vinyl data cannot start.
APP_INSTANCE_UUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
APP_REPLICASET_UUID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

PITR_FULL_ID = "pitr-0001-full"
PITR_INC_ID = "pitr-0002-inc"
PITR_LABEL = "rp-pitr"

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"restore plan requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


def post_start_pitr_app(tt_app):
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(30, tt_app, tt_app.running_instances, ["running"])


TT_PITR_APP = dict(
    app_path="restore_app",
    app_name="app",
    instances=[STORAGE_1_A],
    running_targets=["app"],
    post_start=post_start_pitr_app,
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


# -- Running the command ----------------------------------------------------


def rfc3339(moment):
    return moment.strftime("%Y-%m-%dT%H:%M:%SZ")


def run_plan(tt, storage_uri, target, dest, config=None, fmt="json"):
    """Run tt restore plan, returning (exit code, stdout, stderr)."""
    args = [
        "restore",
        "plan",
        "--target-time",
        target,
        "--backup-storage",
        storage_uri,
        "-d",
        str(dest),
    ]
    if config is not None:
        args.extend(["-c", str(config)])
    if fmt is not None:
        args.extend(["--format", fmt])

    return exec_split(tt, *args)


def plan_document(tt, storage_uri, target, dest, config=None):
    """Run the command and return (exit code, parsed plan).

    The document is the command's product whatever the verdict, so it is parsed
    for a refusing status too: nearest_safe and the topology diff live there,
    and an orchestrator reads them off exactly this output.
    """
    rc, out, err = run_plan(tt, storage_uri, target, dest, config=config)
    assert out.strip(), f"tt restore plan printed no plan (exit {rc}):\n{err}"

    return rc, json.loads(out)


# -- Building a synthetic storage -------------------------------------------


def file_storage(tmp_path):
    root = tmp_path / "backups"
    root.mkdir()
    return FileStorage(str(root))


def downloaded(dest, backup_id, replicaset):
    """Where an archive lands: the storage groups keys by kind, a restore
    directory is handed to an operator and to scp, so it is flat."""
    return dest / f"{backup_id}-{replicaset}.tar.zst"


def point(label, lsn, at):
    """A recovery point as the core reports it.

    The identifier is `label`: a point created without one is never stitched
    into a cluster point, because there is nothing else to stitch by. The
    timestamp is fractional, as box.backup.info() returns it.
    """
    return {"label": label, "replica_id": 1, "lsn": lsn, "timestamp": at.timestamp()}


def shard(master_uuid, master_name, points=(), replicas=()):
    """One replicaset of a backup: the master its archive was taken on, the
    recovery points that archive carries, and the replicas beside the master."""
    return {
        "master_uuid": master_uuid,
        "master_name": master_name,
        "points": list(points),
        "instances": [(master_uuid, master_name), *replicas],
    }


def store_backup(
    storage,
    backup_id,
    shards,
    previous="",
    base=None,
    backup_type="full",
    vclock_begin=0,
    vclock_end=100,
    created=CREATED,
):
    """Store one backup: an archive per replicaset plus the manifest naming them.

    storage_helpers builds a single-replicaset backup carrying no recovery
    points, which is all the retention commands need. A restore plan resolves
    the points themselves and compares topologies between adjacent manifests,
    so both have to be settable per shard and per backup here.
    """
    manifest = {
        "schema_version": 1,
        "backup_id": backup_id,
        "previous_backup_id": previous,
        "base_full_backup_id": backup_id if base is None else base,
        "status": "OK",
        "creation_time": created.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "shards": {},
        "topology": {"replicasets": {}},
        "warnings": [],
    }

    for replicaset, spec in shards.items():
        content = f"archive of {backup_id} {replicaset}".encode()
        storage.put(archive_key(backup_id, replicaset), content)

        instance = shard_instance(
            backup_id,
            replicaset,
            spec["master_uuid"],
            spec["master_name"],
            backup_type,
            vclock_begin,
            vclock_end,
            content,
        )
        instance["artifact"]["recovery_points"] = spec["points"]

        manifest["shards"][replicaset] = {"instance": instance}
        manifest["topology"]["replicasets"][replicaset] = [
            topology_instance(uuid, name) for uuid, name in spec["instances"]
        ]

    write_manifest(storage, manifest)

    return manifest


def cluster_point(label, lsn_a, lsn_b, at):
    """Both replicasets carrying the same label: that is what makes a point
    cluster-wide, and only a cluster-wide point is resolvable at all."""
    return {
        REPLICASET: shard(INSTANCE, STORAGE_1_A, [point(label, lsn_a, at)]),
        REPLICASET_B: shard(INSTANCE_B, STORAGE_2_A, [point(label, lsn_b, at)]),
    }


def store_chain(storage):
    """A two-shard full backup and its increment, one cluster point in each.

    EARLY sits in the full and LATE in the increment, so a target resolving to
    EARLY must not drag the increment into the plan.
    """
    store_backup(storage, FULL_ID, cluster_point(EARLY_LABEL, 50, 55, EARLY))
    store_backup(
        storage,
        INC_ID,
        cluster_point(LATE_LABEL, 150, 177, LATE),
        previous=FULL_ID,
        base=FULL_ID,
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=200,
    )


def fresh_uuid(index):
    return f"cccccccc-0000-4000-8000-{index:012d}"


def cluster_config(tmp_path, replicasets):
    """Write the cluster config -c reads: replicaset names and their instances.

    Every instance gets a freshly minted UUID, the way a redeployed cluster
    looks. The comparison has to map the backup onto it by instance name; the
    UUIDs the backup was taken with are gone, and restore apply stamps the new
    ones in per node.
    """
    names = [instance for members in replicasets.values() for instance in members]
    uuids = {name: fresh_uuid(index) for index, name in enumerate(names, start=1)}
    groups = {
        replicaset: {
            "instances": {
                instance: {"database": {"instance_uuid": uuids[instance]}} for instance in members
            },
        }
        for replicaset, members in replicasets.items()
    }

    path = tmp_path / "cluster.yaml"
    path.write_text(
        yaml.safe_dump({"groups": {"storages": {"replicasets": groups}}}),
        encoding="utf-8",
    )

    return path


# -- The happy path ---------------------------------------------------------


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_plan_ok(tt, storage, tmp_path):
    """The whole product: the document, the files, and an untouched storage."""
    store_chain(storage)
    stored = storage.keys()
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(LATE), dest)

    assert rc == PLAN_OK, doc
    assert doc["status"] == "ok"
    assert doc["target_time"] == rfc3339(LATE)
    assert doc["recovery_point"]["label"] == LATE_LABEL
    assert doc["recovery_point"]["timestamp"] == rfc3339(LATE)
    assert doc["recovery_point"]["trim_to_by_replicaset"] == {
        REPLICASET: {"replica_id": 1, "lsn": 150},
        REPLICASET_B: {"replica_id": 1, "lsn": 177},
    }
    # Without -c the topology of the point was never compared with anything,
    # and that has to be said rather than pass for a clean check.
    assert any("-c" in warning for warning in doc["warnings"]), doc

    # Each replicaset is restored onto the instance its backup was taken on,
    # and the plan hands out the UUID that node has to own afterwards. Without
    # -c there is no cluster composition to draw a wipe list from, so the
    # targets carry no rejoin key.
    assert doc["restore_targets"] == {
        REPLICASET: {"instance_name": STORAGE_1_A, "patch_uuid": INSTANCE},
        REPLICASET_B: {"instance_name": STORAGE_2_A, "patch_uuid": INSTANCE_B},
    }, doc

    assert sorted(doc["download_plan"]) == [REPLICASET, REPLICASET_B]
    for replicaset, lsn in ((REPLICASET, 150), (REPLICASET_B, 177)):
        items = doc["download_plan"][replicaset]

        assert [item["backup_id"] for item in items] == [FULL_ID, INC_ID]
        assert [item["type"] for item in items] == ["full", "incremental"]
        # Only the archive holding the point is trimmed; the full replays whole.
        assert "trim_xlog" not in items[0], items[0]
        assert items[1]["trim_xlog"] is True
        assert items[1]["trim_to"] == {"replica_id": 1, "lsn": lsn}

        for item in items:
            artifact = Path(item["artifact"])
            assert artifact.is_file(), item
            assert artifact.parent.samefile(dest), item
            content = artifact.read_bytes()
            assert content == storage.read(archive_key(item["backup_id"], replicaset))
            assert hashlib.sha256(content).hexdigest() == item["checksum_sha256"], item

    # The manifests come down too: restore apply is handed archives, but an
    # operator diagnosing the plan has nothing else to read the chain from.
    for backup_id in (FULL_ID, INC_ID):
        manifest = json.loads((dest / f"{backup_id}.json").read_text(encoding="utf-8"))
        assert manifest["backup_id"] == backup_id

    assert storage.keys() == stored, "tt restore plan must not modify the storage"


def test_plan_stops_at_point(tt, tmp_path):
    """The plan carries the chain up to the point, not everything stored.

    Downloading the increment as well would be invisible in the document and
    expensive in the only situation this command runs in.
    """
    storage = file_storage(tmp_path)
    store_chain(storage)
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(EARLY), dest)

    assert rc == PLAN_OK, doc
    assert doc["recovery_point"]["label"] == EARLY_LABEL
    for replicaset in (REPLICASET, REPLICASET_B):
        items = doc["download_plan"][replicaset]
        assert [item["backup_id"] for item in items] == [FULL_ID]
        assert items[0]["trim_xlog"] is True

    assert sorted(path.name for path in dest.iterdir()) == sorted(
        [
            f"{FULL_ID}.json",
            f"{FULL_ID}-{REPLICASET}.tar.zst",
            f"{FULL_ID}-{REPLICASET_B}.tar.zst",
        ],
    )


# -- One exit code per reason a restore cannot go ahead ---------------------


def test_plan_out_of_range(tt, tmp_path):
    """A target the storage holds nothing for, on either side of its coverage."""
    storage = file_storage(tmp_path)
    store_chain(storage)
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(BEFORE_COVERAGE), dest)

    assert rc == PLAN_OUT_OF_RANGE, doc
    assert doc["status"] == "out_of_range"
    assert doc["nearest_safe"] == {"after": rfc3339(EARLY)}
    assert not dest.exists(), "a refused plan downloads nothing"

    rc, doc = plan_document(tt, storage.uri, rfc3339(AFTER_COVERAGE), dest)

    assert rc == PLAN_OUT_OF_RANGE, doc
    assert doc["nearest_safe"] == {"before": rfc3339(LATE)}


def test_plan_no_point(tt, tmp_path):
    """Inside the covered window but with no point to stop at.

    Distinct from out_of_range on purpose: the operator's next move is to take
    the neighbouring point offered here, not to reach for another storage.
    """
    storage = file_storage(tmp_path)
    store_chain(storage)
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(INSIDE_COVERAGE), dest)

    assert rc == PLAN_NO_RECOVERY_POINT, doc
    assert doc["status"] == "no_recovery_point"
    assert doc["nearest_safe"] == {"after": rfc3339(EARLY)}
    assert not dest.exists()


def test_plan_chain_broken(tt, tmp_path):
    """Two unrelated full backups: a target between them cannot be replayed to."""
    storage = file_storage(tmp_path)
    store_backup(storage, FULL_ID, cluster_point(EARLY_LABEL, 50, 55, EARLY))
    store_backup(
        storage,
        NEXT_FULL_ID,
        cluster_point(NEXT_LABEL, 50, 55, NEXT_POINT),
        created=NEXT_CREATED,
    )
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(BETWEEN), dest)

    assert rc == PLAN_CHAIN_BROKEN, doc
    assert doc["status"] == "chain_broken"
    assert doc["nearest_safe"] == {
        "before": rfc3339(EARLY),
        "after": rfc3339(NEXT_POINT),
    }
    assert not dest.exists()


def test_plan_boundary(tt, tmp_path):
    """The master changed mid-window, so the two sides are not one cluster state.

    The document is the RFC's topology_boundary example: a status, the reason,
    and the safe times on either side to retry with.
    """
    storage = file_storage(tmp_path)
    store_backup(
        storage,
        FULL_ID,
        {
            REPLICASET: shard(
                INSTANCE,
                STORAGE_1_A,
                [point(EARLY_LABEL, 50, EARLY)],
                replicas=[(INSTANCE_2, STORAGE_1_B)],
            ),
        },
    )
    store_backup(
        storage,
        INC_ID,
        {
            REPLICASET: shard(
                INSTANCE_2,
                STORAGE_1_B,
                [point(LATE_LABEL, 150, LATE)],
                replicas=[(INSTANCE, STORAGE_1_A)],
            ),
        },
        previous=FULL_ID,
        base=FULL_ID,
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=200,
    )
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(BETWEEN), dest)

    assert rc == PLAN_TOPOLOGY_BOUNDARY, doc
    assert doc["status"] == "topology_boundary"
    assert doc["reason"] == "target time falls between manifests with different topology"
    assert doc["nearest_safe"] == {"before": rfc3339(EARLY), "after": rfc3339(LATE)}
    assert "recovery_point" not in doc
    assert "download_plan" not in doc
    assert not dest.exists()


def test_plan_mismatch(tt, tmp_path):
    """A cluster config the backup does not map onto blocks the plan.

    Nothing is downloaded and nothing on the cluster is touched: the archives
    would have nowhere to go, and finding that out after a distribution over
    scp is the failure the check exists to prevent.
    """
    storage = file_storage(tmp_path)
    store_chain(storage)
    config = cluster_config(tmp_path, {"storage-042": ["storage-042-a"]})
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(LATE), dest, config=config)

    assert rc == PLAN_TOPOLOGY_MISMATCH, doc
    assert doc["status"] == "topology_mismatch"

    diff = doc["topology_diff"]
    missing = sorted(item["replicaset_uuid"] for item in diff["missing_replicasets"])
    assert missing == [REPLICASET, REPLICASET_B]
    assert [item["name"] for item in diff["extra_replicasets"]] == ["storage-042"]
    # No stored point matches this config, so there is no alternative to offer.
    assert "nearest_safe" not in doc
    assert not dest.exists()


def test_plan_mismatch_hint(tt, tmp_path):
    """A point of the old topology, offering the nearest point of the new one."""
    storage = file_storage(tmp_path)
    store_backup(
        storage,
        FULL_ID,
        {REPLICASET: shard(INSTANCE, STORAGE_1_A, [point(EARLY_LABEL, 50, EARLY)])},
    )
    store_backup(
        storage,
        INC_ID,
        cluster_point(LATE_LABEL, 150, 177, LATE),
        previous=FULL_ID,
        base=FULL_ID,
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=200,
    )
    config = cluster_config(
        tmp_path,
        {REPLICASET_1: [STORAGE_1_A], REPLICASET_2: [STORAGE_2_A]},
    )
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(EARLY), dest, config=config)

    assert rc == PLAN_TOPOLOGY_MISMATCH, doc
    assert [item["name"] for item in doc["topology_diff"]["extra_replicasets"]] == [
        REPLICASET_2,
    ]
    assert doc["nearest_safe"] == {"after": rfc3339(LATE)}


# -- Comparisons the plan survives ------------------------------------------


def test_plan_redeploy(tt, tmp_path):
    """A freshly deployed cluster: same instance names, brand-new UUIDs.

    This is the sanctioned restore target, so matching by UUID would reject
    exactly the case the topology check exists for.
    """
    storage = file_storage(tmp_path)
    store_chain(storage)
    config = cluster_config(
        tmp_path,
        {REPLICASET_1: [STORAGE_1_A], REPLICASET_2: [STORAGE_2_A]},
    )
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(LATE), dest, config=config)

    assert rc == PLAN_OK, doc
    assert "topology_diff" not in doc
    assert doc["warnings"] == [], doc
    # The UUID handed out is the one the backup was taken with, not the one the
    # redeployed cluster carries: it is what the restored files have to say.
    assert doc["restore_targets"][REPLICASET] == {
        "instance_name": STORAGE_1_A,
        "patch_uuid": INSTANCE,
    }, doc


def test_plan_replaced_replica(tt, tmp_path):
    """A replica swapped out inside a matched replicaset is a warning, not a wall.

    Replacing a dead replica is the usual companion of a restore. The two
    directions are not symmetric: an instance the backup knew and the config no
    longer has is a difference worth reporting, while the one that took its
    place is simply another node to wipe, and lands in the rejoin list with
    every other member.
    """
    storage = file_storage(tmp_path)
    store_backup(
        storage,
        FULL_ID,
        {
            REPLICASET: shard(
                INSTANCE,
                STORAGE_1_A,
                [point(EARLY_LABEL, 50, EARLY)],
                replicas=[(INSTANCE_2, STORAGE_1_B)],
            ),
        },
    )
    config = cluster_config(tmp_path, {REPLICASET_1: [STORAGE_1_A, "storage-001-c"]})
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(EARLY), dest, config=config)

    assert rc == PLAN_OK, doc
    assert "topology_diff" not in doc
    warnings = " ".join(doc["warnings"])
    assert STORAGE_1_B in warnings, doc
    assert "storage-001-c" not in warnings, doc
    assert doc["restore_targets"][REPLICASET] == {
        "instance_name": STORAGE_1_A,
        "patch_uuid": INSTANCE,
        "rejoin": ["storage-001-c"],
    }, doc
    assert Path(doc["download_plan"][REPLICASET][0]["artifact"]).is_file()


def test_plan_targets_wipe_every_other_member(tt, tmp_path):
    """A masters-only manifest against a real cluster: two members per shard.

    This is what every backup tt writes today looks like, because a manifest's
    topology is built from the per-shard fragments and a fragment describes the
    master alone. The replicas being absent from it is not a difference to
    report: only the target replays archives, and every other member is wiped
    and rejoins by name regardless. A successful plan says exactly that and
    warns about nothing.
    """
    storage = file_storage(tmp_path)
    store_chain(storage)
    config = cluster_config(
        tmp_path,
        {
            REPLICASET_1: [STORAGE_1_A, STORAGE_1_B],
            REPLICASET_2: [STORAGE_2_A, "storage-002-b"],
        },
    )
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(LATE), dest, config=config)

    assert rc == PLAN_OK, doc
    assert doc["warnings"] == [], doc
    assert doc["restore_targets"] == {
        REPLICASET: {
            "instance_name": STORAGE_1_A,
            "patch_uuid": INSTANCE,
            "rejoin": [STORAGE_1_B],
        },
        REPLICASET_B: {
            "instance_name": STORAGE_2_A,
            "patch_uuid": INSTANCE_B,
            "rejoin": ["storage-002-b"],
        },
    }, doc


# -- Refusals that are not a verdict on the storage -------------------------


def test_plan_empty_storage(tt, tmp_path):
    """A storage holding no manifests covers no moment at all."""
    storage = file_storage(tmp_path)
    dest = tmp_path / "restore"

    rc, doc = plan_document(tt, storage.uri, rfc3339(LATE), dest)

    assert rc == PLAN_OUT_OF_RANGE, doc
    assert doc["status"] == "out_of_range"
    assert "nearest_safe" not in doc
    assert not dest.exists()


def test_plan_needs_dir(tt, tmp_path):
    """Without -d there is nowhere to put the archives, so nothing is read."""
    storage = file_storage(tmp_path)
    store_chain(storage)

    rc, out, err = exec_split(
        tt,
        "restore",
        "plan",
        "--target-time",
        rfc3339(LATE),
        "--backup-storage",
        storage.uri,
    )

    assert rc == PLAN_FAILED
    assert '"dir"' in out + err
    assert "not set" in out + err


def test_plan_needs_storage(tt, tmp_path):
    """Without --backup-storage there is nothing to resolve the time against."""
    rc, out, err = exec_split(
        tt,
        "restore",
        "plan",
        "--target-time",
        rfc3339(LATE),
        "-d",
        str(tmp_path / "restore"),
    )

    assert rc == PLAN_FAILED
    assert '"backup-storage"' in out + err
    assert "not set" in out + err


def test_plan_bad_format(tt, tmp_path):
    storage = file_storage(tmp_path)
    store_chain(storage)

    rc, out, err = run_plan(tt, storage.uri, rfc3339(LATE), tmp_path / "restore", fmt="xml")

    assert rc == PLAN_FAILED
    assert "unsupported format" in (out + err).lower()


def test_plan_table(tt, tmp_path):
    """--format table renders the same plan for a human to read."""
    storage = file_storage(tmp_path)
    store_chain(storage)
    dest = tmp_path / "restore"

    rc, out, err = run_plan(tt, storage.uri, rfc3339(LATE), dest, fmt="table")
    report = out + err

    assert rc == PLAN_OK, report
    assert not report.lstrip().startswith("{")
    assert LATE_LABEL in report
    assert REPLICASET in report
    assert downloaded(dest, INC_ID, REPLICASET).is_file()


def test_plan_table_names_the_restore_targets(tt, tmp_path):
    """The table has to carry what the JSON does, or an operator reading the
    human form is left without the node to restore onto and the UUID to stamp.

    Run with -c, which is the only way the wipe list is filled: the replicaset
    UUID alone proves nothing, it is already printed by the download plan.
    """
    storage = file_storage(tmp_path)
    store_chain(storage)
    config = cluster_config(
        tmp_path,
        {
            REPLICASET_1: [STORAGE_1_A, STORAGE_1_B],
            REPLICASET_2: [STORAGE_2_A],
        },
    )
    dest = tmp_path / "restore"

    rc, out, err = run_plan(tt, storage.uri, rfc3339(LATE), dest, fmt="table", config=config)
    report = out + err

    assert rc == PLAN_OK, report
    assert "Restore targets" in report, report
    assert f"restore onto  {STORAGE_1_A}" in report, report
    assert f"--patch-uuid  {INSTANCE}" in report, report
    assert f"wipe, rejoins {STORAGE_1_B}" in report, report
    # The replicaset with nothing to wipe carries no rejoin line at all.
    assert report.count("wipe, rejoins") == 1, report


# -- An archive the plan cannot stand on ------------------------------------


def test_plan_corrupt_archive(tt, tmp_path):
    """A checksum that does not match stops the plan while the cluster is up.

    The archive is verified here, on the manager host, because after this it is
    only ever copied: a restore apply that unpacks the wrong bytes has already
    destroyed the work directory it was pointed at.
    """
    storage = file_storage(tmp_path)
    store_chain(storage)
    storage.put(archive_key(INC_ID, REPLICASET), b"not the archive the manifest describes")
    dest = tmp_path / "restore"

    rc, out, err = run_plan(tt, storage.uri, rfc3339(LATE), dest)

    assert rc == PLAN_FAILED
    assert "checksum" in (out + err).lower()
    # Neither the rejected archive nor a half-written one is left behind for a
    # later run to take for a complete download.
    assert not downloaded(dest, INC_ID, REPLICASET).exists()
    assert list(dest.glob("*.part")) == []


def test_plan_missing_archive(tt, tmp_path):
    """An archive the chain needs but the storage does not hold."""
    storage = file_storage(tmp_path)
    store_chain(storage)
    storage.delete(archive_key(INC_ID, REPLICASET_B))
    dest = tmp_path / "restore"

    rc, out, err = run_plan(tt, storage.uri, rfc3339(LATE), dest)

    assert rc == PLAN_FAILED
    assert "not in the backup storage" in out + err
    assert not downloaded(dest, INC_ID, REPLICASET_B).exists()


# -- The real pipeline, end to end ------------------------------------------


def insert_rows(tt, target, begin, end):
    eval_yaml_app(
        tt,
        target,
        f"for i = {begin}, {end} do box.space.restore_test:replace{{i, 'row-' .. i}} end "
        "return true",
    )


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


def backup_plan_file(tt, target, storage_uri, config_path, tmp_path, name):
    """Run tt backup plan and keep its raw output as the file upload reads."""
    rc, out = backup_plan(tt, target, storage_uri, config=config_path, fmt="json")
    assert rc == 0, f"tt backup plan --target={target} failed:\n{out}"

    path = tmp_path / name
    path.write_text(out.strip(), encoding="utf-8")

    return json.loads(out.strip()), str(path)


def backup_and_upload(tt, target, storage, backup_id, plan_path, from_vclock=None):
    """start -> upload -> finalize, the way the orchestrator drives a node."""
    rc, out = start_backup(tt, target, backup_id, from_vclock=from_vclock)
    assert rc == 0, f"tt backup start failed:\n{out}"

    archive = archive_path_from_output(out)
    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive,
        fragments=archive.removesuffix(".tar.zst") + ".json",
        plan=plan_path,
        backup_id=backup_id,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    rc, out = finalize_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup finalize failed:\n{out}"
    assert not Path(backup_dir(backup_id)).exists(), "finalize sweeps what upload left"


# Deliberately terse name: it prefixes the app's unix socket paths through
# pytest's tmp_path, and the whole thing must fit into sun_path on macOS.
@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_PITR_APP)
def test_plan_pitr(tt, tt_app, tmp_path):
    """Step 1 of the PITR procedure, driving steps 4 and 5 with its own output.

    Everything restore apply is given here comes out of the plan document --
    the ordered archives, their checksums, the per-replicaset position, the
    point label and the UUID to stamp -- which is exactly how the orchestrator
    calls it. Before this command existed test_lifecycle.py had to walk
    previous_backup_id and pull the archives out of the storage in Python to
    get the same inputs.

    The plan is read here from manifests a real `tt backup upload` wrote, which
    is what makes it the check that restore_targets is filled from the pipeline
    and not only from the manifests the other tests synthesize.
    """
    target = app_instance(tt_app, STORAGE_1_A)
    config_path = tt_app.path("config.yaml")

    storage = file_storage(tmp_path)

    # -- Rows 11..30 land in the full backup (1..10 come with the app).
    plan_full, plan_full_path = backup_plan_file(
        tt,
        "full",
        storage.uri,
        config_path,
        tmp_path,
        "plan-full.json",
    )
    assert plan_full["mode"] == "full"

    eval_yaml_app(tt, target, "return box.snapshot()")
    insert_rows(tt, target, 11, 30)
    backup_and_upload(tt, target, storage, PITR_FULL_ID, plan_full_path)

    # -- Rows 31..40 precede the recovery point, 41..50 must stay behind it.
    plan_inc, plan_inc_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        config_path,
        tmp_path,
        "plan-inc.json",
    )
    assert plan_inc["previous_backup_id"] == PITR_FULL_ID
    from_vclock = plan_inc["replicasets"][APP_REPLICASET_UUID]["from_vclock"]

    insert_rows(tt, target, 31, 40)
    created = eval_yaml_app(
        tt,
        target,
        f"return box.backup.recovery_point.create({{label = '{PITR_LABEL}'}})",
    )
    insert_rows(tt, target, 41, 50)

    backup_and_upload(
        tt,
        target,
        storage,
        PITR_INC_ID,
        plan_inc_path,
        from_vclock=from_vclock,
    )

    # -- Step 1: resolve the moment the point was taken at into a plan.
    dest = tmp_path / "restore"
    rc, doc = plan_document(
        tt,
        storage.uri,
        str(int(created["timestamp"])),
        dest,
        config=config_path,
    )

    assert rc == PLAN_OK, doc
    assert doc["recovery_point"]["label"] == PITR_LABEL
    assert doc["recovery_point"]["trim_to_by_replicaset"] == {
        APP_REPLICASET_UUID: {
            "replica_id": created["replica_id"],
            "lsn": created["lsn"],
        },
    }
    # The config describes the cluster the backup was taken on, so the topology
    # check has nothing to say about it.
    assert doc["warnings"] == [], doc

    items = doc["download_plan"][APP_REPLICASET_UUID]
    assert [item["backup_id"] for item in items] == [PITR_FULL_ID, PITR_INC_ID]
    assert items[-1]["trim_xlog"] is True

    # The node to restore onto and the UUID it has to own come from the plan
    # too. Both are read back out of the manifests the upload wrote, so this is
    # where restore_targets is checked against a real backup rather than a
    # hand-built one.
    target_of_shard = doc["restore_targets"][APP_REPLICASET_UUID]
    assert target_of_shard["instance_name"] == STORAGE_1_A
    # No rejoin key: the app has a single instance, so there is nobody to wipe.
    assert "rejoin" not in target_of_shard, doc

    # -- Step 4: apply on the node, with nothing but what the plan printed.
    work_dir = tmp_path / "restored"
    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        ",".join(item["artifact"] for item in items),
        "--checksums",
        ",".join(item["checksum_sha256"] for item in items),
        "--work-dir",
        str(work_dir),
        "--target-point",
        json.dumps(doc["recovery_point"]["trim_to_by_replicaset"][APP_REPLICASET_UUID]),
        "--point-name",
        doc["recovery_point"]["label"],
        "--patch-uuid",
        target_of_shard["patch_uuid"],
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    # -- Step 4a: the marker every node is compared by carries the point label.
    marker = json.loads(
        (tmp_path / "restored.restore_state.json").read_text(encoding="utf-8"),
    )
    assert marker["point_name"] == PITR_LABEL

    # -- Step 5: the instance comes up on the point, not on the end of the chain.
    state = boot_restored(work_dir, tmp_path)

    assert state["lsn"] == created["lsn"]
    # 40 rows: 1..10 from app start, 11..30 from the full backup, 31..40 from
    # the increment. 50 would mean the trim never happened, less than 40 that
    # part of the chain never replayed.
    assert state["rows"] == 40
    assert state["max_id"] == 40
    assert state["uuid"] == APP_INSTANCE_UUID
