"""System-scope coverage for `tt package list` and `tt package uninstall`.

The system scope installs into `/usr`, so exercising it means writing to a tree
no test may touch on a developer machine and no CI job should own. A container
gives a disposable `/usr` and a root user, which is the only way these paths get
run at all — every other test drives the project or user scope.
"""

import json
import shutil
import subprocess

import pytest

# The image only needs a shell; tt is mounted in and runs standalone.
IMAGE = "ubuntu"

pytestmark = pytest.mark.docker


def docker_available() -> bool:
    if shutil.which("docker") is None:
        return False
    return subprocess.run(["docker", "ps"], capture_output=True).returncode == 0


def run_in_container(tt_cmd, staging, script: str) -> subprocess.CompletedProcess:
    """Copy the staging tree over /usr in a container and run `script` there.

    tt is mounted read-only at /tt. `cp -a /staging/.` merges the fixture into
    the image's own /usr rather than replacing it, so the container keeps its
    shell and coreutils.
    """
    return subprocess.run(
        [
            "docker",
            "run",
            "--rm",
            "-v",
            f"{tt_cmd}:/tt:ro",
            "-v",
            f"{staging}:/staging:ro",
            IMAGE,
            "bash",
            "-c",
            f"cp -a /staging/. /usr/ && {script}",
        ],
        capture_output=True,
        text=True,
    )


@pytest.fixture(autouse=True)
def require_docker(tt_cmd):
    """Skip unless docker runs and the built tt is executable in the image.

    tt is built for the host, so a macOS binary cannot run in a Linux container.
    Skipping on that is honest: on Linux CI the same test executes for real.
    """
    if not docker_available():
        pytest.skip("docker is not available on this system")

    probe = subprocess.run(
        ["docker", "run", "--rm", "-v", f"{tt_cmd}:/tt:ro", IMAGE, "/tt", "version"],
        capture_output=True,
        text=True,
    )
    if probe.returncode != 0:
        pytest.skip(f"the built tt does not run in {IMAGE}: {probe.stderr.strip()[:200]}")


def test_system_scope_list(tt_cmd, system_staging):
    """`--scope system` reads /usr, not the working directory's .rocks/."""
    result = run_in_container(
        tt_cmd,
        system_staging.root,
        "/tt package list --scope system -o json",
    )
    assert result.returncode == 0, result.stderr

    payload = json.loads(result.stdout)
    assert payload["scope"] == "system"
    assert payload["root"] == "/usr"
    assert {entry["name"] for entry in payload["packages"]} == {"monitoring", "alerting"}

    # A system tree holds guests only: nothing there is the project's own package.
    assert all(entry["primary"] is False for entry in payload["packages"])


def test_system_scope_uninstall_refcounts(tt_cmd, system_staging):
    """Removal in /usr drops the sole-owner dependency and keeps the shared one."""
    script = (
        "/tt package uninstall monitoring --scope system --yes && "
        "echo --- && "
        "ls /usr/share/tarantool && "
        "echo --- && "
        "ls /usr/manifests"
    )
    result = run_in_container(tt_cmd, system_staging.root, script)
    assert result.returncode == 0, result.stderr

    assert "removed unused dependency metrics" in result.stdout
    assert "kept checks (required by alerting)" in result.stdout

    _, modules, manifests = result.stdout.split("---")
    assert "monitoring" not in modules.split()
    assert "metrics" not in modules.split()
    assert "checks" in modules.split()
    assert "alerting" in modules.split()
    assert manifests.split() == ["alerting"]


def test_system_scope_uninstall_unknown_package(tt_cmd, system_staging):
    """A miss in the system scope is exit 1, and touches nothing."""
    result = run_in_container(
        tt_cmd,
        system_staging.root,
        "/tt package uninstall nosuch --scope system --yes; echo exit=$?; ls /usr/manifests",
    )
    assert result.returncode == 0, result.stderr

    assert "not installed" in result.stderr
    assert "exit=1" in result.stdout
    assert "monitoring" in result.stdout
