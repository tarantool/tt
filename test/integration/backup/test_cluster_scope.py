"""--cluster-name / --environment: one storage, several clusters.

The pair names a subtree of the storage, <cluster_name>/<environment>/, and
every command that reads or writes a storage takes it. What these cover is that
the writer and the readers agree on where that subtree is: a backup uploaded
with the flags is found by last, verify, gc and plan given the same flags, and
is invisible -- without an error, which is the trap -- to a run given none.
"""

import hashlib
import json

import pytest
from backup_helpers import backup_gc, backup_last, backup_upload, backup_verify, restore_plan
from storage_helpers import (
    PREFIXED_STORAGE_BACKENDS,
    STORAGE_BACKENDS,
    FileStorage,
    S3Storage,
)

REPLICASET = "11111111-1111-1111-1111-111111111111"
INSTANCE_UUID = "aaaaaaaa-0000-0000-0000-000000000001"
BACKUP_ID = "20260326T120000Z"
OLDER_BACKUP_ID = "20260325T120000Z"
ARCHIVE_CONTENT = b"archive payload abc"

RECOVERY_POINT = "rp-1"
# The only recovery point of the only backup, so it is both ends of the
# coverage: the plan asks for exactly it.
POINT_TIMESTAMP = 1774526340
TARGET_TIME = "2026-03-26T11:59:00Z"

CLUSTER = "payments"
ENVIRONMENT = "production"


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


def _upload_inputs(tmp_path, backup_id=BACKUP_ID, plan_scope=None):
    """Write the archive, fragment and plan of a one-shard full backup."""
    work = tmp_path / backup_id
    work.mkdir()

    archive = work / f"{backup_id}-{REPLICASET}.tar.zst"
    archive.write_bytes(ARCHIVE_CONTENT)

    fragment = work / "fragment.json"
    fragment.write_text(
        json.dumps(
            {
                "replicaset_uuid": REPLICASET,
                "instance_uuid": INSTANCE_UUID,
                "instance_name": "router-001",
                "hostname": "localhost",
                "type": "full",
                "vclock_begin": None,
                "vclock_end": {"1": 100},
                "files": ["00000000000000000100.xlog"],
                "checksum_sha256": hashlib.sha256(ARCHIVE_CONTENT).hexdigest(),
                "recovery_points": [
                    {
                        "label": RECOVERY_POINT,
                        "replica_id": 1,
                        "lsn": 100,
                        "timestamp": POINT_TIMESTAMP,
                    },
                ],
            },
        ),
    )

    plan = work / "plan.json"
    plan_body = {
        "format_version": 1,
        "mode": "full",
    }
    if plan_scope is not None:
        plan_body["cluster_name"], plan_body["environment"] = plan_scope
    plan.write_text(
        json.dumps(
            {
                **plan_body,
                "replicasets": {
                    REPLICASET: {
                        "master_instance_uuid": INSTANCE_UUID,
                        "master_instance_name": "router-001",
                    },
                },
            },
        ),
    )

    return str(archive), str(fragment), str(plan)


def _upload_scoped(
    tt,
    tmp_path,
    storage,
    backup_id=BACKUP_ID,
    cluster_name=CLUSTER,
    environment=ENVIRONMENT,
    plan_scope=None,
):
    archive, fragment, plan = _upload_inputs(tmp_path, backup_id, plan_scope=plan_scope)

    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive,
        fragments=fragment,
        plan=plan,
        backup_id=backup_id,
        cluster_name=cluster_name,
        environment=environment,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    return out


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_readers_find_a_scoped_backup(tt, tmp_path, storage):
    """last, verify and gc read the subtree upload wrote when given the same pair."""
    _upload_scoped(tt, tmp_path, storage)

    assert f"{CLUSTER}/{ENVIRONMENT}/manifests/{BACKUP_ID}.json" in storage.keys()

    rc, out = backup_last(
        tt,
        storage.uri,
        fmt="json",
        cluster_name=CLUSTER,
        environment=ENVIRONMENT,
    )
    assert rc == 0, out
    assert json.loads(out)["backup_id"] == BACKUP_ID

    rc, out = backup_verify(
        tt,
        storage.uri,
        fmt="json",
        cluster_name=CLUSTER,
        environment=ENVIRONMENT,
    )
    assert rc == 0, f"tt backup verify reported problems:\n{out}"

    report = json.loads(out)
    assert report["manifests_checked"] == 1
    assert report["archives_checked"] == 1
    assert report["issues"] == []

    # gc sees the chains in the subtree: a second full backup and --keep-full 1
    # leave the older one to delete. Reading the storage root instead would
    # find no chain at all and plan nothing, which is what the next test pins.
    _upload_scoped(tt, tmp_path, storage, backup_id=OLDER_BACKUP_ID)

    rc, out = backup_gc(
        tt,
        storage.uri,
        keep_full=1,
        dry_run=True,
        fmt="json",
        cluster_name=CLUSTER,
        environment=ENVIRONMENT,
    )
    assert rc == 0, out

    planned = json.loads(out)["plan"]
    assert [entry["backup_id"] for entry in planned["backups"]] == [OLDER_BACKUP_ID]


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_readers_without_the_scope_see_nothing(tt, tmp_path, storage):
    """Reading the storage root reports an empty storage, not the scoped backup.

    This is the shape that hurts: verify and gc succeed, so a cron watching
    exit codes reports a healthy storage it has never actually looked at.
    """
    _upload_scoped(tt, tmp_path, storage)

    rc, out = backup_last(tt, storage.uri, fmt="json")
    assert rc == 1, f"an empty storage root must not report a backup:\n{out}"
    assert "no backups found" in out.lower()

    rc, out = backup_verify(tt, storage.uri, fmt="json")
    assert rc == 0, out
    assert json.loads(out)["manifests_checked"] == 0

    rc, out = backup_gc(tt, storage.uri, keep_full=1, dry_run=True, fmt="json")
    assert rc == 0, out

    planned = json.loads(out)["plan"]
    assert planned["backups"] == []
    assert planned["orphans"] == []


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_two_clusters_share_one_storage(tt, tmp_path, storage):
    """A backup of one cluster is not visible from the other's subtree."""
    _upload_scoped(tt, tmp_path, storage)

    rc, out = backup_last(
        tt,
        storage.uri,
        fmt="json",
        cluster_name="orders",
        environment=ENVIRONMENT,
    )
    assert rc == 1, f"another cluster's subtree must be empty:\n{out}"
    assert "no backups found" in out.lower()

    rc, out = backup_last(
        tt,
        storage.uri,
        fmt="json",
        cluster_name=CLUSTER,
        environment="staging",
    )
    assert rc == 1, f"another environment's subtree must be empty:\n{out}"
    assert "no backups found" in out.lower()


def test_environment_without_a_cluster_name_is_refused(tt, tmp_path):
    """The pair names one path component each; honouring half of it is worse."""
    root = tmp_path / "backups"
    root.mkdir()

    rc, out = backup_last(tt, f"file://{root}", environment=ENVIRONMENT)
    assert rc != 0
    assert "--environment" in out and "--cluster-name" in out


def test_a_cluster_name_that_is_not_one_component_is_refused(tt, tmp_path):
    """A separator in either flag would move the storage somewhere else."""
    root = tmp_path / "backups"
    root.mkdir()

    rc, out = backup_last(tt, f"file://{root}", cluster_name="../escape")
    assert rc != 0
    assert "path separator" in out


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_restore_plan_resolves_a_scoped_backup(tt, tmp_path, storage):
    """restore plan reads the same subtree, and reports nothing without it."""
    _upload_scoped(tt, tmp_path, storage)

    rc, out = restore_plan(
        tt,
        storage.uri,
        TARGET_TIME,
        tmp_path / "download",
        cluster_name=CLUSTER,
        environment=ENVIRONMENT,
    )
    assert rc == 0, f"tt restore plan did not resolve the point:\n{out}"

    plan = json.loads(out)
    assert plan["status"] == "ok"
    assert plan["recovery_point"]["label"] == RECOVERY_POINT

    rc, out = restore_plan(tt, storage.uri, TARGET_TIME, tmp_path / "download-unscoped")
    assert rc != 0, f"the storage root holds no backup to plan from:\n{out}"


@pytest.mark.parametrize("storage", PREFIXED_STORAGE_BACKENDS, indirect=True)
def test_the_scope_composes_with_the_uri_prefix(tt, tmp_path, storage):
    """The pair appends to the prefix the URI or the config file already carried."""
    _upload_scoped(tt, tmp_path, storage)

    # storage.keys() is relative to the URI prefix, so what it shows is what the
    # flags added on top of it.
    assert f"{CLUSTER}/{ENVIRONMENT}/manifests/{BACKUP_ID}.json" in storage.keys()

    rc, out = backup_last(
        tt,
        storage.uri,
        fmt="json",
        cluster_name=CLUSTER,
        environment=ENVIRONMENT,
    )
    assert rc == 0, out
    assert json.loads(out)["backup_id"] == BACKUP_ID

    # Without the pair the URI prefix alone is a different, empty storage.
    rc, out = backup_last(tt, storage.uri, fmt="json")
    assert rc == 1, out


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_takes_the_scope_from_the_plan(tt, tmp_path, storage):
    """The plan says where the backup belongs; upload needs no flags for it."""
    _upload_scoped(
        tt,
        tmp_path,
        storage,
        cluster_name=None,
        environment=None,
        plan_scope=(CLUSTER, ENVIRONMENT),
    )

    assert f"{CLUSTER}/{ENVIRONMENT}/manifests/{BACKUP_ID}.json" in storage.keys()

    rc, out = backup_last(
        tt,
        storage.uri,
        fmt="json",
        cluster_name=CLUSTER,
        environment=ENVIRONMENT,
    )
    assert rc == 0, out
    assert json.loads(out)["backup_id"] == BACKUP_ID


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_upload_flags_override_the_plan_scope(tt, tmp_path, storage):
    """A flag moves the backup elsewhere, and says that it did."""
    out = _upload_scoped(
        tt,
        tmp_path,
        storage,
        cluster_name=None,
        environment="staging",
        plan_scope=(CLUSTER, ENVIRONMENT),
    )

    assert f"{CLUSTER}/staging/manifests/{BACKUP_ID}.json" in storage.keys()
    assert f"{CLUSTER}/{ENVIRONMENT}/manifests/{BACKUP_ID}.json" not in storage.keys()
    assert "overrides the plan" in out


@pytest.mark.parametrize(
    "plan_scope,expected",
    [
        pytest.param(("../escape", ENVIRONMENT), "path separator", id="escaping-cluster"),
        pytest.param((CLUSTER, "a/b"), "path separator", id="separator-in-environment"),
        pytest.param(("   ", ENVIRONMENT), "cannot be blank", id="blank-cluster"),
        pytest.param((None, ENVIRONMENT), "needs a cluster name", id="environment-alone"),
    ],
)
def test_a_plan_cannot_name_a_scope_outside_the_storage(tt, tmp_path, plan_scope, expected):
    """The plan is input from another host, checked like any other input.

    Whoever wrote the plan is not the operator running upload, so the scope it
    carries goes through the same validation the flags do -- and the message
    says the plan is where the value came from, not a flag nobody passed.
    """
    storage_dir = tmp_path / "backups"
    cluster, environment = plan_scope
    archive, fragment, plan = _upload_inputs(
        tmp_path,
        plan_scope=(cluster or "", environment),
    )

    rc, out = backup_upload(
        tt,
        f"file://{storage_dir}",
        archives=archive,
        fragments=fragment,
        plan=plan,
        backup_id=BACKUP_ID,
    )
    assert rc != 0
    assert expected in out
    assert "from the plan" in out, f"the message must name the plan as the source:\n{out}"
    assert not storage_dir.exists(), "a refused scope must leave the storage untouched"
