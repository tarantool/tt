"""The authentication method of a binary connection: --auth-type and the
auth_type parameter of a URI, for tt connect, tt data play and
tt replicaset.

pap-sha256 is in Tarantool Enterprise since 2.11 and in the Community
Edition since 3.9, as is the SSL transport of IPROTO, which pap-sha256 needs:
the tests of a pap-sha256 user are skipped on a Tarantool without them."""

import os
import shutil
import socket
import subprocess
from pathlib import Path

import pytest

from utils import (
    TARANTOOL_SSL_REQUIRED,
    is_tarantool_pap_sha256_supported,
    is_tarantool_ssl_supported,
    run_command_and_get_output,
    wait_file,
)

AUTH_APP = Path(__file__).parent / "test_auth_type"
CERTS = Path(__file__).parent / "test_ssl_app"
PLAY_XLOG = Path(__file__).parent.parent / "data" / "test_file" / "test.xlog"

PASSWORD = "secret"
WHO_AM_I = "return box.session.user()"

PLAIN_REFUSED = (
    "authentication method pap-sha256 does not support an unencrypted connection, "
    "use the ssl transport"
)
CREDS_REFUSED = "User not found or supplied credentials are invalid"
PAP_SERVER_HINT = "the server authenticates with pap-sha256, which needs the ssl transport"


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class AuthInstance:
    def __init__(self, workdir, plain_port, ssl_port, pap):
        self.workdir = workdir
        self.plain_port = plain_port
        self.ssl_port = ssl_port
        self.pap = pap

    def plain(self, user, query=""):
        return f"{user}:{PASSWORD}@127.0.0.1:{self.plain_port}{query}"

    def ssl(self, user, query=""):
        params = f"transport=ssl&ssl_ca_file={self.workdir / 'ca.crt'}"
        if query:
            params += "&" + query
        return f"{user}:{PASSWORD}@127.0.0.1:{self.ssl_port}?{params}"


@pytest.fixture(scope="module")
def auth_instance(tmp_path_factory):
    """An instance of test_auth_type/init.lua: pap-sha256 the auth_type of
    the server and the method of user "pap" where Tarantool has it, chap-sha1
    the method of user "chap", and a TLS port where Tarantool has one."""
    workdir = tmp_path_factory.mktemp("auth_type")
    shutil.copy(AUTH_APP / "init.lua", workdir)
    for name in ["localhost.key", "localhost.crt", "ca.crt"]:
        shutil.copy(CERTS / name, workdir)

    instance = AuthInstance(
        workdir,
        free_port(),
        free_port() if is_tarantool_ssl_supported() else None,
        is_tarantool_pap_sha256_supported(),
    )

    env = dict(os.environ, TEST_PLAIN_PORT=str(instance.plain_port))
    if instance.ssl_port is not None:
        env["TEST_SSL_PORT"] = str(instance.ssl_port)
    if instance.pap:
        env["TEST_PAP"] = "1"

    with open(workdir / "tarantool.log", "w") as log:
        process = subprocess.Popen(
            ["tarantool", "init.lua"],
            cwd=workdir,
            env=env,
            stdout=log,
            stderr=subprocess.STDOUT,
        )
    try:
        assert wait_file(workdir, "ready", []) != "", (workdir / "tarantool.log").read_text()
        yield instance
    finally:
        process.kill()
        process.wait()


@pytest.fixture
def pap_instance(auth_instance):
    if not auth_instance.pap:
        pytest.skip("pap-sha256 needs Tarantool Enterprise 2.11+ or Community 3.9+")
    return auth_instance


@pytest.fixture
def pap_ssl_instance(pap_instance):
    if pap_instance.ssl_port is None:
        pytest.skip(TARANTOOL_SSL_REQUIRED)
    return pap_instance


def tt_connect(tt_cmd, cwd, uri, *flags):
    """Run `tt connect URI -f-` that prints the user of the session."""
    return run_command_and_get_output(
        [tt_cmd, "connect", uri, "-f-", *flags],
        input=WHO_AM_I,
        cwd=cwd,
    )


def assert_session_user(rc, output, user):
    assert rc == 0, output
    assert output == f"---\n- {user}\n...\n\n"


@pytest.mark.parametrize(
    "query, flags",
    [
        pytest.param("", [], id="auto"),
        pytest.param("", ["--auth-type", "chap-sha1"], id="flag"),
        pytest.param("?auth_type=chap-sha1", [], id="uri"),
        pytest.param("?transport=plain&auth_type=chap-sha1", [], id="uri plain"),
        pytest.param("?auth_type=auto", [], id="uri auto"),
    ],
)
def test_connect_chap_sha1(tt_cmd, auth_instance, query, flags):
    """chap-sha1 over a plain connection: explicit or automatic, whatever the
    auth_type of the server, for a user whose password is a chap-sha1 one."""
    rc, output = tt_connect(
        tt_cmd,
        auth_instance.workdir,
        auth_instance.plain("chap", query),
        *flags,
    )
    assert_session_user(rc, output, "chap")


@pytest.mark.parametrize(
    "query, flags",
    [
        pytest.param("", ["--auth-type", "pap-sha256"], id="flag"),
        pytest.param("?auth_type=pap-sha256", [], id="uri"),
        pytest.param("?transport=plain&auth_type=pap-sha256", [], id="uri plain"),
        pytest.param("?transport=plain", ["--auth-type=pap-sha256"], id="flag uri plain"),
    ],
)
def test_connect_pap_sha256_plain_refused(tt_cmd, auth_instance, query, flags):
    """pap-sha256 sends the password as it is: tt refuses it over a plain
    connection before it sends anything, on any Tarantool."""
    rc, output = tt_connect(
        tt_cmd,
        auth_instance.workdir,
        auth_instance.plain("pap", query),
        *flags,
    )
    assert rc != 0
    assert PLAIN_REFUSED in output
    assert PASSWORD not in output


@pytest.mark.parametrize(
    "query, flags, error",
    [
        pytest.param(
            "",
            ["--auth-type", "md5"],
            'invalid argument "md5" for "--auth-type"',
            id="flag",
        ),
        pytest.param(
            "",
            ["--auth-type", "PAP-SHA256"],
            'invalid argument "PAP-SHA256" for "--auth-type"',
            id="flag case",
        ),
        pytest.param(
            "?auth_type=md5",
            [],
            '"auth_type": unknown authentication method in the query of a URI',
            id="uri",
        ),
        pytest.param(
            "?auth_type=chap-sha1",
            ["--auth-type", "chap-sha1"],
            '"auth_type": is specified with a flag and in the query of a URI',
            id="flag and uri",
        ),
    ],
)
def test_connect_auth_type_invalid(tt_cmd, auth_instance, query, flags, error):
    rc, output = tt_connect(
        tt_cmd,
        auth_instance.workdir,
        auth_instance.plain("chap", query),
        *flags,
    )
    assert rc != 0
    assert error in output
    assert PASSWORD not in output


def test_connect_pap_server_plain(tt_cmd, pap_instance):
    """Over a plain connection tt authenticates with chap-sha1 even when the
    auth_type of the server is pap-sha256: a user whose password predates
    the auth_type logs in, and one of a pap-sha256 password is refused with
    the error of the server and what to do about it."""
    rc, output = tt_connect(tt_cmd, pap_instance.workdir, pap_instance.plain("chap"))
    assert_session_user(rc, output, "chap")

    rc, output = tt_connect(tt_cmd, pap_instance.workdir, pap_instance.plain("pap"))
    assert rc != 0
    assert CREDS_REFUSED in output
    assert PAP_SERVER_HINT in output


@pytest.mark.tls
@pytest.mark.parametrize(
    "query, flags",
    [
        pytest.param("", [], id="auto"),
        pytest.param("", ["--auth-type", "pap-sha256"], id="flag"),
        pytest.param("auth_type=pap-sha256", [], id="uri"),
    ],
)
def test_connect_pap_sha256_ssl(tt_cmd, pap_ssl_instance, query, flags):
    """Over TLS the automatic method is the auth_type of the server,
    pap-sha256 here, and pap-sha256 may be asked for explicitly."""
    rc, output = tt_connect(
        tt_cmd,
        pap_ssl_instance.workdir,
        pap_ssl_instance.ssl("pap", query),
        *flags,
    )
    assert_session_user(rc, output, "pap")


@pytest.mark.tls
def test_connect_chap_sha1_ssl(tt_cmd, pap_ssl_instance):
    """A user whose password predates the pap-sha256 auth_type of the server
    logs in over TLS with chap-sha1 asked for: the automatic method is the
    server's, which the password does not fit. chap-sha1 does not fit a
    pap-sha256 password either."""
    rc, output = tt_connect(
        tt_cmd,
        pap_ssl_instance.workdir,
        pap_ssl_instance.ssl("chap", "auth_type=chap-sha1"),
    )
    assert_session_user(rc, output, "chap")

    rc, output = tt_connect(tt_cmd, pap_ssl_instance.workdir, pap_ssl_instance.ssl("chap"))
    assert rc != 0
    assert CREDS_REFUSED in output

    rc, output = tt_connect(
        tt_cmd,
        pap_ssl_instance.workdir,
        pap_ssl_instance.ssl("pap"),
        "--auth-type=chap-sha1",
    )
    assert rc != 0
    assert CREDS_REFUSED in output


def tt_data_play(tt_cmd, cwd, uri, *flags):
    return run_command_and_get_output(
        [tt_cmd, "data", "play", uri, PLAY_XLOG, "--space=999", "--dry-run", *flags],
        cwd=cwd,
    )


def test_data_play_auth_type(tt_cmd, auth_instance):
    """tt data play takes --auth-type and the auth_type of a URI as
    tt connect does."""
    for uri, flags in [
        (auth_instance.plain("chap"), ["--auth-type", "chap-sha1"]),
        (auth_instance.plain("chap", "?auth_type=chap-sha1"), []),
    ]:
        rc, output = tt_data_play(tt_cmd, auth_instance.workdir, uri, *flags)
        assert rc == 0, output
        assert "Dry run: 3 transactions with 3 data changes would be applied" in output

    for uri, flags in [
        (auth_instance.plain("pap"), ["--auth-type", "pap-sha256"]),
        (auth_instance.plain("pap", "?auth_type=pap-sha256"), []),
    ]:
        rc, output = tt_data_play(tt_cmd, auth_instance.workdir, uri, *flags)
        # An invalid request, not a failed connection: nothing is dialed.
        assert rc == 1, output
        assert PLAIN_REFUSED in output
        assert PASSWORD not in output


@pytest.mark.tls
def test_data_play_pap_sha256_ssl(tt_cmd, pap_ssl_instance):
    for uri, flags in [
        (pap_ssl_instance.ssl("pap"), ["--auth-type", "pap-sha256"]),
        (pap_ssl_instance.ssl("pap", "auth_type=pap-sha256"), []),
        (pap_ssl_instance.ssl("pap"), []),
    ]:
        rc, output = tt_data_play(tt_cmd, pap_ssl_instance.workdir, uri, *flags)
        assert rc == 0, output
        assert "Dry run: 3 transactions with 3 data changes would be applied" in output


def tt_replicaset_status(tt_cmd, cwd, uri, *flags):
    # The instance is configured with box.cfg: its orchestrator is a custom one.
    return run_command_and_get_output(
        [tt_cmd, "replicaset", "status", "--custom", uri, *flags],
        cwd=cwd,
    )


def test_replicaset_status_auth_type(tt_cmd, auth_instance):
    """tt replicaset takes --auth-type: pap-sha256 over a plain connection
    is refused as tt connect refuses it, chap-sha1 connects."""
    rc, output = tt_replicaset_status(
        tt_cmd,
        auth_instance.workdir,
        auth_instance.plain("pap"),
        "--auth-type",
        "pap-sha256",
    )
    assert rc != 0
    assert PLAIN_REFUSED in output
    assert PASSWORD not in output

    rc, output = tt_replicaset_status(
        tt_cmd,
        auth_instance.workdir,
        auth_instance.plain("chap"),
        "--auth-type",
        "chap-sha1",
    )
    assert rc == 0, output
    assert "Orchestrator:      custom" in output


@pytest.mark.tls
def test_replicaset_status_pap_sha256_ssl(tt_cmd, pap_ssl_instance):
    rc, output = tt_replicaset_status(
        tt_cmd,
        pap_ssl_instance.workdir,
        pap_ssl_instance.ssl("pap"),
        "--auth-type",
        "pap-sha256",
    )
    assert rc == 0, output
    assert "Orchestrator:      custom" in output
