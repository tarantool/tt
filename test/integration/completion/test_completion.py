import os
from pathlib import Path
from subprocess import run

import pytest

from . import HELPERS_DIR, Completion
from utils import run_command_and_get_output

# Note: Don't forget to update the list of subcommands in `tt` if you add a new root command.
# This almost full list, but without `help` subcommand.
tt_root_command: set[str] = set(
    (
        "rocks",
        "aeon",
        "clean",
        "download",
        "log",
        "uninstall",
        "binaries",
        "cluster",
        "enable",
        "logrotate",
        "run",
        "version",
        "build",
        "completion",
        "env",
        "modules",
        "search",
        "cartridge",
        "connect",
        "init",
        "pack",
        "start",
        "cat",
        "coredump",
        "install",
        "play",
        "status",
        "cfg",
        "create",
        "instances",
        "replicaset",
        "stop",
        "check",
        "daemon",
        "kill",
        "restart",
        "tcm",
    )
)

# Define test cases in tuple: (input line to complete; set of expected completions).
CompletionCases = list[tuple[str, set[str]]]

# Validates the root command completion list in `tt` to confirm that
# the `rocks` command is correctly included.
# Assumes that completion for other commands is handled by the `Cobra` package.
ROOT_TT_TEST_CASES: CompletionCases = [
    ("tt ", {*tt_root_command, "help"}),
    (
        "tt -",
        {
            "--verbose",
            "--no-prompt",
            "-V",
            "--help",
            "-S",
            "--internal",
            "-c",
            "--local",
            "--cfg",
            "-I",
            "-L",
            "--system",
            "--self",
            "-s",
            "-h",
        },
    ),
    ("tt help ", tt_root_command),
]

# Cases for testing `tt rocks` command.
ROCKS_TEST_CASES: CompletionCases = [
    (
        "tt rocks ",
        {
            "help",
            "admin",
            "build",
            "config",
            "doc",
            "download",
            "install",
            "lint",
            "list",
            "make",
            "make_manifest",
            "make-manifest",
            "new_version",
            "new-version",
            "pack",
            "purge",
            "remove",
            "search",
            "show",
            "test",
            "unpack",
            "which",
            "write_rockspec",
            "write-rockspec",
        },
    ),
    (
        "tt rocks --",
        {
            "--help",
            "--version",
            "--dev",
            "--server",
            "--from",
            "--only-server",
            "--only-from",
            "--only-sources",
            "--only-sources-from",
            "--namespace",
            "--lua-dir",
            "--lua-version",
            "--tree",
            "--to",
            "--local",
            "--global",
            "--no-project",
            "--verbose",
            "--timeout",
            "--project-tree",
            "--pack-binary-rock",
            "--branch",
            "--sign",
        },
    ),
    (
        "tt rocks build -",  # Single dash to get a short options.
        {
            "--help",
            "-h",
            "--version",  # Global options.
            "--dev",
            "--server",
            "--from",
            "--only-server",
            "--only-from",
            "--only-sources",
            "--only-sources-from",
            "--namespace",
            "--lua-dir",
            "--lua-version",
            "--tree",
            "--to",
            "--local",
            "--global",
            "--no-project",
            "--verbose",
            "--timeout",
            "--project-tree",
            "--pack-binary-rock",
            "--branch",
            "--sign",
            # Build specific options.
            "--only-deps",
            "--deps-only",
            "--pin",
            "--no-install",
            "--no-doc",
            "--keep",
            "--force",
            "--force-fast",
            "--verify",
            "--check-lua-versions",
            "--no-manifest",
            "--chdir",
            "--deps-mode",
            "--nodeps",
        },
    ),
    (
        "tt rocks admin ",
        {
            "help",
            "add",
            "make_manifest",
            "make-manifest",
            "refresh_cache",
            "refresh-cache",
            "remove",
        },
    ),
    ("tt rocks config --scope ", {"system", "user", "project"}),
]


def get_completions_from_shell(
    tt_cmd: Path,
    completion: Completion,
    line_to_complete: str,
    tmp_path: Path,
) -> set[str]:
    """
    Gets completion suggestions from a given shell for a partial command line.

    It runs helper script for the specified shell, specifying necessary
    paths and commands with arguments.
    """
    helper_script = HELPERS_DIR / f"{completion.shell}_completion.sh"

    path = os.environ.get("PATH", "")
    path = f"{tt_cmd.parent}:{path}"

    process = run(
        [helper_script, completion.file, line_to_complete],
        text=True,
        capture_output=True,
        cwd=tmp_path,
        env=dict(os.environ, PATH=path),
    )
    if process.returncode != 0:
        print("STDERR:", process.stderr)
        print("STDOUT:", process.stdout)
        assert False, f"Failed to run {completion.shell} helper script."

    return set(
        line.split("\t")[0] for line in process.stdout.splitlines() if line.strip()
    )


@pytest.mark.skip_unimplemented
@pytest.mark.parametrize(
    "input_line, expected", (*ROCKS_TEST_CASES, *ROOT_TT_TEST_CASES)
)
def test_actual_completions(
    tt_cmd: Path,
    completion: Completion,
    tmp_path: Path,
    input_line: str,
    expected: set[str],
) -> None:
    """
    Test actual completion suggestions for various command lines and shells.
    It generates the completion script, then uses a helper script to get completions.
    """
    actual = get_completions_from_shell(tt_cmd, completion, input_line, tmp_path)

    assert expected == actual, (
        f"Completion mismatch for '{input_line}' in {completion.shell}.\n"
        f"Expected: {expected}\n"
        f"Got:      {actual}"
    )


def test_completion_loaded(completion: Completion) -> None:
    """
    Test that the completion script for each shell can be generated by `tt completion <shell>`
    and that the generated script can be loaded without errors in its respective shell.
    """
    rc, source_output = run_command_and_get_output(
        [completion.shell, "-c", f"source {completion.file}"],
    )
    assert rc == 0, f"Failed to apply '{completion.shell}' completion script."
