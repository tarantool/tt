import os
from dataclasses import dataclass
from pathlib import Path
from subprocess import run

import pytest

# Path to the directory containing helper scripts.
HELPERS_DIR = Path(__file__).parent / "helpers"

# List of shells for which completion helpers for tests are implemented.
SUPPORTED_SHELLS: list[str] = ["bash", "fish", "zsh"]


@dataclass
class Completion:
    """A dataclass to hold the shell and the completion file path."""

    shell: str
    file: Path


@pytest.fixture(scope="session", params=SUPPORTED_SHELLS)
def completion(tt_cmd: Path, tmp_path_factory: pytest.TempPathFactory, request) -> Completion:
    shell = request.param

    cmd = [tt_cmd, "completion", shell]
    process = run(cmd, text=True, capture_output=True)
    assert process.returncode == 0, f"Failed to generate {shell} completion script for testing."

    # spell-checker:ignore getbasetemp

    completion_file = tmp_path_factory.getbasetemp() / f"tt_completion.{shell}"
    completion_file.write_text(process.stdout)

    return Completion(shell=shell, file=completion_file)


@pytest.fixture(autouse=True)
def skip_no_helpers(request: pytest.FixtureRequest, completion: Completion) -> None:
    marker = request.node.get_closest_marker("skip_unimplemented")
    if marker is not None:
        helper = HELPERS_DIR / f"{completion.shell}_completion.sh"
        if not (helper.is_file() and os.access(helper, os.X_OK)):
            pytest.skip(f"TODO: implement helper script for shell: {completion.shell}")


def pytest_configure(config):
    config.addinivalue_line(  # spell-checker:ignore addinivalue
        "markers",
        "skip_unimplemented: skip test if no helper script implemented for this shell",
    )
