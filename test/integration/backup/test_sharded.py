"""Backup of a sharded (vshard) cluster.

These tests cover the part of the backup flow that only exists once vshard is
in play: a *cluster* recovery point, created on the router through the
recovery point manager role, has to reach every storage master and show up in
the manifest fragment each shard produces.

The vshard-router backend of the role ships with vshard 0.1.42+
(roles/recovery-point-manager/backends/vshard-router.lua), which is the version
pinned in sharded_app/sharded_app-scm-1.rockspec.

Unlike test_backup_start.py these tests do not compare against golden
fragments: vclocks on a sharded cluster move on their own (bucket bookkeeping,
discovery, rebalancer), so the assertions are structural.
"""

import os

import pytest
from backup_helpers import archive_path_from_output, finalize_backup, start_backup
from sharded_helpers import STORAGE_REPLICASET_UUIDS, STORAGE_URIS, fragment_of

from utils import get_tarantool_version, is_tarantool_less

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"sharded backup requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
def test_recovery_point_manager_uses_vshard_router_backend(sharded_app):
    info = sharded_app.recovery_point_manager_info()

    assert info is not None, "the recovery point manager role did not start"
    assert info["backend"]["backend_type"] == "vshard-router"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
def test_cluster_recovery_point_reaches_every_storage(sharded_app):
    label = "rp-cluster-fanout"
    points = sharded_app.create_cluster_recovery_point(label)

    # The backend returns the created points keyed by replicaset name.
    assert sorted(points) == ["storage-001", "storage-002"], points
    for replicaset, created in points.items():
        assert len(created) == 1, (replicaset, created)
        assert created[0]["label"] == label, (replicaset, created)

    for instance in STORAGE_URIS:
        assert label in sharded_app.recovery_point_labels(instance), instance


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
def test_start_carries_cluster_recovery_point_into_every_fragment(sharded_app):
    label = "rp-cluster-backup"
    backup_id = "itest-sharded"

    sharded_app.eval_router("fill(50)")
    sharded_app.create_cluster_recovery_point(label)

    archives = {}
    try:
        for instance, uri in STORAGE_URIS.items():
            rc, out = start_backup(sharded_app, uri, backup_id)
            assert rc == 0, f"backup start on {instance} failed:\n{out}"
            archives[instance] = archive_path_from_output(out)

        # One archive per shard, all in the same per-backup directory, named
        # after the replicaset the shard belongs to.
        assert len({os.path.dirname(path) for path in archives.values()}) == 1, archives
        for instance, path in archives.items():
            uuid = STORAGE_REPLICASET_UUIDS[instance]
            assert os.path.basename(path) == f"{backup_id}-{uuid}.tar.zst"

        fragments = {inst: fragment_of(path) for inst, path in archives.items()}
        for instance, fragment in fragments.items():
            assert fragment["replicaset_uuid"] == STORAGE_REPLICASET_UUIDS[instance]
            assert fragment["instance_name"] == instance
            assert fragment["type"] == "full"
            labels = [point["label"] for point in fragment["recovery_points"]]
            assert label in labels, (instance, fragment["recovery_points"])

        # The shards are independent: same label, but each shard records the
        # point at its own LSN, and the replicaset UUIDs must not collide.
        uuids = {fragment["replicaset_uuid"] for fragment in fragments.values()}
        assert len(uuids) == len(fragments)
    finally:
        for instance, uri in STORAGE_URIS.items():
            rc, out = finalize_backup(sharded_app, uri, backup_id)
            assert rc == 0, f"backup finalize on {instance} failed:\n{out}"
