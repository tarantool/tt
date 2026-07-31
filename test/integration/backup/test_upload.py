import json
import os
from pathlib import Path

import pytest
from backup_helpers import backup_upload
from storage_helpers import STORAGE_BACKENDS, FileStorage, S3Storage

TESTDATA_DIR = Path(__file__).parent / "testdata"

REPLICASET = "11111111-1111-1111-1111-111111111111"
REPLICASET_B = "22222222-2222-2222-2222-222222222222"
INSTANCE_UUID = "aaaaaaaa-0000-0000-0000-000000000001"
INSTANCE_UUID_B = "bbbbbbbb-0000-0000-0000-000000000001"
BACKUP_ID = "20260326T120000Z"
ARCHIVE_CONTENT = b"archive payload abc"
ARCHIVE_CONTENT_B = b"archive payload def"


def _make_fragment(
    replicaset=REPLICASET,
    instance_uuid=INSTANCE_UUID,
    instance_name="router-001",
    backup_type="full",
    vclock_end=100,
    vclock_begin=None,
):
    return {
        "replicaset_uuid": replicaset,
        "instance_uuid": instance_uuid,
        "instance_name": instance_name,
        "hostname": "localhost",
        "type": backup_type,
        "vclock_begin": vclock_begin
        if vclock_begin is not None
        else ({"1": 50} if backup_type == "incremental" else None),
        "vclock_end": {"1": vclock_end},
        "files": ["00000000000000000100.xlog"],
        "checksum_sha256": "abc123",
        "recovery_points": [],
    }


def _make_plan(replicasets=None, mode="full", previous_backup_id=None, base_full_backup_id=None):
    if replicasets is None:
        replicasets = {
            REPLICASET: {
                "master_instance_uuid": INSTANCE_UUID,
                "master_instance_name": "router-001",
            },
        }
    plan = {"mode": mode, "replicasets": replicasets}
    if previous_backup_id:
        plan["previous_backup_id"] = previous_backup_id
    if base_full_backup_id:
        plan["base_full_backup_id"] = base_full_backup_id
    return plan


def _write_json(path, data):
    with open(path, "w") as f:
        json.dump(data, f)


def _write_archive(dir_path, backup_id, replicaset, content=ARCHIVE_CONTENT):
    name = f"{backup_id}-{replicaset}.tar.zst"
    path = os.path.join(dir_path, name)
    with open(path, "wb") as f:
        f.write(content)
    return path


def _prepare_inputs(tmp_path, plan=None, fragment=None, backup_id=BACKUP_ID):
    """Create archive, fragment and plan files under tmp_path and return their paths."""
    work = tmp_path / "work"
    work.mkdir()

    plan = plan or _make_plan()
    fragment = fragment or _make_fragment()

    plan_path = work / "plan.json"
    _write_json(plan_path, plan)

    fragment_path = work / "fragment.json"
    _write_json(fragment_path, fragment)

    archive_path = _write_archive(str(work), backup_id, REPLICASET)

    return str(archive_path), str(fragment_path), str(plan_path)


def _load_golden(name):
    return json.loads((TESTDATA_DIR / name).read_text())


@pytest.fixture
def storage(request, tmp_path):
    backend, prefix = request.param
    if backend == "file":
        root = tmp_path / "backups"
        root.mkdir()
        yield FileStorage(str(root), prefix)
        return

    garage = request.getfixturevalue("garage")
    s3 = S3Storage(garage, prefix)
    try:
        yield s3
    finally:
        for key in s3.keys():
            s3.delete(key)


UPLOAD_CASES = [
    pytest.param(
        {"cluster_name": None, "environment": None},
        "",
        id="no-cluster-no-env",
    ),
    pytest.param(
        {"cluster_name": "payments", "environment": None},
        "payments/",
        id="cluster-only",
    ),
    pytest.param(
        {"cluster_name": "payments", "environment": "production"},
        "payments/production/",
        id="cluster-and-env",
    ),
]


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
@pytest.mark.parametrize("flags,expected_prefix", UPLOAD_CASES)
def test_upload_happy_path(tt, tmp_path, storage, flags, expected_prefix):
    """Upload a single-shard full backup and verify storage contents."""
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
        cluster_name=flags["cluster_name"],
        environment=flags["environment"],
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"
    assert "uploaded successfully" in out.lower()

    # The archive is in storage under the expected key.
    expected_archive_key = expected_prefix + f"data/{BACKUP_ID}-{REPLICASET}.tar.zst"
    assert expected_archive_key in storage.keys()
    assert storage.read(expected_archive_key) == ARCHIVE_CONTENT

    # The manifest is in storage under the expected key.
    expected_manifest_key = expected_prefix + f"manifests/{BACKUP_ID}.json"
    assert expected_manifest_key in storage.keys()

    # The manifest matches the golden file (creation_time is non-deterministic).
    uploaded_manifest = json.loads(storage.read(expected_manifest_key))
    golden = _load_golden("upload_manifest_full.golden.json")

    # The artifact path in the manifest must include the prefix.
    expected_artifact_path = expected_prefix + f"data/{BACKUP_ID}-{REPLICASET}.tar.zst"
    uploaded_manifest["shards"][REPLICASET]["instance"]["artifact"]["path"] = expected_artifact_path
    golden["shards"][REPLICASET]["instance"]["artifact"]["path"] = expected_artifact_path

    # creation_time is set to time.Now() by upload — strip it before comparing.
    uploaded_manifest.pop("creation_time", None)
    golden.pop("creation_time", None)

    assert uploaded_manifest == golden, (
        f"manifest mismatch:\n"
        f"uploaded: {json.dumps(uploaded_manifest, indent=2)}\n"
        f"golden:   {json.dumps(golden, indent=2)}"
    )


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_multi_shard(tt, tmp_path, storage):
    """Upload two shards and verify both archives and the manifest are stored."""
    work = tmp_path / "work"
    work.mkdir()

    frag_a = _make_fragment(replicaset=REPLICASET)
    frag_b = _make_fragment(
        replicaset=REPLICASET_B,
        instance_uuid=INSTANCE_UUID_B,
        instance_name="router-002",
        vclock_end=200,
    )

    plan = _make_plan(
        replicasets={
            REPLICASET: {
                "master_instance_uuid": INSTANCE_UUID,
                "master_instance_name": "router-001",
            },
            REPLICASET_B: {
                "master_instance_uuid": INSTANCE_UUID_B,
                "master_instance_name": "router-002",
            },
        },
    )

    plan_path = work / "plan.json"
    _write_json(plan_path, plan)

    frag_a_path = work / "fragment_a.json"
    frag_b_path = work / "fragment_b.json"
    _write_json(frag_a_path, frag_a)
    _write_json(frag_b_path, frag_b)

    archive_a = _write_archive(str(work), BACKUP_ID, REPLICASET, ARCHIVE_CONTENT)
    archive_b = _write_archive(str(work), BACKUP_ID, REPLICASET_B, ARCHIVE_CONTENT_B)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=f"{archive_a},{archive_b}",
        fragments=f"{frag_a_path},{frag_b_path}",
        plan=str(plan_path),
        backup_id=BACKUP_ID,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"
    assert "2 shards" in out

    keys = storage.keys()
    assert f"data/{BACKUP_ID}-{REPLICASET}.tar.zst" in keys
    assert f"data/{BACKUP_ID}-{REPLICASET_B}.tar.zst" in keys
    assert f"manifests/{BACKUP_ID}.json" in keys

    # Verify archive contents were uploaded correctly.
    assert storage.read(f"data/{BACKUP_ID}-{REPLICASET}.tar.zst") == ARCHIVE_CONTENT
    assert storage.read(f"data/{BACKUP_ID}-{REPLICASET_B}.tar.zst") == ARCHIVE_CONTENT_B

    # Verify the manifest references both shards.
    manifest = json.loads(storage.read(f"manifests/{BACKUP_ID}.json"))
    assert REPLICASET in manifest["shards"]
    assert REPLICASET_B in manifest["shards"]
    assert manifest["shards"][REPLICASET]["instance"]["artifact"]["size_bytes"] == len(
        ARCHIVE_CONTENT,
    )
    assert manifest["shards"][REPLICASET_B]["instance"]["artifact"]["size_bytes"] == len(
        ARCHIVE_CONTENT_B,
    )


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_incremental(tt, tmp_path, storage):
    """Upload an incremental backup with previous_backup_id and base_full_backup_id."""
    previous_id = "20260325T120000Z"
    base_id = "20260325T120000Z"

    plan = _make_plan(
        mode="incremental",
        previous_backup_id=previous_id,
        base_full_backup_id=base_id,
    )
    fragment = _make_fragment(backup_type="incremental", vclock_end=200, vclock_begin={"1": 100})

    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path, plan=plan, fragment=fragment)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    manifest = json.loads(storage.read(f"manifests/{BACKUP_ID}.json"))
    assert manifest["previous_backup_id"] == previous_id
    assert manifest["base_full_backup_id"] == base_id
    assert manifest["shards"][REPLICASET]["instance"]["artifact"]["type"] == "incremental"
    assert manifest["shards"][REPLICASET]["instance"]["vclock_begin"] == {"1": 100}
    assert manifest["shards"][REPLICASET]["instance"]["vclock_end"] == {"1": 200}


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_keep_local(tt, tmp_path, storage):
    """--keep-local preserves the local archive after upload."""
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
        keep_local=True,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    assert os.path.isfile(archive_path), "local archive was removed despite --keep-local"
    assert f"data/{BACKUP_ID}-{REPLICASET}.tar.zst" in storage.keys()


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_removes_local_by_default(tt, tmp_path, storage):
    """Without --keep-local, local archives are removed after successful upload."""
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    assert not os.path.exists(archive_path), "local archive was not removed"
    assert f"data/{BACKUP_ID}-{REPLICASET}.tar.zst" in storage.keys()


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_with_cluster_name_prefixes_keys(tt, tmp_path, storage):
    """--cluster-name prefixes archive and manifest keys in storage."""
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
        cluster_name="my-cluster",
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    keys = storage.keys()
    assert any(k.startswith("my-cluster/data/") for k in keys)
    assert any(k.startswith("my-cluster/manifests/") for k in keys)


def test_upload_missing_backup_id(tt, tmp_path):
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)
    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = tt.exec(
        "backup",
        "upload",
        "--archives",
        archive_path,
        "--fragments",
        fragment_path,
        "--plan",
        plan_path,
        "--backup-storage",
        storage_uri,
    )
    assert rc != 0
    assert "required" in out.lower()


def test_upload_missing_archives(tt, tmp_path):
    _, fragment_path, plan_path = _prepare_inputs(tmp_path)
    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = tt.exec(
        "backup",
        "upload",
        "--fragments",
        fragment_path,
        "--plan",
        plan_path,
        "--backup-storage",
        storage_uri,
        "--backup-id",
        BACKUP_ID,
    )
    assert rc != 0
    assert "required" in out.lower()


def test_upload_missing_plan(tt, tmp_path):
    archive_path, fragment_path, _ = _prepare_inputs(tmp_path)
    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = tt.exec(
        "backup",
        "upload",
        "--archives",
        archive_path,
        "--fragments",
        fragment_path,
        "--backup-storage",
        storage_uri,
        "--backup-id",
        BACKUP_ID,
    )
    assert rc != 0
    assert "required" in out.lower()


def test_upload_missing_backup_storage(tt, tmp_path):
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = tt.exec(
        "backup",
        "upload",
        "--archives",
        archive_path,
        "--fragments",
        fragment_path,
        "--plan",
        plan_path,
        "--backup-id",
        BACKUP_ID,
    )
    assert rc != 0
    assert "required" in out.lower()


def test_upload_nonexistent_archive(tt, tmp_path):
    _, fragment_path, plan_path = _prepare_inputs(tmp_path)
    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = backup_upload(
        tt,
        storage_uri,
        archives="/nonexistent/archive.tar.zst",
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "stat archive" in out.lower()


def test_upload_nonexistent_plan(tt, tmp_path):
    archive_path, fragment_path, _ = _prepare_inputs(tmp_path)
    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = backup_upload(
        tt,
        storage_uri,
        archives=archive_path,
        fragments=fragment_path,
        plan="/nonexistent/plan.json",
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "read plan" in out.lower()


def test_upload_nonexistent_fragment(tt, tmp_path):
    archive_path, _, plan_path = _prepare_inputs(tmp_path)
    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = backup_upload(
        tt,
        storage_uri,
        archives=archive_path,
        fragments="/nonexistent/fragment.json",
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "read fragment" in out.lower()


def test_upload_invalid_plan_json(tt, tmp_path):
    archive_path, fragment_path, _ = _prepare_inputs(tmp_path)
    work = tmp_path / "work"
    bad_plan = work / "bad_plan.json"
    bad_plan.write_text("{invalid json")

    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = backup_upload(
        tt,
        storage_uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=str(bad_plan),
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "decode plan" in out.lower()


def test_upload_invalid_fragment_json(tt, tmp_path):
    archive_path, _, plan_path = _prepare_inputs(tmp_path)
    work = tmp_path / "work"
    bad_fragment = work / "bad_fragment.json"
    bad_fragment.write_text("{invalid json")

    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = backup_upload(
        tt,
        storage_uri,
        archives=archive_path,
        fragments=str(bad_fragment),
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "read fragment" in out.lower()


def test_upload_archive_wrong_backup_id_prefix(tt, tmp_path):
    """Archive filename that doesn't start with the backup-id is rejected."""
    work = tmp_path / "work"
    work.mkdir()

    # Write an archive with a different backup-id in the filename.
    wrong_path = _write_archive(str(work), "wrong-id", REPLICASET)

    plan = _make_plan()
    plan_path = work / "plan.json"
    _write_json(plan_path, plan)

    fragment = _make_fragment()
    fragment_path = work / "fragment.json"
    _write_json(fragment_path, fragment)

    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = backup_upload(
        tt,
        storage_uri,
        archives=wrong_path,
        fragments=str(fragment_path),
        plan=str(plan_path),
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "does not start with backup-id" in out.lower()


def test_upload_archive_wrong_extension(tt, tmp_path):
    """Archive without .tar.zst extension is rejected."""
    work = tmp_path / "work"
    work.mkdir()

    bad_archive = os.path.join(str(work), f"{BACKUP_ID}-{REPLICASET}.zip")
    with open(bad_archive, "wb") as f:
        f.write(ARCHIVE_CONTENT)

    plan = _make_plan()
    plan_path = work / "plan.json"
    _write_json(plan_path, plan)

    fragment = _make_fragment()
    fragment_path = work / "fragment.json"
    _write_json(fragment_path, fragment)

    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = backup_upload(
        tt,
        storage_uri,
        archives=bad_archive,
        fragments=str(fragment_path),
        plan=str(plan_path),
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert ".tar.zst" in out.lower()


def test_upload_fragment_missing_replicaset(tt, tmp_path):
    """Plan expects a replicaset that has no fragment."""
    work = tmp_path / "work"
    work.mkdir()

    # Plan expects two replicasets, but only one fragment is provided.
    plan = _make_plan(
        replicasets={
            REPLICASET: {
                "master_instance_uuid": INSTANCE_UUID,
                "master_instance_name": "router-001",
            },
            REPLICASET_B: {
                "master_instance_uuid": INSTANCE_UUID_B,
                "master_instance_name": "router-002",
            },
        },
    )
    plan_path = work / "plan.json"
    _write_json(plan_path, plan)

    fragment = _make_fragment(replicaset=REPLICASET)
    fragment_path = work / "fragment.json"
    _write_json(fragment_path, fragment)

    archive_path = _write_archive(str(work), BACKUP_ID, REPLICASET)

    storage_uri = f"file://{tmp_path / 'backups'}"

    rc, out = backup_upload(
        tt,
        storage_uri,
        archives=archive_path,
        fragments=str(fragment_path),
        plan=str(plan_path),
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "missing" in out.lower()
    assert REPLICASET_B in out


def test_upload_invalid_storage_uri(tt, tmp_path):
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = backup_upload(
        tt,
        "not-a-uri",
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "storage" in out.lower()
