#!/usr/bin/env python3
from subprocess import PIPE, STDOUT, run

import pytest

AeonConnectCommand = ("aeon", "connect")


@pytest.mark.parametrize(
    "args",
    [
        (),
        ("--transport", "plain"),
        ("--transport=plain"),
        # "plain" mode ignores any ssl flags values.
        ("--transport", "plain", "--sslkeyfile", "not-exits.key"),
        ("--transport", "plain", "--sslcertfile", "not-exits.key"),
        ("--transport", "plain", "--sslcafile", "not-exits.key"),
        ("--transport", "plain", "--sslkeyfile", "{c_private}"),
        ("--transport", "plain", "--sslcertfile", "{c_public}"),
        ("--transport", "plain", "--sslcafile", "{ca}"),
    ],
)
def test_cli_plain_arguments_success(tt_cmd, aeon_plain, certificates, args):
    args = (a.format(**certificates) for a in args)
    print(f"aeon_server={aeon_plain}")
    result = run((tt_cmd, *AeonConnectCommand, *args))
    assert result.returncode == 0


def test_cli_ssl_arguments_success(tt_cmd, aeon_ssl, certificates):
    cmd = [tt_cmd, *AeonConnectCommand, f"--sslcafile={certificates['ca']}"]
    print(f"aeon ssl mode={aeon_ssl}")
    if aeon_ssl == "mutual-tls":
        cmd += (
            f"--sslkeyfile={certificates['c_private']}",
            f"--sslcertfile={certificates['c_public']}",
        )
    elif aeon_ssl == "server-side":
        cmd += ("--transport", "ssl")

    result = run(cmd)
    assert result.returncode == 0


@pytest.mark.parametrize(
    "args, error",
    [
        (
            ("--transport", "mode"),
            'Error: invalid argument "mode" for "--transport" flag',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile=not-exits.key",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
            ),
            'not valid path to a private SSL key file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcertfile=not-exits.key",
                "--sslcafile={ca}",
            ),
            'not valid path to an SSL certificate file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcertfile={c_public}",
                "--sslcafile=not-exits.key",
            ),
            'not valid path to trusted certificate authorities (CA) file="not-exits.key"',
        ),
        (
            ("--sslcafile=not-exits.key",),
            'not valid path to trusted certificate authorities (CA) file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
            ),
            "files Key and Cert must be specified both",
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcafile={ca}",
            ),
            "files Key and Cert must be specified both",
        ),
        (
            (
                "--transport=ssl",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
                "--sslkeyfile",
            ),
            "flag needs an argument: --sslkeyfile",
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcafile={ca}",
                "--sslcertfile",
            ),
            "flag needs an argument: --sslcertfile",
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcertfile={c_public}",
                "--sslcafile",
            ),
            "flag needs an argument: --sslcafile",
        ),
    ],
)
def test_cli_arguments_fail(tt_cmd, certificates, args, error):
    args = (a.format(**certificates) for a in args)
    result = run(
        (tt_cmd, *AeonConnectCommand, *args),
        stderr=STDOUT,
        stdout=PIPE,
        text=True,
    )
    assert result.returncode != 0
    assert error in result.stdout
