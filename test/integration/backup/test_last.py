import io
import json
from pathlib import Path
from types import SimpleNamespace

import pytest
from backup_helpers import (
    backup_last,
    write_manifest_to_storage,
)

TESTDATA_DIR = Path(__file__).parent / "testdata"
FULL_MANIFEST = json.loads((TESTDATA_DIR / "manifest_full.json").read_text())
INCREMENTAL_MANIFEST = json.loads((TESTDATA_DIR / "manifest_incremental.json").read_text())

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


def test_last_parse_error_does_not_expose_password(tt):
    password = "secret"
    uri = f"s3+https://host/bucket?AccessKeyID=key&SecretAccessKey={password}%zz"

    rc, out = backup_last(tt, uri, fmt="json")
    assert rc != 0
    assert password not in out


def test_last_missing_backup_storage_flag(tt):
    rc, out = tt.exec("backup", "last")
    assert rc != 0
    assert "required" in out.lower()
