import io
import json
from pathlib import Path

import pytest
from backup_helpers import (
    TT_BACKUP_APP,
    backup_plan,
    write_manifest_to_storage,
)

from utils import get_tarantool_version, is_tarantool_less

TESTDATA_DIR = Path(__file__).parent / "testdata"

# UUIDs pinned in test_app/config.yaml.
REPLICASET_UUID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
INSTANCE_UUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
INSTANCE_NAME = "storage-001-a"

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = f"backup plan requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"


def _load_manifest(name):
    return json.loads((TESTDATA_DIR / name).read_text())


PLAN_MANIFEST_FULL = _load_manifest("plan_manifest_full.json")
PLAN_MANIFEST_INCREMENTAL = _load_manifest("plan_manifest_incremental.json")
PLAN_MANIFEST_WRONG_TOPOLOGY = _load_manifest("plan_manifest_wrong_topology.json")
PLAN_MANIFEST_WRONG_MASTER = _load_manifest("plan_manifest_wrong_master.json")
PLAN_MANIFEST_ORPHAN = _load_manifest("plan_manifest_orphan.json")


def _config_path(tt_app):
    """Return the cluster config path inside the running test app."""
    return tt_app.path("config.yaml")


def _storage_uri(tmp_path):
    root = tmp_path / "backups"
    root.mkdir()
    return f"file://{root}", root


def _write_manifest(storage_dir, manifest):
    write_manifest_to_storage(str(storage_dir), manifest)


def _write_manifest_s3(garage, prefix, manifest):
    data = json.dumps(manifest).encode()
    key_prefix = f"{prefix}/" if prefix else ""
    garage.client.put_object(
        garage.bucket,
        f"{key_prefix}manifests/{manifest['backup_id']}.json",
        io.BytesIO(data),
        len(data),
    )


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_full_empty_storage(tt, tt_app, tmp_path):
    """Full plan on empty storage succeeds and returns mode=full."""
    storage_uri, _ = _storage_uri(tmp_path)

    rc, out = backup_plan(tt, "full", storage_uri, config=_config_path(tt_app), fmt="json")
    assert rc == 0, f"tt backup plan failed:\n{out}"

    plan = json.loads(out.strip())
    assert plan["mode"] == "full"
    assert "previous_backup_id" not in plan
    assert REPLICASET_UUID in plan["replicasets"]
    rs = plan["replicasets"][REPLICASET_UUID]
    assert rs["master_instance_uuid"] == INSTANCE_UUID
    assert rs["master_instance_name"] == INSTANCE_NAME
    assert "from_vclock" not in rs


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_full_ignores_storage_manifests(tt, tt_app, tmp_path):
    """Full plan succeeds even when manifests exist in storage."""
    storage_uri, storage_dir = _storage_uri(tmp_path)
    _write_manifest(storage_dir, PLAN_MANIFEST_FULL)

    rc, out = backup_plan(tt, "full", storage_uri, config=_config_path(tt_app), fmt="json")
    assert rc == 0, f"tt backup plan failed:\n{out}"

    plan = json.loads(out.strip())
    assert plan["mode"] == "full"
    assert "previous_backup_id" not in plan


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_incremental_no_backups(tt, tt_app, tmp_path):
    """Incremental plan on empty storage fails with a clear error."""
    storage_uri, _ = _storage_uri(tmp_path)

    rc, out = backup_plan(tt, "incremental", storage_uri, config=_config_path(tt_app))
    assert rc != 0
    assert "no backups found" in out.lower()
    assert "--target=full" in out


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_incremental_after_full(tt, tt_app, tmp_path):
    """Incremental plan after a full backup returns mode=incremental + from_vclock."""
    storage_uri, storage_dir = _storage_uri(tmp_path)
    _write_manifest(storage_dir, PLAN_MANIFEST_FULL)

    rc, out = backup_plan(tt, "incremental", storage_uri, config=_config_path(tt_app), fmt="json")
    assert rc == 0, f"tt backup plan failed:\n{out}"

    plan = json.loads(out.strip())
    assert plan["mode"] == "incremental"
    assert plan["previous_backup_id"] == "2026-01-01-full"

    rs = plan["replicasets"][REPLICASET_UUID]
    assert rs["master_instance_uuid"] == INSTANCE_UUID
    assert rs["master_instance_name"] == INSTANCE_NAME
    assert rs["from_vclock"] == {"1": 100}


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_incremental_after_incremental(tt, tt_app, tmp_path):
    """Incremental plan chains on top of the latest incremental."""
    storage_uri, storage_dir = _storage_uri(tmp_path)
    _write_manifest(storage_dir, PLAN_MANIFEST_FULL)
    _write_manifest(storage_dir, PLAN_MANIFEST_INCREMENTAL)

    rc, out = backup_plan(tt, "incremental", storage_uri, config=_config_path(tt_app), fmt="json")
    assert rc == 0, f"tt backup plan failed:\n{out}"

    plan = json.loads(out.strip())
    assert plan["mode"] == "incremental"
    assert plan["previous_backup_id"] == "2026-01-02-inc"
    rs = plan["replicasets"][REPLICASET_UUID]
    assert rs["from_vclock"] == {"1": 200}


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_incremental_orphan_chain(tt, tt_app, tmp_path):
    """Incremental plan fails when the latest manifest is an orphan."""
    storage_uri, storage_dir = _storage_uri(tmp_path)
    _write_manifest(storage_dir, PLAN_MANIFEST_FULL)
    _write_manifest(storage_dir, PLAN_MANIFEST_ORPHAN)

    rc, out = backup_plan(tt, "incremental", storage_uri, config=_config_path(tt_app))
    assert rc != 0
    assert "chain problems" in out.lower()
    assert "not found" in out.lower()
    assert "--target=full" in out


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_incremental_topology_changed(tt, tt_app, tmp_path):
    """Incremental plan fails when the replicaset set changed."""
    storage_uri, storage_dir = _storage_uri(tmp_path)
    _write_manifest(storage_dir, PLAN_MANIFEST_WRONG_TOPOLOGY)

    rc, out = backup_plan(tt, "incremental", storage_uri, config=_config_path(tt_app))
    assert rc != 0
    assert "topology changed" in out.lower()
    assert "--target=full" in out


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_incremental_master_changed(tt, tt_app, tmp_path):
    """Incremental plan fails when the backed-up master is no longer in the live topology."""
    storage_uri, storage_dir = _storage_uri(tmp_path)
    _write_manifest(storage_dir, PLAN_MANIFEST_WRONG_MASTER)

    rc, out = backup_plan(tt, "incremental", storage_uri, config=_config_path(tt_app))
    assert rc != 0
    assert "master changed" in out.lower()
    assert "--target=full" in out


def test_plan_invalid_storage_uri(tt):
    """Invalid storage URI is rejected."""
    rc, out = backup_plan(tt, "full", "not-a-uri", config="/dev/null")
    assert rc != 0
    assert "storage" in out.lower()


def test_plan_unsupported_target(tt, tmp_path):
    """Unsupported target is rejected."""
    storage_uri, _ = _storage_uri(tmp_path)
    rc, out = backup_plan(tt, "bogus", storage_uri, config="/dev/null")
    assert rc != 0
    assert "unsupported target" in out.lower()


def test_plan_missing_target_flag(tt, tmp_path):
    """Missing --target flag is rejected."""
    storage_uri, _ = _storage_uri(tmp_path)
    rc, out = tt.exec("backup", "plan", "--backup-storage", storage_uri, "-c", "/dev/null")
    assert rc != 0
    assert "required" in out.lower()


def test_plan_missing_config_flag(tt, tmp_path):
    """Missing --config flag is rejected."""
    storage_uri, _ = _storage_uri(tmp_path)
    rc, out = tt.exec("backup", "plan", "--target", "full", "--backup-storage", storage_uri)
    assert rc != 0
    assert "required" in out.lower()


def test_plan_missing_backup_storage_flag(tt):
    """Missing --backup-storage flag is rejected."""
    rc, out = tt.exec("backup", "plan", "--target", "full", "-c", "/dev/null")
    assert rc != 0
    assert "required" in out.lower()


def test_plan_invalid_format(tt, tmp_path):
    """Invalid format is rejected."""
    storage_uri, _ = _storage_uri(tmp_path)
    rc, out = backup_plan(tt, "full", storage_uri, config="/dev/null", fmt="xml")
    assert rc != 0
    assert "unsupported format" in out.lower()


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_table_format(tt, tt_app, tmp_path):
    """Table format produces human-readable output."""
    storage_uri, _ = _storage_uri(tmp_path)

    rc, out = backup_plan(tt, "full", storage_uri, config=_config_path(tt_app), fmt="table")
    assert rc == 0, f"tt backup plan failed:\n{out}"
    assert not out.lstrip().startswith("{")
    assert "full" in out
    assert "Backup plan" in out


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.docker
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_full_s3(tt, tt_app, garage):
    """Full plan works with S3 storage."""
    uri = (
        f"s3+http://{garage.endpoint}/{garage.bucket}"
        f"?Region={garage.region}&AccessKeyID={garage.access_key}"
        f"&SecretAccessKey={garage.secret_key}"
    )
    try:
        rc, out = backup_plan(tt, "full", uri, config=_config_path(tt_app), fmt="json")
        assert rc == 0, f"tt backup plan failed:\n{out}"
        plan = json.loads(out.strip())
        assert plan["mode"] == "full"
    finally:
        for obj in garage.client.list_objects(garage.bucket, recursive=True):
            garage.client.remove_object(garage.bucket, obj.object_name)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.docker
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_plan_incremental_s3(tt, tt_app, garage):
    """Incremental plan works with S3 storage."""
    uri = (
        f"s3+http://{garage.endpoint}/{garage.bucket}"
        f"?Region={garage.region}&AccessKeyID={garage.access_key}"
        f"&SecretAccessKey={garage.secret_key}"
    )
    try:
        _write_manifest_s3(garage, "", PLAN_MANIFEST_FULL)
        rc, out = backup_plan(tt, "incremental", uri, config=_config_path(tt_app), fmt="json")
        assert rc == 0, f"tt backup plan failed:\n{out}"
        plan = json.loads(out.strip())
        assert plan["mode"] == "incremental"
        assert plan["previous_backup_id"] == "2026-01-01-full"
    finally:
        for obj in garage.client.list_objects(garage.bucket, recursive=True):
            garage.client.remove_object(garage.bucket, obj.object_name)
