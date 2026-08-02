import io
import json
import re
from pathlib import Path
from types import SimpleNamespace

import pytest
from backup_helpers import (
    backup_last,
    write_manifest_to_storage,
)
from storage_helpers import (
    REPLICASET_B,
    FileStorage,
    add_failed_shard,
    write_backup,
    write_manifest,
)

TESTDATA_DIR = Path(__file__).parent / "testdata"
FULL_MANIFEST = json.loads((TESTDATA_DIR / "manifest_full.json").read_text())
INCREMENTAL_MANIFEST = json.loads((TESTDATA_DIR / "manifest_incremental.json").read_text())

# Sorts after every manifest the fixtures write, so it is always the one the
# command picks as the latest.
PROBE_ID = "2026-02-01-probe"
PROBE_KEY = f"manifests/{PROBE_ID}.json"

STORAGE_BACKENDS = [
    pytest.param(("file", ""), id="file"),
    pytest.param(("s3", ""), marks=pytest.mark.docker, id="s3"),
]
PREFIXED_STORAGE_BACKENDS = [
    pytest.param(("file", "mycluster"), id="file"),
    pytest.param(("s3", "mycluster"), marks=pytest.mark.docker, id="s3"),
]


@pytest.fixture
def backup_storage(request, tmp_path):
    backend, prefix = request.param
    if backend == "file":
        root = tmp_path / "backups"
        root.mkdir()
        uri = f"file://{root}"
        if prefix:
            uri += f"?Prefix={prefix}"
        yield SimpleNamespace(
            backend=backend,
            uri=uri,
            path=root / prefix if prefix else root,
        )
        return

    garage = request.getfixturevalue("garage")
    prefix_path = f"/{prefix}" if prefix else ""
    uri = (
        f"s3+http://{garage.endpoint}/{garage.bucket}{prefix_path}"
        f"?Region={garage.region}&AccessKeyID={garage.access_key}"
        f"&SecretAccessKey={garage.secret_key}"
    )
    try:
        yield SimpleNamespace(
            backend=backend,
            uri=uri,
            garage=garage,
            prefix=prefix,
        )
    finally:
        for obj in garage.client.list_objects(garage.bucket, recursive=True):
            garage.client.remove_object(garage.bucket, obj.object_name)


def _write_manifest(storage, manifest):
    if storage.backend == "file":
        write_manifest_to_storage(str(storage.path), manifest)
        return

    data = json.dumps(manifest).encode()
    prefix = f"{storage.prefix}/" if storage.prefix else ""
    storage.garage.client.put_object(
        storage.garage.bucket,
        f"{prefix}manifests/{manifest['backup_id']}.json",
        io.BytesIO(data),
        len(data),
    )


@pytest.mark.parametrize("backup_storage", STORAGE_BACKENDS, indirect=True)
def test_last_json_single_manifest(tt, backup_storage):
    _write_manifest(backup_storage, FULL_MANIFEST)

    rc, out = backup_last(tt, backup_storage.uri, fmt="json")
    assert rc == 0, f"tt backup last failed:\n{out}"

    parsed = json.loads(out.strip())
    assert parsed["backup_id"] == "2026-01-01-full"
    assert parsed["status"] == "OK"
    assert parsed["schema_version"] == 1

    shard = parsed["shards"]["11111111-1111-1111-1111-111111111111"]["instance"]
    assert shard["vclock_begin"] is None, shard
    assert shard["vclock_end"] == {"1": 100}, shard
    artifact = shard["artifact"]
    assert artifact["type"] == "full"
    assert artifact["compression"] == "zstd"
    assert any(name.endswith(".snap") for name in artifact["files"]), artifact
    assert artifact["recovery_points"][0]["label"] == "rp-1", artifact


@pytest.mark.parametrize("backup_storage", STORAGE_BACKENDS, indirect=True)
def test_last_returns_latest_manifest(tt, backup_storage):
    _write_manifest(backup_storage, FULL_MANIFEST)
    _write_manifest(backup_storage, INCREMENTAL_MANIFEST)

    rc, out = backup_last(tt, backup_storage.uri, fmt="json")
    assert rc == 0, f"tt backup last failed:\n{out}"

    parsed = json.loads(out.strip())
    assert parsed["backup_id"] == "2026-01-02-inc"
    assert parsed["previous_backup_id"] == "2026-01-01-full"
    assert parsed["base_full_backup_id"] == "2026-01-01-full"


@pytest.mark.parametrize("backup_storage", STORAGE_BACKENDS, indirect=True)
def test_last_does_not_load_older_manifests(tt, backup_storage):
    if backup_storage.backend == "file":
        manifests = backup_storage.path / "manifests"
        manifests.mkdir(parents=True)
        (manifests / "0000-broken.json").write_text("{", encoding="utf-8")
    else:
        data = b"{"
        backup_storage.garage.client.put_object(
            backup_storage.garage.bucket,
            "manifests/0000-broken.json",
            io.BytesIO(data),
            len(data),
        )
    _write_manifest(backup_storage, FULL_MANIFEST)

    rc, out = backup_last(tt, backup_storage.uri, fmt="json")
    assert rc == 0, f"tt backup last failed:\n{out}"
    assert json.loads(out)["backup_id"] == "2026-01-01-full"


@pytest.mark.parametrize("backup_storage", STORAGE_BACKENDS, indirect=True)
def test_last_empty_storage(tt, backup_storage):
    rc, out = backup_last(tt, backup_storage.uri, fmt="json")
    assert rc != 0
    assert "no backups found" in out.lower()


def test_last_table_format(tt, tmp_path):
    storage = tmp_path / "backups"
    write_manifest_to_storage(str(storage), FULL_MANIFEST)

    uri = f"file://{storage}"
    rc, out = backup_last(tt, uri, fmt="table")
    assert rc == 0, f"tt backup last failed:\n{out}"
    # Table format must not produce JSON.
    assert not out.lstrip().startswith("{")
    # Key manifest fields must be present in the output.
    assert "2026-01-01-full" in out
    assert "OK" in out
    assert "Shards" in out


def test_last_default_format_is_table(tt, tmp_path):
    storage = tmp_path / "backups"
    write_manifest_to_storage(str(storage), FULL_MANIFEST)

    uri = f"file://{storage}"
    rc, out = backup_last(tt, uri)
    assert rc == 0, f"tt backup last failed:\n{out}"
    assert "2026-01-01-full" in out


def test_last_invalid_format(tt, tmp_path):
    storage = tmp_path / "backups"
    write_manifest_to_storage(str(storage), FULL_MANIFEST)

    uri = f"file://{storage}"
    rc, out = backup_last(tt, uri, fmt="xml")
    assert rc != 0
    assert "unsupported format" in out.lower()


@pytest.mark.parametrize("backup_storage", PREFIXED_STORAGE_BACKENDS, indirect=True)
def test_last_with_prefix(tt, backup_storage):
    _write_manifest(backup_storage, FULL_MANIFEST)

    rc, out = backup_last(tt, backup_storage.uri, fmt="json")
    assert rc == 0, f"tt backup last failed:\n{out}"

    parsed = json.loads(out.strip())
    assert parsed["backup_id"] == "2026-01-01-full"


@pytest.mark.docker
def test_last_s3_auth_error_does_not_expose_secret(tt, garage):
    secret = "garage-invalid-secret"
    uri = (
        f"s3+http://{garage.endpoint}/{garage.bucket}"
        f"?Region={garage.region}&AccessKeyID={garage.access_key}"
        f"&SecretAccessKey={secret}"
    )

    rc, out = backup_last(tt, uri, fmt="json")
    assert rc != 0
    assert secret not in out


def test_last_timeout_expires(tt, tmp_path):
    root = tmp_path / "backups"
    root.mkdir()
    write_manifest_to_storage(str(root), FULL_MANIFEST)

    rc, out = backup_last(tt, f"file://{root}", fmt="json", timeout="1ns")

    assert rc != 0
    assert "context deadline exceeded" in out.lower()


UNUSABLE_MANIFESTS = [
    pytest.param("null", id="null"),
    pytest.param("{}", id="empty-object"),
    pytest.param(
        json.dumps({**FULL_MANIFEST, "backup_id": PROBE_ID, "schema_version": 99}),
        id="future-schema",
    ),
]


# An orchestrator chains the next increment onto the backup_id it reads here, so
# a manifest nothing can be chained onto -- no id, an unknown schema -- has to be
# an error rather than a blank report. The error has to name the object: an
# operator cannot delete the bad manifest without knowing which one it is.
@pytest.mark.parametrize("payload", UNUSABLE_MANIFESTS)
def test_last_rejects_an_unusable_manifest(tt, tmp_path, payload):
    storage = tmp_path / "backups"
    write_manifest_to_storage(str(storage), FULL_MANIFEST)
    (storage / PROBE_KEY).write_text(payload, encoding="utf-8")

    rc, out = backup_last(tt, f"file://{storage}", fmt="json")

    assert rc != 0, out
    assert PROBE_KEY in out, out


# An operator note, a .DS_Store or a half-cleaned staging directory under
# manifests/ says nothing about the backups themselves. Sorting after the newest
# manifest must not take the command out.
def test_last_skips_non_manifest_objects(tt, tmp_path):
    storage = tmp_path / "backups"
    write_manifest_to_storage(str(storage), FULL_MANIFEST)
    manifests = storage / "manifests"
    (manifests / "zz-README.txt").write_text("restored from this one\n", encoding="utf-8")
    (manifests / "zz-staging").mkdir()
    (manifests / "zz-staging" / "x.bin").write_bytes(b"\x00\x01binary")

    rc, out = backup_last(tt, f"file://{storage}", fmt="json")

    assert rc == 0, out
    assert json.loads(out)["backup_id"] == "2026-01-01-full"


BROKEN_STORAGES = ["absent-root", "root-is-a-file", "manifests-is-a-file"]


def _broken_storage(tmp_path, shape):
    if shape == "absent-root":
        return tmp_path / "typo"
    if shape == "root-is-a-file":
        path = tmp_path / "regular"
        path.write_text("not a storage", encoding="utf-8")
        return path

    path = tmp_path / "shadowed"
    path.mkdir()
    (path / "manifests").write_text("not a directory", encoding="utf-8")
    return path


# A typo'd --backup-storage in a cron job must not read as "the daemon never
# ran". That is what tt backup plan --target=incremental answers with "use
# --target=full", so a wrong path silently proposes resetting the whole chain.
@pytest.mark.parametrize("shape", BROKEN_STORAGES)
def test_last_distinguishes_missing_storage_from_empty(tt, tmp_path, shape):
    genuinely_empty = tmp_path / "empty"
    genuinely_empty.mkdir()
    _, empty_out = backup_last(tt, f"file://{genuinely_empty}", fmt="json")

    rc, out = backup_last(tt, f"file://{_broken_storage(tmp_path, shape)}", fmt="json")

    assert rc != 0
    assert out.strip() != empty_out.strip(), (
        f"{shape}: reports exactly what a genuinely empty storage reports"
    )


# The table is what an operator reads to answer "did last night's backup land?".
# A degraded backup whose second replicaset produced nothing must not read as two
# shards backed up -- otherwise the missing replicaset surfaces at restore time.
def test_last_table_shows_failed_shards(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    manifest = add_failed_shard(storage, write_backup(storage, "2026-01-01-full"))
    manifest["warnings"] = [
        {"code": "shard_unreachable", "message": "storage-002-a is not reachable"},
        {"code": "storage_partial_upload", "message": "upload was interrupted"},
    ]
    write_manifest(storage, manifest)

    rc, out = backup_last(tt, storage.uri)

    assert rc == 0, out
    assert REPLICASET_B in out, out
    assert "shard_unreachable" in out and "storage_partial_upload" in out, out
    assert not re.search(r"Shards:\s+2\s*$", out, re.M), (
        "the table counts a replicaset that has no backup as if it were backed up"
    )


# 0 means "no limit" on verify and gc. The same flag on the same command group
# must not mean "expire immediately" here.
def test_last_timeout_zero_means_no_limit(tt, tmp_path):
    root = tmp_path / "backups"
    write_manifest_to_storage(str(root), FULL_MANIFEST)

    rc, out = backup_last(tt, f"file://{root}", fmt="json", timeout="0")

    assert rc == 0, out
    assert json.loads(out)["backup_id"] == "2026-01-01-full"
