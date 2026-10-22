"""The full form of --backup-storage: @<path>.yaml instead of a URI.

The URI forms are covered by every other module in this directory. These cover
the config file itself, end to end through the cheapest command that opens a
storage: that the file reaches a working storage, that a ${VAR} in it is read
from the environment rather than becoming an empty credential, and that a
backend tt cannot write to is refused by name instead of being ignored.
"""

import json
import os

import pytest
from backup_helpers import backup_last, exec_split
from storage_helpers import FileStorage, write_backup

BACKUP_ID = "2026-01-01-full"
# Sorts after every backup the tests write, so it is the one a command that
# ignored the configured prefix would report.
DECOY_ID = "2026-12-31-decoy"


def write_config(tmp_path, body):
    """Write a storage config file, returning the --backup-storage value."""
    path = tmp_path / "storage.yaml"
    path.write_text(body, encoding="utf-8")
    return f"@{path}"


def test_file_config(tt, tmp_path):
    """A config file naming a local filesystem storage drives a real command."""
    root = tmp_path / "backups"
    root.mkdir()
    write_backup(FileStorage(str(root), "mycluster"), BACKUP_ID)
    write_backup(FileStorage(str(root)), DECOY_ID)

    config = write_config(tmp_path, f"type: fs\nroot: {root}\nprefix: mycluster\n")

    rc, out = backup_last(tt, config, fmt="json")

    assert rc == 0, out
    # The prefix decides which of the two storages under root is read.
    assert json.loads(out)["backup_id"] == BACKUP_ID


def test_file_config_env(tt, tmp_path):
    """${VAR} is substituted before the fields are read, in every value."""
    root = tmp_path / "backups"
    root.mkdir()
    write_backup(FileStorage(str(root), "mycluster"), BACKUP_ID)

    config = write_config(tmp_path, "type: fs\nroot: ${TT_TEST_ROOT}\nprefix: ${TT_TEST_PREFIX}\n")

    rc, out, err = exec_split(
        tt,
        "backup",
        "last",
        "--backup-storage",
        config,
        "--format",
        "json",
        env=dict(os.environ, TT_TEST_ROOT=str(root), TT_TEST_PREFIX="mycluster"),
    )

    assert rc == 0, err
    assert json.loads(out)["backup_id"] == BACKUP_ID


@pytest.mark.docker
@pytest.mark.parametrize("storage", [pytest.param(("s3", ""), id="s3")], indirect=True)
def test_s3_config_env(tt, tmp_path, garage, storage):
    """The credential the storage authenticates with comes from the environment.

    This is what the form exists for: the file names an S3 endpoint a URI cannot
    express, and holds no secret, so it can live next to the cluster config.
    """
    write_backup(storage, BACKUP_ID)

    config = write_config(
        tmp_path,
        f"type: s3\nendpoint: http://{garage.endpoint}\n"
        f"region: {garage.region}\nbucket: {garage.bucket}\n"
        "access_key_id: ${TT_TEST_S3_KEY}\nsecret_access_key: ${TT_TEST_S3_SECRET}\n",
    )

    rc, out, err = exec_split(
        tt,
        "backup",
        "last",
        "--backup-storage",
        config,
        "--format",
        "json",
        env=dict(
            os.environ,
            TT_TEST_S3_KEY=garage.access_key,
            TT_TEST_S3_SECRET=garage.secret_key,
        ),
    )

    assert rc == 0, err
    assert json.loads(out)["backup_id"] == BACKUP_ID
    assert garage.secret_key not in (tmp_path / "storage.yaml").read_text(encoding="utf-8")
    assert garage.secret_key not in out + err


def test_config_env_unset(tt, tmp_path):
    """An unset variable stops the command and names itself.

    An empty credential is not refused as missing by an S3 storage: it comes
    back as an opaque 403 after the backup has already been taken.
    """
    config = write_config(
        tmp_path,
        "type: s3\nendpoint: https://s3.example.com\nbucket: payments\n"
        "access_key_id: key\nsecret_access_key: ${TT_TEST_ABSENT_SECRET}\n",
    )

    rc, out = backup_last(tt, config, fmt="json")

    assert rc != 0
    assert "TT_TEST_ABSENT_SECRET" in out


def test_config_ftp(tt, tmp_path):
    """The spec defines an ftp backend, tt does not implement it.

    An ignored 'type: ftp' would send the backups to whatever the rest of the
    config happens to describe, so it has to be refused by name.
    """
    config = write_config(tmp_path, "type: ftp\nhost: backups.example.com\nroot: /payments\n")

    rc, out = backup_last(tt, config, fmt="json")

    assert rc != 0
    assert "ftp" in out.lower()
    assert "not supported" in out.lower()


def test_config_unknown_field(tt, tmp_path):
    """A field the backend does not declare is an error, not a comment.

    The backup here sits in the root the config names, so a 'Prefix' that was
    read as a URI query parameter and then ignored would exit 0 and report it.
    """
    root = tmp_path / "backups"
    root.mkdir()
    write_backup(FileStorage(str(root)), BACKUP_ID)

    config = write_config(tmp_path, f"type: fs\nroot: {root}\nPrefix: mycluster\n")

    rc, out = backup_last(tt, config, fmt="json")

    assert rc != 0, out
    assert "Prefix" in out
