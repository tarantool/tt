"""A project keeps working after it is moved: the active links of
.tarantool are relative, and .env/activate.sh and activate.fish find the
project from their own path."""

import os
import re
import shutil
import subprocess
import tarfile
from pathlib import Path

import pytest

from utils import run_command_and_get_output

VERSION = "gc64-3.8.0-0-r94"


def make_sdk_archive(directory: Path) -> Path:
    source = directory / "archive" / "tarantool-enterprise"
    headers = source / "include" / "tarantool"
    headers.mkdir(parents=True)
    (headers / "module.h").write_text("/* enterprise headers */\n")
    executable = source / "tarantool"
    executable.write_text("#!/bin/sh\necho 'Tarantool Enterprise 3.8.0-0-r94'\n")
    executable.chmod(0o755)
    bundles = directory / "bundles"
    bundles.mkdir()
    archive = bundles / f"tarantool-enterprise-sdk-{VERSION}.linux.x86_64.tar.gz"
    with tarfile.open(archive, "w:gz") as bundle:
        bundle.add(source, arcname="tarantool-enterprise")
    return bundles


@pytest.fixture
def moved_project(tt_cmd, tmp_path):
    """Install tarantool into a project, then move the project."""
    bundles = make_sdk_archive(tmp_path)
    project = tmp_path / "a" / "b"
    project.mkdir(parents=True)
    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "install", "tarantool", VERSION, "--local-dir", str(bundles), "--force"],
        cwd=project,
    )
    assert rc == 0, output

    root = project / ".tarantool"
    for link in [root / "bin" / "tarantool", root / "include" / "tarantool"]:
        assert not os.readlink(link).startswith("/"), os.readlink(link)

    moved = tmp_path / "moved" / "b2"
    moved.parent.mkdir()
    shutil.move(project, moved)
    shutil.rmtree(tmp_path / "a")
    return moved


def test_moved_project_links_resolve(tt_cmd, moved_project):
    root = moved_project / ".tarantool"
    installation = root / "versions" / "tarantool-ee" / "tarantool-ee_3.8.0"
    assert (root / "bin" / "tarantool").resolve(strict=True) == installation / "tarantool"
    assert (root / "include" / "tarantool").resolve(strict=True) == (
        installation / "include" / "tarantool"
    )

    rc, output = run_command_and_get_output([tt_cmd, "binary", "list"], cwd=moved_project)
    assert rc == 0, output
    assert re.search(r"^tarantool-ee +3\.8\.0 \[active\]$", output, re.M), output
    assert "->" not in output


def shell_path(name: str) -> str | None:
    """The shell name, if it is installed and runs a command substitution:
    a zsh built by hand may hang on one."""
    executable = shutil.which(name)
    if executable is None:
        return None
    probe = "echo (echo ok)" if name == "fish" else "echo $(echo ok)"
    try:
        process = subprocess.run(
            [executable, "-c", probe],
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            timeout=5,
        )
    except subprocess.TimeoutExpired:
        return None
    return executable if process.stdout.strip() == "ok" else None


ACTIVATE = {
    "bash": ("bash", "-c", '. "$1"/.env/activate.sh && {report}', "sh"),
    "zsh": ("zsh", "-c", 'source "$1"/.env/activate.sh && {report}', "zsh"),
    "fish": ("fish", "-c", "source $argv[1]/.env/activate.fish; and {report}", "fish"),
}

REPORT = {
    "bash": 'printf "%s\\n%s\\n%s\\n" "$TT_PROJECT_DIR" "$(command -v tarantool)" '
    '"$TT_CLI_TARANTOOL_PREFIX"',
    "zsh": 'printf "%s\\n%s\\n%s\\n" "$TT_PROJECT_DIR" "$(command -v tarantool)" '
    '"$TT_CLI_TARANTOOL_PREFIX"',
    "fish": 'printf "%s\\n%s\\n%s\\n" $TT_PROJECT_DIR (command -v tarantool) '
    "$TT_CLI_TARANTOOL_PREFIX",
}


@pytest.mark.parametrize("shell", ["bash", "zsh", "fish"])
@pytest.mark.parametrize("from_subdirectory", [False, True])
def test_moved_project_activate(moved_project, shell, from_subdirectory):
    executable = shell_path(shell)
    if executable is None:
        pytest.skip(f"{shell} is not installed")

    _, flag, script, name = ACTIVATE[shell]
    command = [executable, flag, script.format(report=REPORT[shell])]
    if shell == "fish":
        command.append(str(moved_project))
    else:
        command.extend([name, str(moved_project)])

    cwd = moved_project
    if from_subdirectory:
        cwd = moved_project / "src"
        cwd.mkdir(exist_ok=True)

    env = dict(os.environ)
    env.pop("TT_PROJECT_DIR", None)
    env.pop("TT_USER_OLD_PATH", None)
    env["HOME"] = str(moved_project.parent)
    env["XDG_CONFIG_HOME"] = str(moved_project.parent / "config")
    process = subprocess.run(
        command,
        cwd=cwd,
        env=env,
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        timeout=60,
    )
    assert process.returncode == 0, process.stderr

    project_dir, tarantool, prefix = process.stdout.splitlines()[-3:]
    assert Path(project_dir).resolve() == moved_project.resolve()
    installation = moved_project / ".tarantool" / "versions" / "tarantool-ee" / "tarantool-ee_3.8.0"
    assert Path(tarantool).resolve() == installation.resolve() / "tarantool"
    assert Path(prefix).resolve() == (moved_project / ".tarantool").resolve()


@pytest.mark.parametrize("shell", ["bash", "fish"])
def test_deactivate_restores_path(moved_project, shell):
    executable = shell_path(shell)
    if executable is None:
        pytest.skip(f"{shell} is not installed")

    if shell == "fish":
        script = (
            "set -l before $PATH; source $argv[1]/.env/activate.fish; "
            "source $argv[1]/.env/deactivate.fish; "
            "test (string join : $before) = (string join : $PATH); "
            "and not set -q TT_PROJECT_DIR; and echo restored"
        )
        command = [executable, "-c", script, str(moved_project)]
    else:
        script = (
            'before=$PATH; . "$1"/.env/activate.sh; . "$1"/.env/deactivate.sh; '
            '[ "$before" = "$PATH" ] && [ -z "${TT_PROJECT_DIR+x}" ] && echo restored'
        )
        command = [executable, "-c", script, "sh", str(moved_project)]

    env = dict(os.environ)
    env.pop("TT_USER_OLD_PATH", None)
    env["HOME"] = str(moved_project.parent)
    env["XDG_CONFIG_HOME"] = str(moved_project.parent / "config")
    process = subprocess.run(
        command,
        env=env,
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        timeout=60,
    )
    assert process.returncode == 0, process.stderr
    assert process.stdout.strip().endswith("restored")
