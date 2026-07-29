import shutil
import subprocess
import time
from types import SimpleNamespace

import pytest
from minio import Minio
from sharded_helpers import start_sharded_app
from storage_helpers import FileStorage, S3Storage

GARAGE_IMAGE = "dxflrs/garage:v2.3.0"
GARAGE_BUCKET = "tt-backup-test"
GARAGE_REGION = "garage"
GARAGE_ACCESS_KEY = "GKTTBACKUPTEST000000000000000000"
GARAGE_SECRET_KEY = "tt-backup-test-secret-key"

GARAGE_CONFIG = """metadata_dir = "/tmp/meta"
data_dir = "/tmp/data"
db_engine = "sqlite"
replication_factor = 1

rpc_bind_addr = "[::]:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = "0000000000000000000000000000000000000000000000000000000000000000"

[s3_api]
s3_region = "garage"
api_bind_addr = "[::]:3900"
root_domain = ".s3.garage.localhost"
"""


@pytest.fixture(scope="module")
def garage(tmp_path_factory):
    docker = shutil.which("docker")
    if docker is None:
        pytest.skip("docker is not installed")

    docker_info = subprocess.run(
        [docker, "info"],
        capture_output=True,
        text=True,
    )
    if docker_info.returncode != 0:
        pytest.skip(f"docker daemon is unavailable: {docker_info.stderr}")

    config_path = tmp_path_factory.mktemp("garage") / "garage.toml"
    config_path.write_text(GARAGE_CONFIG, encoding="utf-8")
    started = subprocess.run(
        [
            docker,
            "run",
            "--detach",
            "--rm",
            "--publish",
            "127.0.0.1::3900",
            "--env",
            f"GARAGE_DEFAULT_ACCESS_KEY={GARAGE_ACCESS_KEY}",
            "--env",
            f"GARAGE_DEFAULT_SECRET_KEY={GARAGE_SECRET_KEY}",
            "--env",
            f"GARAGE_DEFAULT_BUCKET={GARAGE_BUCKET}",
            "--volume",
            f"{config_path}:/etc/garage.toml:ro",
            GARAGE_IMAGE,
            "/garage",
            "server",
            "--single-node",
            "--default-bucket",
        ],
        capture_output=True,
        text=True,
    )
    assert started.returncode == 0, started.stderr
    container_id = started.stdout.strip()

    try:
        deadline = time.monotonic() + 60
        last_bucket_output = ""
        while time.monotonic() < deadline:
            bucket_info = subprocess.run(
                [docker, "exec", container_id, "/garage", "bucket", "info", GARAGE_BUCKET],
                capture_output=True,
                text=True,
            )
            if bucket_info.returncode == 0:
                break
            last_bucket_output = bucket_info.stdout + bucket_info.stderr
            time.sleep(1)
        else:
            logs = subprocess.run(
                [docker, "logs", container_id],
                capture_output=True,
                text=True,
            )
            pytest.fail(
                "Garage default bucket was not created:\n"
                f"{last_bucket_output}\ncontainer logs:\n{logs.stdout}{logs.stderr}",
            )

        port = subprocess.run(
            [docker, "port", container_id, "3900/tcp"],
            capture_output=True,
            check=True,
            text=True,
        ).stdout.strip()
        yield SimpleNamespace(
            endpoint=port,
            bucket=GARAGE_BUCKET,
            region=GARAGE_REGION,
            access_key=GARAGE_ACCESS_KEY,
            secret_key=GARAGE_SECRET_KEY,
            client=Minio(
                port,
                access_key=GARAGE_ACCESS_KEY,
                secret_key=GARAGE_SECRET_KEY,
                region=GARAGE_REGION,
                secure=False,
            ),
        )
    finally:
        subprocess.run(
            [docker, "rm", "--force", container_id],
            capture_output=True,
            text=True,
        )


@pytest.fixture
def storage(request, tmp_path):
    """A backup storage on the backend named by the test's parameter.

    Parametrized with (backend, prefix) pairs from storage_helpers, so one test
    body runs against the local filesystem and against S3.
    """
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


@pytest.fixture(scope="module")
def sharded_app(tt_cmd, tmp_path_factory):
    """A running two-shard vshard cluster with the recovery point manager role.

    Module-scoped: building the app downloads and installs the pinned vshard
    rock, which is too slow to repeat per test.
    """
    # Short name on purpose: it is a prefix of every instance's console socket
    # path, which must fit into sun_path (see start_sharded_app).
    env_dir = tmp_path_factory.mktemp("shard")
    app = start_sharded_app(tt_cmd, env_dir)
    try:
        yield app
    finally:
        app.tt("stop", "-y", assert_rc=False)
