import os

import pytest
from backup_helpers import (
    TT_BACKUP_APP,
    app_instance,
    archive_path_from_output,
    backup_dir,
    eval_yaml_app,
    finalize_backup,
    get_backup_info_app,
    get_instance_info_app,
    inspect_backup_artifact,
    start_backup,
)

from utils import get_tarantool_version, is_tarantool_less

STORAGE_1_A = "storage-001-a"

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"backup start requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_full_artifact_and_open_backup_lifecycle(tt, tt_app, tmp_path):
    target = app_instance(tt_app, STORAGE_1_A)
    instance = get_instance_info_app(tt, target)

    eval_yaml_app(tt, target, "return box.snapshot()")

    recovery_point = eval_yaml_app(
        tt,
        target,
        "return box.backup.recovery_point.create({label = 'rp-itest'})",
    )
    assert recovery_point is not None

    backup_id = "itest-full"
    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup start failed:\n{out}"

    try:
        archive_path = archive_path_from_output(out)
        fragment = inspect_backup_artifact(
            archive_path,
            tmp_path / "full",
            backup_id,
            instance,
            expected_type="full",
        )
        assert fragment["vclock_begin"] is None
        assert any(name.endswith(".snap") for name in fragment["files"])
        assert any(name.endswith(".run") for name in fragment["files"])
        matching_points = [
            point for point in fragment["recovery_points"] if point.get("label") == "rp-itest"
        ]
        assert len(matching_points) == 1, fragment
        point = matching_points[0]
        assert point["replica_id"] == recovery_point["replica_id"]
        assert point["lsn"] == recovery_point["lsn"]
        assert point["timestamp"] == pytest.approx(recovery_point["timestamp"])

        # This is only a lifecycle ping. Artifact correctness is asserted above
        # from the archive and its manifest fragment, not from box.backup.info().
        assert get_backup_info_app(tt, target) is not None

        with open(archive_path, "rb") as src:
            archive_before = src.read()

        conflicting_id = "itest-already-open"
        conflict_rc, conflict_out = start_backup(tt, target, conflicting_id)
        assert conflict_rc == 2, conflict_out
        assert "backup already in progress" in conflict_out.lower()
        assert not os.path.exists(backup_dir(conflicting_id))
        with open(archive_path, "rb") as src:
            assert src.read() == archive_before
        assert get_backup_info_app(tt, target) is not None
    finally:
        rc, cleanup_out = finalize_backup(tt, target, backup_id)
        assert rc == 0, f"backup cleanup failed:\n{cleanup_out}"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_incremental_continues_full_artifact(tt, tt_app, tmp_path):
    target = app_instance(tt_app, STORAGE_1_A)
    instance = get_instance_info_app(tt, target)

    full_id = "itest-inc-full"
    rc, out = start_backup(tt, target, full_id)
    assert rc == 0, f"full backup start failed:\n{out}"
    try:
        full_fragment = inspect_backup_artifact(
            archive_path_from_output(out),
            tmp_path / "full",
            full_id,
            instance,
            expected_type="full",
        )
    finally:
        rc, finalize_out = finalize_backup(tt, target, full_id)
        assert rc == 0, f"full backup finalize failed:\n{finalize_out}"

    # Write more data after the full backup.
    eval_yaml_app(
        tt,
        target,
        "for i = 100, 105 do box.space.backup_test:insert({i, 'extra-' .. i}) end",
    )

    inc_id = "itest-inc"
    rc, out = start_backup(
        tt,
        target,
        inc_id,
        from_vclock=full_fragment["vclock_end"],
    )
    assert rc == 0, f"incremental backup start failed:\n{out}"

    try:
        inc_fragment = inspect_backup_artifact(
            archive_path_from_output(out),
            tmp_path / "incremental",
            inc_id,
            instance,
            expected_type="incremental",
        )
        assert inc_fragment["vclock_begin"] == full_fragment["vclock_end"]
        assert any(name.endswith(".xlog") for name in inc_fragment["files"])
        assert any(
            inc_fragment["vclock_end"].get(replica_id, 0) > lsn
            for replica_id, lsn in full_fragment["vclock_end"].items()
        )
        assert get_backup_info_app(tt, target) is not None
    finally:
        rc, cleanup_out = finalize_backup(tt, target, inc_id)
        assert rc == 0, f"incremental backup cleanup failed:\n{cleanup_out}"
