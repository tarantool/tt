#!/usr/bin/env python3
from subprocess import PIPE, STDOUT, run

import pytest

AeonConnectCommand = ("aeon", "connect")


def check_tt_aeon_response(output: str):
    expected = ["Aeon responses at", "Processing piped input", "EOF on pipe"]
    logs = output.split("\n")
    for line in logs:
        print(line)
        for e in expected:
            if e in line:
                expected.remove(e)
                break
        if len(expected) == 0:
            return

    assert False, f"Not found all expected Log records: {expected}"


@pytest.mark.parametrize(
    "args",
    [
        ("--transport", "plain"),
        ("--transport=plain",),
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
    cmd = [str(tt_cmd), *AeonConnectCommand]
    cmd += (a.format(**certificates) for a in args)
    print(f"Aeon plain at: {aeon_plain}")
    if isinstance(aeon_plain, int):
        cmd.append(f"localhost:{aeon_plain}")
    else:
        cmd.append(f"unix://{aeon_plain}")

    print(f"Run: {' '.join(cmd)}")
    tt = run(
        cmd,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )
    check_tt_aeon_response(tt.stderr)
    assert tt.returncode == 0


def test_cli_ssl_arguments_success(tt_cmd, aeon_ssl, certificates):
    cmd = [str(tt_cmd), *AeonConnectCommand, f"--sslcafile={certificates['ca']}"]
    print(f"Aeon ssl mode: {aeon_ssl}")
    if aeon_ssl == "mutual-tls":
        cmd += (
            f"--sslkeyfile={certificates['c_private']}",
            f"--sslcertfile={certificates['c_public']}",
        )
    elif aeon_ssl == "server-side":
        cmd += ("--transport", "ssl")

    cmd.append("localhost:50051")

    print(f"Run: {' '.join(cmd)}")
    tt = run(
        cmd,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )
    check_tt_aeon_response(tt.stderr)
    assert tt.returncode == 0


@pytest.mark.parametrize(
    "args, error",
    [
        ((), "Error: accepts 1 arg(s), received 0"),
        (
            ("localhost:50051", "@aeon_unix_socket"),
            "Error: accepts 1 arg(s), received 2",
        ),
        (
            (
                "--transport",
                "mode",
                "localhost:50051",
            ),
            'Error: invalid argument "mode" for "--transport" flag',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile=not-exits.key",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
                "localhost:50051",
            ),
            'not valid path to a private SSL key file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcertfile=not-exits.key",
                "--sslcafile={ca}",
                "localhost:50051",
            ),
            'not valid path to an SSL certificate file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcertfile={c_public}",
                "--sslcafile=not-exits.key",
                "localhost:50051",
            ),
            'not valid path to trusted certificate authorities (CA) file="not-exits.key"',
        ),
        (
            (
                "--sslcafile=not-exits.key",
                "localhost:50051",
            ),
            'not valid path to trusted certificate authorities (CA) file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
                "localhost:50051",
            ),
            "files Key and Cert must be specified both",
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcafile={ca}",
                "localhost:50051",
            ),
            "files Key and Cert must be specified both",
        ),
        (
            (
                "localhost:50051",
                "--transport=ssl",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
                "--sslkeyfile",
            ),
            "flag needs an argument: --sslkeyfile",
        ),
        (
            (
                "localhost:50051",
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcafile={ca}",
                "--sslcertfile",
            ),
            "flag needs an argument: --sslcertfile",
        ),
        (
            (
                "localhost:50051",
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
    cmd = [str(tt_cmd), *AeonConnectCommand]
    cmd += (a.format(**certificates) for a in args)

    print(f"Run: {' '.join(cmd)}")
    result = run(
        cmd,
        stderr=STDOUT,
        stdout=PIPE,
        text=True,
    )
    assert result.returncode != 0
    assert error in result.stdout
