"""End-to-end coverage for `tt registry list`.

These cases need no rock server at all: the command reports the configuration,
not what any server answers. They run outside a project on purpose — the
command has to work there, because the flag and the environment configure
registries whether or not a manifest is around.
"""

import json
import os
import subprocess
from pathlib import Path

import pytest
import yaml

DEFAULT_SERVERS = ["https://rocks.tarantool.org/", "https://luarocks.org/"]

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'
registries = ['https://manifest.example/']

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
"""


@pytest.fixture()
def run_registry(tt_cmd, tmp_path):
    """Run `tt registry ...` in an empty directory."""

    def run(*args: str, env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
        return subprocess.run(
            [str(tt_cmd), "registry", *args],
            cwd=tmp_path,
            capture_output=True,
            text=True,
            env=dict(os.environ, **(env or {})),
        )

    return run


def urls(output: str) -> list[str]:
    """The URLs of a JSON listing, in order."""
    return [entry["url"] for entry in json.loads(output)]


def sources(output: str) -> list[str]:
    """The layers of a JSON listing, in order."""
    return [entry["source"] for entry in json.loads(output)]


def test_list_falls_back_to_the_built_in_servers(run_registry):
    """With nothing configured the answer is the defaults, not an empty list."""
    result = run_registry("list", "-o", "json")

    assert result.returncode == 0, result.stderr
    assert urls(result.stdout) == DEFAULT_SERVERS
    assert set(sources(result.stdout)) == {"default"}


def test_the_flag_overrides_the_environment(run_registry):
    """Precedence is flag over environment, and the winner is the whole list."""
    result = run_registry(
        "list",
        "-o",
        "json",
        "--registry",
        "https://flag.example/",
        env={"TT_REGISTRIES": "https://env.example/"},
    )

    assert result.returncode == 0, result.stderr
    # Replacement, not merging: the environment entry is gone, not appended.
    assert urls(result.stdout) == ["https://flag.example/"]
    assert sources(result.stdout) == ["flag"]


def test_the_environment_overrides_the_manifest(run_registry, tmp_path):
    """A manifest in the working directory is the layer below the environment."""
    (tmp_path / "app.manifest.toml").write_text(MANIFEST)

    from_manifest = run_registry("list", "-o", "json")
    assert from_manifest.returncode == 0, from_manifest.stderr
    assert urls(from_manifest.stdout) == ["https://manifest.example/"]
    assert sources(from_manifest.stdout) == ["manifest"]

    from_env = run_registry("list", "-o", "json", env={"TT_REGISTRIES": "https://env.example/"})
    assert from_env.returncode == 0, from_env.stderr
    assert urls(from_env.stdout) == ["https://env.example/"]
    assert sources(from_env.stdout) == ["env"]


def test_the_environment_keeps_its_order(run_registry):
    """The list is the resolution order, so it is reported as written."""
    result = run_registry(
        "list",
        "-o",
        "json",
        env={"TT_REGISTRIES": "https://a.example/, https://b.example/"},
    )

    assert result.returncode == 0, result.stderr
    assert urls(result.stdout) == ["https://a.example/", "https://b.example/"]


def test_a_relative_directory_is_reported_absolute(run_registry, tmp_path):
    """A path entry is reported as the engine will use it, not as it was typed."""
    result = run_registry("list", "-o", "json", "--registry", "./mirror")

    assert result.returncode == 0, result.stderr
    assert urls(result.stdout) == [str(Path(tmp_path) / "mirror")]


def test_the_table_names_both_columns(run_registry):
    """The layer is a column, because the URL alone does not explain itself."""
    result = run_registry("list", "-o", "table", "--registry", "https://flag.example/")

    assert result.returncode == 0, result.stderr
    lines = result.stdout.splitlines()
    assert lines[0].split() == ["URL", "SOURCE"]
    assert lines[1].split() == ["https://flag.example/", "flag"]


def test_yaml_is_the_default_off_a_terminal(run_registry):
    """Output that is being captured has to be parseable without a flag."""
    result = run_registry("list", "--registry", "https://flag.example/")

    assert result.returncode == 0, result.stderr
    assert yaml.safe_load(result.stdout) == [
        {"url": "https://flag.example/", "source": "flag"},
    ]


def test_a_duplicated_server_is_refused(run_registry):
    """The order is the resolution order, so the same server twice is a mistake."""
    result = run_registry(
        "list",
        "-o",
        "json",
        env={"TT_REGISTRIES": "https://a.example/,https://a.example/"},
    )

    assert result.returncode == 1
    assert "duplicate" in result.stderr


def test_an_empty_entry_is_refused(run_registry):
    """A stray comma is reported rather than quietly shortening the list."""
    result = run_registry(
        "list",
        "-o",
        "json",
        env={"TT_REGISTRIES": "https://a.example/,,https://b.example/"},
    )

    assert result.returncode == 1
    assert "empty registry entry" in result.stderr


def test_an_unknown_format_is_refused(run_registry):
    """-o names one of three formats; anything else is a typo worth reporting."""
    result = run_registry("list", "-o", "csv")

    assert result.returncode == 1
    assert "unknown output format" in result.stderr
