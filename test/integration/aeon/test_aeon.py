#!/usr/bin/env python3
from pathlib import Path
from subprocess import PIPE, STDOUT, run

import pytest

AeonConnectCommand = ("aeon", "connect")

FormatData = {
    "testdata": Path(__file__).parent / "testdata",
}


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
        ("--transport", "plain", "--sslkeyfile", "{testdata}/private.key"),
        ("--transport", "plain", "--sslcertfile", "{testdata}/certfile.key"),
        ("--transport", "plain", "--sslcafile", "{testdata}/ca.key"),
        (
            "--sslkeyfile={testdata}/private.key",
            "--sslcertfile={testdata}/certfile.key",
        ),
        (
            # "ssl" mode require existed path to files.
            "--transport=ssl",
            "--sslkeyfile={testdata}/private.key",
            "--sslcertfile={testdata}/certfile.key",
            "--sslcafile={testdata}/ca.key",
        ),
    ],
)
def test_cli_arguments_success(tt_cmd, args):
    args = (a.format(**FormatData) for a in args)
    result = run((tt_cmd, *AeonConnectCommand, *args))
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
                "--sslcertfile={testdata}/certfile.key",
                "--sslcafile={testdata}/ca.key",
            ),
            'not valid path to a private SSL key file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={testdata}/private.key",
                "--sslcertfile=not-exits.key",
                "--sslcafile={testdata}/ca.key",
            ),
            'not valid path to an SSL certificate file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={testdata}/private.key",
                "--sslcertfile={testdata}/certfile.key",
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
                "--sslcertfile={testdata}/certfile.key",
                "--sslcafile={testdata}/ca.key",
            ),
            "files Key and Cert must be specified both",
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={testdata}/private.key",
                "--sslcafile={testdata}/ca.key",
            ),
            "files Key and Cert must be specified both",
        ),
        (
            (
                "--transport=ssl",
                "--sslcertfile={testdata}/certfile.key",
                "--sslcafile={testdata}/ca.key",
                "--sslkeyfile",
            ),
            "flag needs an argument: --sslkeyfile",
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={testdata}/private.key",
                "--sslcafile={testdata}/ca.key",
                "--sslcertfile",
            ),
            "flag needs an argument: --sslcertfile",
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={testdata}/private.key",
                "--sslcertfile={testdata}/certfile.key",
                "--sslcafile",
            ),
            "flag needs an argument: --sslcafile",
        ),
    ],
)
def test_cli_arguments_fail(tt_cmd, args, error):
    args = (a.format(**FormatData) for a in args)
    result = run(
        (tt_cmd, *AeonConnectCommand, *args),
        stderr=STDOUT,
        stdout=PIPE,
        text=True,
    )
    assert result.returncode != 0
    assert error in result.stdout
