import os
from pathlib import Path

import pytest
from backup_helpers import (
    app_instance,
    archive_path_from_output,
    backup_dir,
    finalize_backup,
    get_backup_info_app,
    inspect_backup_artifact,
    post_start_backup_app,
    start_backup,
)

from utils import get_tarantool_version, is_tarantool_ee, is_tarantool_less

STORAGE_1_A = "storage-001-a"

# The port must match ssl_app/config.yaml. The instance listens with TLS only,
# so the backup commands address it by <URI>: an <APP:INSTANCE> target would be
# resolved to the instance's own binary port and never negotiate TLS.
SSL_APP_URI = "localhost:3344"
SSL_APP_CREDENTIALS_URI = f"client:secret@{SSL_APP_URI}"

TT_BACKUP_SSL_APP = dict(
    app_path="ssl_app",
    # Short on purpose: the app name is a prefix of the console socket path,
    # which must fit into sun_path.
    app_name="ssl",
    instances=[STORAGE_1_A],
    running_targets=["ssl"],
    post_start=post_start_backup_app,
)

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = f"backup requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"

# The Enterprise check is a marker rather than a skip_if_tarantool_ce() call in
# the test body: the tt_app fixture starts the application before the body runs,
# and a Community instance refuses a listener with transport 'ssl'.
pytestmark = [
    pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason),
    pytest.mark.skipif(not is_tarantool_ee(), reason="iproto TLS requires Tarantool Enterprise"),
]


def ssl_args(tt_app, ca_file="ca.crt"):
    """The --ssl* flags pointing at the certificates the app was started with.

    ca_file names the file passed as --sslcafile, so a test can hand the
    command a certificate that did not sign the instance's one.
    """
    return [
        "--sslkeyfile",
        tt_app.path("localhost.key"),
        "--sslcertfile",
        tt_app.path("localhost.crt"),
        "--sslcafile",
        tt_app.path(ca_file),
    ]


@pytest.mark.tt_app(**TT_BACKUP_SSL_APP)
def test_ssl_start_and_finalize_with_uri_credentials(tt, tt_app, tmp_path):
    console_target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-ssl-uri"
    target_dir = Path(backup_dir(backup_id))

    rc, out = start_backup(
        tt,
        SSL_APP_CREDENTIALS_URI,
        backup_id,
        extra_args=ssl_args(tt_app),
    )
    assert rc == 0, f"tt backup start over TLS failed:\n{out}"

    try:
        archive_path = archive_path_from_output(out)
        inspect_backup_artifact(archive_path, tmp_path / "unpacked", backup_id)
        assert get_backup_info_app(tt, console_target) is not None
    finally:
        rc, out = finalize_backup(
            tt,
            SSL_APP_CREDENTIALS_URI,
            backup_id,
            extra_args=ssl_args(tt_app),
        )

    assert rc == 0, f"tt backup finalize over TLS failed:\n{out}"
    assert get_backup_info_app(tt, console_target) is None
    assert not target_dir.exists()


@pytest.mark.tt_app(**TT_BACKUP_SSL_APP)
def test_ssl_start_and_finalize_with_env_credentials(tt, tt_app, tmp_path):
    console_target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-ssl-env"
    target_dir = Path(backup_dir(backup_id))
    env = dict(os.environ, TT_CLI_USERNAME="client", TT_CLI_PASSWORD="secret")

    rc, out = start_backup(
        tt,
        SSL_APP_URI,
        backup_id,
        extra_args=ssl_args(tt_app),
        env=env,
    )
    assert rc == 0, f"tt backup start over TLS failed:\n{out}"

    try:
        archive_path = archive_path_from_output(out)
        inspect_backup_artifact(archive_path, tmp_path / "unpacked", backup_id)
        assert get_backup_info_app(tt, console_target) is not None
    finally:
        rc, out = finalize_backup(
            tt,
            SSL_APP_URI,
            backup_id,
            extra_args=ssl_args(tt_app),
            env=env,
        )

    assert rc == 0, f"tt backup finalize over TLS failed:\n{out}"
    assert get_backup_info_app(tt, console_target) is None
    assert not target_dir.exists()


@pytest.mark.tt_app(**TT_BACKUP_SSL_APP)
@pytest.mark.parametrize(
    "ca_file, case, expected_error",
    [
        # A plain dial against a TLS-only listener never gets a greeting.
        (None, "no TLS material at all", "failed to read Tarantool greeting"),
        (
            "localhost.crt",
            "a certificate authority that signed nothing",
            "certificate verify failed",
        ),
    ],
    ids=["no_ssl_flags", "wrong_ca"],
)
def test_ssl_start_without_usable_ssl_flags_takes_no_backup(
    tt,
    tt_app,
    ca_file,
    case,
    expected_error,
):
    """The --ssl* flags are what makes the dial work: without them, or with a
    certificate authority that did not sign the instance's certificate, the
    command must fail at the TLS layer and leave the instance and the
    filesystem untouched."""
    console_target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-ssl-refused"
    extra_args = [] if ca_file is None else ssl_args(tt_app, ca_file=ca_file)

    rc, out = start_backup(
        tt,
        SSL_APP_CREDENTIALS_URI,
        backup_id,
        extra_args=extra_args,
    )

    assert rc != 0, f"tt backup start must not succeed with {case}:\n{out}"
    # The reason has to be the connection, not a later step: a failure past
    # the dial would also satisfy rc != 0 and prove nothing about TLS.
    assert expected_error in out, f"expected a TLS-layer failure with {case}:\n{out}"
    assert not Path(backup_dir(backup_id)).exists(), out
    assert get_backup_info_app(tt, console_target) is None, out
