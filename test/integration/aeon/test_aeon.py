#!/usr/bin/env python3
import os
import shutil
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


def copy_app(tmpdir, app_name):
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)


def copy_file(path_file, tmpdir):
    shutil.copy2(os.path.join(os.path.dirname(__file__), path_file), tmpdir)


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
        cmd.append(f"http://localhost:{aeon_plain}")
    else:
        cmd.append(f"unix://{aeon_plain}")

    print(f"unix://{aeon_plain}")
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

    cmd.append("http://localhost:50051")

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
        (
            (
                "--transport",
                "mode",
                "http://localhost:50051",
            ),
            'Error: invalid argument "mode" for "--transport" flag',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile=not-exits.key",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
                "http://localhost:50051",
            ),
            'not valid path to a private SSL key file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcertfile=not-exits.key",
                "--sslcafile={ca}",
                "http://localhost:50051",
            ),
            'not valid path to an SSL certificate file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcertfile={c_public}",
                "--sslcafile=not-exits.key",
                "http://localhost:50051",
            ),
            'not valid path to trusted certificate authorities (CA) file="not-exits.key"',
        ),
        (
            (
                "--sslcafile=not-exits.key",
                "http://localhost:50051",
            ),
            'not valid path to trusted certificate authorities (CA) file="not-exits.key"',
        ),
        (
            (
                "--transport=ssl",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
                "http://localhost:50051",
            ),
            "files Key and Cert must be specified both",
        ),
        (
            (
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcafile={ca}",
                "http://localhost:50051",
            ),
            "files Key and Cert must be specified both",
        ),
        (
            (
                "http://localhost:50051",
                "--transport=ssl",
                "--sslcertfile={c_public}",
                "--sslcafile={ca}",
                "--sslkeyfile",
            ),
            "flag needs an argument: --sslkeyfile",
        ),
        (
            (
                "http://localhost:50051",
                "--transport=ssl",
                "--sslkeyfile={c_private}",
                "--sslcafile={ca}",
                "--sslcertfile",
            ),
            "flag needs an argument: --sslcertfile",
        ),
        (
            (
                "http://localhost:50051",
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


def test_cli_plain_config_file_success(tt_cmd, tmp_path, aeon_plain_file):
    tmp_path = os.path.join(tmp_path, "data")

    print(f"Aeon plain at: {aeon_plain_file}")

    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "data"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
    )

    pathConfig = os.path.join(tmp_path, "config.yml")
    assert os.path.isfile(pathConfig)

    cmd = [str(tt_cmd), *AeonConnectCommand, pathConfig, "aeon-router-002"]
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


def test_cli_ssl_config_file_success(tt_cmd, tmp_path, aeon_ssl, certificates):
    print(f"Aeon ssl mode: {aeon_ssl}")
    for v in certificates.values():
        shutil.copy2(v, tmp_path)

    copy_file("data/config_ssl.yml", tmp_path)

    configPath = os.path.join(tmp_path, "config_ssl.yml")
    cmd = [str(tt_cmd), *AeonConnectCommand, configPath, "aeon-router-001"]

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


def test_cli_plain_not_config_fail(tt_cmd, tmp_path, aeon_plain_file):
    tmp_path = os.path.join(tmp_path, "data")

    print(f"Aeon plain at: {aeon_plain_file}")

    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "data"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
    )

    pathConfig = os.path.join(tmp_path, "not_config.yml")

    cmd = [str(tt_cmd), *AeonConnectCommand, pathConfig, "aeon-router-002"]
    print(f"Run: {' '.join(cmd)}")

    tt = run(
        cmd,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )

    assert "failed to recognize a connect destination, see the command examples" in tt.stderr
    assert tt.returncode != 0


def test_cli_invalid_ssl_config_file(
    tt_cmd,
    tmp_path,
    aeon_ssl,
):
    print(f"Aeon ssl mode: {aeon_ssl}")

    copy_file("data/invalid_config_ssl.yml", tmp_path)

    configPath = os.path.join(tmp_path, "invalid_config_ssl.yml")
    cmd = [str(tt_cmd), *AeonConnectCommand, configPath, "aeon-router-001"]

    print(f"Run: {' '.join(cmd)}")

    tt = run(
        cmd,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )

    print(f"stderr {tt.stderr}")

    assert "Error: not valid path to trusted certificate authorities" in tt.stderr
    assert tt.returncode != 0


# The test checks the handling of cases where the file contains
# invalid SSL keys or they are missing.
# For the test to work correctly, SSL keys are provided using specific flags.
# This allows simulating a scenario with missing or invalid keys
# to ensure that the system handles such cases properly.
def test_cli_ssl_config_flag_success(tt_cmd, tmp_path, aeon_ssl, certificates):
    print(f"Aeon ssl mode: {aeon_ssl}")

    copy_file("data/invalid_config_ssl.yml", tmp_path)

    configPath = os.path.join(tmp_path, "invalid_config_ssl.yml")
    cmd = [str(tt_cmd), *AeonConnectCommand, configPath, "aeon-router-001"]

    cmd += (
        f"--sslcafile={certificates['ca']}",
        f"--sslkeyfile={certificates['c_private']}",
        f"--sslcertfile={certificates['c_public']}",
    )

    cmd += ("--transport", "ssl")

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


@pytest.mark.parametrize("app_name", ["app"])
def test_cli_plain_app_success(tt_cmd, app_name, tmpdir_with_cfg, aeon_plain_file):
    print(f"Aeon plain at: {aeon_plain_file}")

    tmpdir = tmpdir_with_cfg
    copy_app(tmpdir, app_name)

    aeon_cmd = [str(tt_cmd), *AeonConnectCommand, "app:aeon-router-002"]
    print(f"Run: {' '.join(aeon_cmd)}")

    tt = run(
        aeon_cmd,
        cwd=tmpdir,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )

    check_tt_aeon_response(tt.stderr)
    assert tt.returncode == 0


def test_cli_plain_tcs_success(tt_cmd, tmpdir_with_cfg, request, aeon_plain_file):
    instance = request.getfixturevalue("tcs")
    tmpdir = tmpdir_with_cfg

    print(f"Aeon plain at: {aeon_plain_file}")
    conn = instance.conn()

    source_file = os.path.join(os.path.dirname(__file__), "data/config.yml")
    file = open(source_file, "r")
    config = file.read()

    conn.call("config.storage.put", "/prefix/config/all", config)
    creds = f"{instance.connection_username}:{instance.connection_password}@"

    cmd = [
        tt_cmd,
        *AeonConnectCommand,
        "http://" + creds + f"{instance.host}:{instance.port}/prefix",
        "aeon-router-002",
    ]

    tt = run(
        cmd,
        cwd=tmpdir,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )
    check_tt_aeon_response(tt.stderr)
    assert tt.returncode == 0


def test_cli_plain_etcd_success(tt_cmd, tmpdir_with_cfg, request, aeon_plain_file):
    instance = request.getfixturevalue("etcd")
    tmpdir = tmpdir_with_cfg

    print(f"Aeon plain at: {aeon_plain_file}")
    conn = instance.conn()

    source_file = os.path.join(os.path.dirname(__file__), "data/config.yml")
    file = open(source_file, "r")
    config = file.read()

    conn.put("/prefix/config/", config)

    creds = f"{instance.connection_username}:{instance.connection_password}@"

    cmd = [
        tt_cmd,
        *AeonConnectCommand,
        "http://" + creds + f"{instance.host}:{instance.port}/prefix",
        "aeon-router-002",
    ]
    tt = run(
        cmd,
        cwd=tmpdir,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )
    check_tt_aeon_response(tt.stderr)
    assert tt.returncode == 0


@pytest.mark.parametrize("app_name", ["app_ssl"])
def test_cli_ssl_app_flag_success(tt_cmd, app_name, tmpdir_with_cfg, aeon_ssl, certificates):
    print(f"Aeon ssl at: {aeon_ssl}")

    tmp_path = tmpdir_with_cfg
    copy_app(tmp_path, app_name)

    for v in certificates.values():
        shutil.copy2(v, tmp_path)

    cmd = [str(tt_cmd), *AeonConnectCommand, "app_ssl:aeon-router-001"]

    cmd += (
        f"--sslcafile={certificates['ca']}",
        f"--sslkeyfile={certificates['c_private']}",
        f"--sslcertfile={certificates['c_public']}",
    )

    cmd += ("--transport", "ssl")
    print(f"Run: {' '.join(cmd)}")

    tt = run(
        cmd,
        cwd=tmp_path,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )

    check_tt_aeon_response(tt.stderr)
    assert tt.returncode == 0


@pytest.mark.parametrize("app_name", ["app_ssl"])
def test_cli_ssl_app_success(tt_cmd, app_name, tmpdir_with_cfg, aeon_ssl, certificates):
    print(f"Aeon ssl at: {aeon_ssl}")

    tmp_path = tmpdir_with_cfg
    copy_app(tmp_path, app_name)

    for v in certificates.values():
        shutil.copy2(v, f"{tmp_path}/{app_name}")

    cmd = [str(tt_cmd), *AeonConnectCommand, "app_ssl:aeon-router-001"]

    print(f"Run: {' '.join(cmd)}")

    tt = run(
        cmd,
        cwd=tmp_path,
        capture_output=True,
        input="",
        text=True,
        encoding="utf-8",
    )

    check_tt_aeon_response(tt.stderr)
    assert tt.returncode == 0
