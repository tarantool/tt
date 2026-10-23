import hashlib
import json
import os
from pathlib import Path

import pytest
from backup_helpers import backup_upload, backup_verify
from storage_helpers import STORAGE_BACKENDS, FileStorage, S3Storage

TESTDATA_DIR = Path(__file__).parent / "testdata"

REPLICASET = "11111111-1111-1111-1111-111111111111"
REPLICASET_B = "22222222-2222-2222-2222-222222222222"
INSTANCE_UUID = "aaaaaaaa-0000-0000-0000-000000000001"
INSTANCE_UUID_B = "bbbbbbbb-0000-0000-0000-000000000001"
BACKUP_ID = "20260326T120000Z"
FORMAT_VERSION = 1
ARCHIVE_CONTENT = b"archive payload abc"
ARCHIVE_CONTENT_B = b"archive payload def"


def _make_fragment(
    replicaset=REPLICASET,
    instance_uuid=INSTANCE_UUID,
    instance_name="router-001",
    backup_type="full",
    vclock_end=100,
    vclock_begin=None,
    content=ARCHIVE_CONTENT,
):
    """A fragment describing the archive built from content.

    The checksum has to be the archive's real one: upload reads every archive
    through before storing it, and a fragment that disagrees is a copy that
    went wrong on the way here.
    """
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
        "checksum_sha256": hashlib.sha256(content).hexdigest(),
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
    plan = {"format_version": FORMAT_VERSION, "mode": mode, "replicasets": replicasets}
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


def _prepare_two_shard_inputs(tmp_path):
    """Create two archives, their fragments and a plan naming both replicasets."""
    work = tmp_path / "work"
    work.mkdir()

    plan_path = work / "plan.json"
    _write_json(
        plan_path,
        _make_plan(
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
        ),
    )

    frag_a = work / "fragment_a.json"
    frag_b = work / "fragment_b.json"
    _write_json(frag_a, _make_fragment(replicaset=REPLICASET))
    _write_json(
        frag_b,
        _make_fragment(
            replicaset=REPLICASET_B,
            instance_uuid=INSTANCE_UUID_B,
            instance_name="router-002",
            content=ARCHIVE_CONTENT_B,
        ),
    )

    archive_a = _write_archive(str(work), BACKUP_ID, REPLICASET, ARCHIVE_CONTENT)
    archive_b = _write_archive(str(work), BACKUP_ID, REPLICASET_B, ARCHIVE_CONTENT_B)

    return [archive_a, archive_b], [str(frag_a), str(frag_b)], str(plan_path)


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
    assert "uploaded" in out.lower()
    assert "status ok" in out.lower()

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

    # The artifact path is relative to the storage root the manifest sits
    # beside: the <cluster_name>/<environment>/ segment belongs to the object
    # key, not to the path every reader resolves. The golden file carries it,
    # so the comparison below covers it — this is the explicit statement.
    assert (
        uploaded_manifest["shards"][REPLICASET]["instance"]["artifact"]["path"]
        == f"data/{BACKUP_ID}-{REPLICASET}.tar.zst"
    )

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
        content=ARCHIVE_CONTENT_B,
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
    """Upload an incremental backup with previous_backup_id and base_full_backup_id.

    The full backup it continues has to be in the storage: upload compares the
    plan against the chain head, and an increment continuing a backup nobody
    stored is refused.
    """
    previous_id = "20260325T120000Z"
    base_id = previous_id

    full_dir = tmp_path / "full"
    full_dir.mkdir()
    full_archive = _write_archive(str(full_dir), previous_id, REPLICASET)
    full_fragment = full_dir / "fragment.json"
    _write_json(full_fragment, _make_fragment())
    full_plan = full_dir / "plan.json"
    _write_json(full_plan, _make_plan())

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=full_archive,
        fragments=str(full_fragment),
        plan=str(full_plan),
        backup_id=previous_id,
    )
    assert rc == 0, f"tt backup upload of the base full backup failed:\n{out}"

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


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_with_cluster_name_is_readable(tt, tmp_path, storage):
    """A backup taken with --cluster-name verifies clean when read at its prefix.

    The manifest records the archive relative to the storage root it sits
    beside. A manifest repeating its own <cluster_name>/<environment>/ segment
    sends every reader looking for <prefix>/<prefix>/data/…: restore cannot
    download the archive, verify reports the live one missing and the stored
    one dangling, and gc counts the stored one as an orphan.
    """
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
        cluster_name="payments",
        environment="production",
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    rc, out = backup_verify(
        tt,
        storage.uri,
        fmt="json",
        cluster_name="payments",
        environment="production",
    )
    assert rc == 0, f"tt backup verify reported problems:\n{out}"

    report = json.loads(out)
    assert report["issues"] == []
    assert report["manifests_checked"] == 1
    assert report["archives_checked"] == 1


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


def _upload_full(tt, storage_uri, tmp_path, backup_id, name="full", master=INSTANCE_UUID):
    """Upload a one-shard full backup under its own directory."""
    work = tmp_path / name
    work.mkdir()

    archive = _write_archive(str(work), backup_id, REPLICASET)
    fragment = work / "fragment.json"
    _write_json(fragment, _make_fragment(instance_uuid=master))
    plan = work / "plan.json"
    _write_json(
        plan,
        _make_plan(
            replicasets={
                REPLICASET: {
                    "master_instance_uuid": master,
                    "master_instance_name": "router-001",
                },
            },
        ),
    )

    return backup_upload(
        tt,
        storage_uri,
        archives=archive,
        fragments=str(fragment),
        plan=str(plan),
        backup_id=backup_id,
    )


def test_upload_creates_a_storage_that_is_not_there_yet(tt, tmp_path):
    """The first backup of a new storage is not "the storage is unreadable".

    upload reads the chain head before writing anything, and a storage that
    does not exist yet holds no head -- while for every reading command the
    same answer is an error, since a mistyped path must not read as empty.
    """
    storage_dir = tmp_path / "not-created-yet"
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = backup_upload(
        tt,
        f"file://{storage_dir}",
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc == 0, f"the first backup has to create its storage:\n{out}"
    assert (storage_dir / "manifests" / f"{BACKUP_ID}.json").is_file()

    # The same storage read by a command that only reads: still an error,
    # because there is nothing there to read.
    rc, out = backup_verify(tt, f"file://{tmp_path / 'never-created'}")
    assert rc != 0
    assert "does not exist" in out


def test_upload_refuses_an_increment_the_storage_moved_under(tt, tmp_path):
    """The plan was made against a backup that is no longer the chain head.

    Another upload landed in between, so this increment continues something the
    chain no longer ends with. Stored anyway, it would either orphan itself or
    silently reorder the chain -- neither is recoverable afterwards.
    """
    storage = FileStorage(str(tmp_path / "backups"))

    rc, out = _upload_full(tt, storage.uri, tmp_path, "20260325T120000Z")
    assert rc == 0, out
    rc, out = _upload_full(tt, storage.uri, tmp_path, "20260326T120000Z", name="second")
    assert rc == 0, out

    # Planned against the first backup, uploaded after the second landed.
    plan = _make_plan(
        mode="incremental",
        previous_backup_id="20260325T120000Z",
        base_full_backup_id="20260325T120000Z",
    )
    fragment = _make_fragment(backup_type="incremental", vclock_end=200, vclock_begin={"1": 100})
    archive_path, fragment_path, plan_path = _prepare_inputs(
        tmp_path,
        plan=plan,
        fragment=fragment,
        backup_id="20260327T120000Z",
    )

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id="20260327T120000Z",
    )
    assert rc != 0
    assert "would not continue what it was planned against" in out
    assert "manifests/20260327T120000Z.json" not in storage.keys()


def test_upload_records_why_a_full_backup_was_forced(tt, tmp_path):
    """A full backup on a changed cluster says why it is not an increment.

    The orchestrator cannot tell tt why it asked for a full backup, and months
    later nobody can tell a scheduled one from a forced one -- unless the
    manifest says so.
    """
    storage = FileStorage(str(tmp_path / "backups"))

    rc, out = _upload_full(tt, storage.uri, tmp_path, "20260325T120000Z")
    assert rc == 0, out

    # Same replicaset, another master: an increment is impossible.
    rc, out = _upload_full(
        tt,
        storage.uri,
        tmp_path,
        "20260326T120000Z",
        name="promoted",
        master=INSTANCE_UUID_B,
    )
    assert rc == 0, out
    assert "full backup on top of chain" in out, f"the run has to say what it recorded:\n{out}"
    assert INSTANCE_UUID_B in out

    manifest = json.loads(storage.read("manifests/20260326T120000Z.json"))
    assert [w["code"] for w in manifest["warnings"]] == ["promoted_to_full"]
    assert manifest["warnings"][0]["details"]["reason"] == "master_changed"
    # The backup is complete: a forced full backup holds every byte a planned
    # one would, so the warning must not degrade the status.
    assert manifest["status"] == "OK"


def test_upload_of_an_unchanged_cluster_records_no_promotion(tt, tmp_path):
    """A scheduled full backup on top of a chain is not a promotion."""
    storage = FileStorage(str(tmp_path / "backups"))

    rc, out = _upload_full(tt, storage.uri, tmp_path, "20260325T120000Z")
    assert rc == 0, out
    rc, out = _upload_full(tt, storage.uri, tmp_path, "20260326T120000Z", name="scheduled")
    assert rc == 0, out

    manifest = json.loads(storage.read("manifests/20260326T120000Z.json"))
    assert manifest["warnings"] == []
    assert manifest["status"] == "OK"


def test_upload_refuses_an_archive_that_changed_on_the_way(tt, tmp_path):
    """The fragment's checksum was computed on the node, before the copy here.

    An archive truncated or corrupted by the transfer is otherwise stored and
    published as healthy, and found out when it is needed.
    """
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)
    storage_dir = tmp_path / "backups"

    # What the scp got wrong, done by hand.
    with open(archive_path, "wb") as damaged:
        damaged.write(ARCHIVE_CONTENT[:-3])

    rc, out = backup_upload(
        tt,
        f"file://{storage_dir}",
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert "the archive changed after it was packed" in out
    assert not storage_dir.exists(), "a damaged archive must not be stored"
    assert os.path.exists(archive_path), "the local copy is all there is left"


def test_upload_computes_a_checksum_the_fragment_lacks(tt, tmp_path):
    """A fragment without a checksum still yields a manifest describing the archive.

    Nothing is verified for such a shard -- the run says so rather than
    pretending the archive was checked.
    """
    fragment = _make_fragment()
    fragment.pop("checksum_sha256")
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path, fragment=fragment)
    storage_dir = tmp_path / "backups"

    rc, out = backup_upload(
        tt,
        f"file://{storage_dir}",
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc == 0, out
    assert "carries no checksum_sha256" in out

    manifest = json.loads((storage_dir / "manifests" / f"{BACKUP_ID}.json").read_text())
    stored = manifest["shards"][REPLICASET]["instance"]["artifact"]["checksum_sha256"]
    assert stored == hashlib.sha256(ARCHIVE_CONTENT).hexdigest()


@pytest.mark.parametrize(
    "plan_patch,expected",
    [
        pytest.param(
            {"format_version": 99},
            "format_version 99, this tt understands 1",
            id="unknown-version",
        ),
        pytest.param(
            {"format_version": None},
            "has no format_version",
            id="no-version",
        ),
        pytest.param(
            {"format_version": "1"},
            "format_version must be a number, got the string",
            id="version-as-a-string",
        ),
        pytest.param(
            {"format_version": 1.5},
            "format_version must be a whole number",
            id="fractional-version",
        ),
    ],
)
def test_upload_refuses_a_plan_it_cannot_read(tt, tmp_path, plan_patch, expected):
    """A plan of another format is refused, not read for the fields tt knows.

    The plan crosses hosts and possibly a tt upgrade between `backup plan` and
    `backup upload`, so taking the recognised fields out of an unknown version
    would silently drop whatever that version added.
    """
    plan = _make_plan()
    if plan_patch["format_version"] is None:
        plan.pop("format_version")
    else:
        plan.update(plan_patch)

    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path, plan=plan)
    storage_dir = tmp_path / "backups"

    rc, out = backup_upload(
        tt,
        f"file://{storage_dir}",
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert expected in out
    assert not storage_dir.exists(), "a refused plan must leave the storage untouched"
    assert os.path.exists(archive_path), "the local archive is the only copy left"


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


def test_upload_fragment_missing_replicaset_warns(tt, tmp_path):
    """A replicaset the plan expects and no fragment covers is a shard that
    produced nothing, not a run that has to die: the backup of every other
    replicaset is still stored, and the manifest records the missing one as
    unreachable so a restore knows the point is incomplete."""
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

    storage = FileStorage(str(tmp_path / "backups"))

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=str(fragment_path),
        plan=str(plan_path),
        backup_id=BACKUP_ID,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"
    assert "shard_unreachable" in out
    assert "status degraded" in out.lower()

    manifest = json.loads(storage.read(f"manifests/{BACKUP_ID}.json"))
    assert manifest["status"] == "degraded"
    assert manifest["shards"][REPLICASET_B]["error"] == "shard unreachable"
    assert "artifact" in manifest["shards"][REPLICASET]["instance"]
    assert manifest["warnings"] == [
        {
            "code": "shard_unreachable",
            "message": "shard unreachable",
            "details": {"replicaset_uuid": REPLICASET_B},
        },
    ]

    # The topology entry of a shard that reported nothing comes from the plan,
    # so a restore can still match the replicaset to the cluster by name.
    assert manifest["topology"]["replicasets"][REPLICASET_B] == [
        {"instance_uuid": INSTANCE_UUID_B, "instance_name": "router-002", "hostname": ""},
    ]


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


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_same_backup_id_overwrites(tt, tmp_path, storage):
    """A second upload under an already used backup id is refused.

    Ids order the chain, so one that does not sort above the newest stored
    backup would be read as older than the backup it was taken after. Reusing
    an id is the same input as a host whose clock went backwards, and both used
    to overwrite the stored backup silently."""
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
    )
    assert rc == 0, f"first upload failed:\n{out}"

    manifest_key = f"manifests/{BACKUP_ID}.json"
    archive_key = f"data/{BACKUP_ID}-{REPLICASET}.tar.zst"
    first_manifest = json.loads(storage.read(manifest_key))
    assert first_manifest["shards"][REPLICASET]["instance"]["vclock_end"] == {"1": 100}

    replacement = b"replacement payload"
    work = tmp_path / "work-again"
    work.mkdir()
    plan_again = work / "plan.json"
    _write_json(plan_again, _make_plan())
    fragment_again = work / "fragment.json"
    _write_json(fragment_again, _make_fragment(vclock_end=999, content=replacement))
    archive_again = _write_archive(str(work), BACKUP_ID, REPLICASET, replacement)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_again,
        fragments=str(fragment_again),
        plan=str(plan_again),
        backup_id=BACKUP_ID,
    )
    assert rc != 0, f"reusing a backup id must be refused:\n{out}"
    assert "does not sort above" in out

    # The stored backup is the first one, untouched: nothing was overwritten
    # and nothing was half-written either.
    keys = storage.keys()
    assert keys.count(manifest_key) == 1
    assert keys.count(archive_key) == 1
    assert storage.read(archive_key) == ARCHIVE_CONTENT

    assert json.loads(storage.read(manifest_key)) == first_manifest


def test_upload_timeout_expires(tt, tmp_path):
    archive_path, fragment_path, plan_path = _prepare_inputs(tmp_path)
    root = tmp_path / "backups"
    root.mkdir()

    rc, out = backup_upload(
        tt,
        f"file://{root}",
        archives=archive_path,
        fragments=fragment_path,
        plan=plan_path,
        backup_id=BACKUP_ID,
        timeout="1ns",
    )

    # A run that could not finish within the timeout must fail, and the
    # rollback leaves no half-uploaded backup behind.
    assert rc != 0
    assert "context deadline exceeded" in out.lower()
    assert FileStorage(str(root)).keys() == []


def test_upload_fragment_uuid_not_matching_archive_is_rejected(tt, tmp_path):
    """An archive renamed to another replicaset's uuid used to be uploaded with
    exit 0: the fragment found no archive of its own, so the shard was stored
    with an empty artifact path and status OK, and the local archive was removed
    right after. A backup that reports healthy, cannot be restored, and whose
    only good copy is already deleted."""
    work = tmp_path / "work"
    work.mkdir()

    # Plan and fragment agree on RS-B; only the archive's file name says RS-A.
    plan_path = work / "plan.json"
    _write_json(
        plan_path,
        _make_plan(
            replicasets={
                REPLICASET_B: {
                    "master_instance_uuid": INSTANCE_UUID_B,
                    "master_instance_name": "router-002",
                },
            },
        ),
    )

    fragment_path = work / "fragment.json"
    _write_json(
        fragment_path,
        _make_fragment(
            replicaset=REPLICASET_B,
            instance_uuid=INSTANCE_UUID_B,
            instance_name="router-002",
            content=ARCHIVE_CONTENT_B,
        ),
    )

    archive_path = _write_archive(str(work), BACKUP_ID, REPLICASET)
    storage = FileStorage(str(tmp_path / "backups"))

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive_path,
        fragments=str(fragment_path),
        plan=str(plan_path),
        backup_id=BACKUP_ID,
    )

    assert rc != 0
    # The checksum pass sees it first: it cannot verify an archive that no
    # fragment describes, and names the archive's own replicaset.
    assert REPLICASET in out
    assert storage.keys() == []
    assert os.path.isfile(archive_path), "the only copy of the data was removed"


def test_upload_duplicate_replicaset_across_fragments_is_rejected(tt, tmp_path):
    """Two fragments claiming one replicaset used to overwrite each other in the
    manifest -- last write won. The run reported "2 shards", the manifest held
    one, and the archive of the shard that lost is left in storage with no
    manifest naming it: a shard's backup gone with no warning and no failure.

    The plan names only the duplicated replicaset, so coverage against the plan
    passes and the run reaches the aggregation this test is about."""
    work = tmp_path / "work"
    work.mkdir()

    plan_path = work / "plan.json"
    _write_json(plan_path, _make_plan())

    fragments = []
    for index, instance in enumerate((INSTANCE_UUID, INSTANCE_UUID_B)):
        path = work / f"fragment_{index}.json"
        _write_json(
            path,
            _make_fragment(
                replicaset=REPLICASET,
                instance_uuid=instance,
                instance_name=f"router-00{index + 1}",
            ),
        )
        fragments.append(str(path))

    archive_a = _write_archive(str(work), BACKUP_ID, REPLICASET, ARCHIVE_CONTENT)
    archive_b = _write_archive(str(work), BACKUP_ID, REPLICASET_B, ARCHIVE_CONTENT_B)
    storage = FileStorage(str(tmp_path / "backups"))

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=f"{archive_a},{archive_b}",
        fragments=",".join(fragments),
        plan=str(plan_path),
        backup_id=BACKUP_ID,
    )

    assert rc != 0
    # The duplicate displaces the other replicaset's fragment, so the checksum
    # pass reaches an archive nothing describes and stops there -- before the
    # aggregation that used to be the one catching this.
    assert "no fragment describes archive" in out
    assert REPLICASET_B in out
    assert storage.keys() == []
    assert os.path.isfile(archive_a) and os.path.isfile(archive_b)


def test_upload_rollback_on_real_fs_backend(tt, tmp_path):
    """Archives are uploaded before the manifest, so a failed manifest write must
    take the archives back out: what is left otherwise is an archive set no
    manifest names, which every other command reads as an upload still running.

    Pinned against a mock storage only until now -- a real backend is what says
    whether the rollback survives contact with a filesystem."""
    archives, fragments, plan_path = _prepare_two_shard_inputs(tmp_path)

    root = tmp_path / "backups"
    root.mkdir()
    # A directory where the manifest file has to go: the storage lists as empty
    # and the archives upload, but the manifest write cannot land.
    (root / "manifests" / f"{BACKUP_ID}.json").mkdir(parents=True)
    storage = FileStorage(str(root))

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=",".join(archives),
        fragments=",".join(fragments),
        plan=plan_path,
        backup_id=BACKUP_ID,
    )

    assert rc != 0
    assert "upload manifest" in out
    # Nothing but the blocking directory is left: both archives were rolled
    # back, and a directory holds no objects to list.
    assert storage.keys() == []
    assert all(os.path.isfile(archive) for archive in archives)


def test_upload_more_fragments_than_archives_warns(tt, tmp_path):
    """The two lists are matched by replicaset, not by position: a fragment
    whose archive never made it to the manager host is a partial shard, and the
    archive that did arrive is still stored."""
    archives, fragments, plan_path = _prepare_two_shard_inputs(tmp_path)
    storage = FileStorage(str(tmp_path / "backups"))

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archives[0],
        fragments=",".join(fragments),
        plan=plan_path,
        backup_id=BACKUP_ID,
    )

    assert rc == 0, f"tt backup upload failed:\n{out}"
    assert "shard_partial" in out

    manifest = json.loads(storage.read(f"manifests/{BACKUP_ID}.json"))
    assert manifest["status"] == "degraded"
    assert manifest["shards"][REPLICASET]["instance"]["artifact"]["path"] == (
        f"data/{BACKUP_ID}-{REPLICASET}.tar.zst"
    )
    assert "expected an archive named" in manifest["shards"][REPLICASET_B]["error"]
    assert manifest["warnings"][0]["code"] == "shard_partial"
    assert manifest["warnings"][0]["details"]["replicaset_uuid"] == REPLICASET_B


@pytest.mark.parametrize(
    "trailing_comma",
    [
        pytest.param(False, id="two-archives-one-fragment"),
        pytest.param(True, id="trailing-comma"),
    ],
)
def test_upload_archive_nothing_describes_is_rejected(tt, tmp_path, trailing_comma):
    """The other direction is not a partial shard but an input nothing accounts
    for: an archive no fragment describes cannot have its checksum verified and
    would be stored as an object the manifest never refers to. A trailing comma
    in a generated command line is the same kind of mistake -- an entry that
    names no file at all."""
    archives, fragments, plan_path = _prepare_two_shard_inputs(tmp_path)
    storage = FileStorage(str(tmp_path / "backups"))

    archives_flag = archives[0] + "," if trailing_comma else ",".join(archives)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archives_flag,
        fragments=fragments[0],
        plan=plan_path,
        backup_id=BACKUP_ID,
    )

    assert rc != 0
    if trailing_comma:
        assert "stat archive" in out.lower()
    else:
        assert "no fragment describes archive" in out
        assert REPLICASET_B in out
    assert storage.keys() == []
