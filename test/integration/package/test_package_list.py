import json

import pytest
import yaml


@pytest.mark.usefixtures("shared_tree")
def test_list_shows_primary_and_guests(run_tt):
    """The task's headline case: primary plus two guests, all three listed."""
    result = run_tt("package", "list", "-o", "table")
    assert result.returncode == 0, result.stderr

    lines = result.stdout.strip().splitlines()
    assert lines[0].split() == ["NAME", "VERSION", "ORIGIN", "DESCRIPTION"]
    assert len(lines) == 4

    rows = {line.split()[0]: line for line in lines[1:]}
    assert set(rows) == {"my-app", "monitoring", "alerting"}
    assert "primary" in rows["my-app"]
    assert "1.2.3" in rows["my-app"]
    assert "guest" in rows["monitoring"]
    assert "guest" in rows["alerting"]


def test_list_json_is_valid(run_tt, shared_tree):
    """`-o json` parses, and carries the dependency pins read off disk."""
    result = run_tt("package", "list", "-o", "json")
    assert result.returncode == 0, result.stderr

    payload = json.loads(result.stdout)
    assert payload["scope"] == "project"
    assert payload["root"] == str(shared_tree.root)

    by_name = {entry["name"]: entry for entry in payload["packages"]}
    assert set(by_name) == {"my-app", "monitoring", "alerting"}
    assert by_name["my-app"]["primary"] is True
    assert by_name["monitoring"]["primary"] is False
    assert by_name["monitoring"]["dependencies"] == {"metrics": "1.0.0", "checks": "3.1.0"}
    assert by_name["alerting"]["dependencies"] == {"checks": "3.1.0"}


@pytest.mark.usefixtures("shared_tree")
def test_list_defaults_to_yaml_when_piped(run_tt):
    """Captured output is not a terminal, so the default format is YAML.

    This is the convention that makes `tt package list | ...` parseable without
    the caller having to remember `-o`.
    """
    result = run_tt("package", "list")
    assert result.returncode == 0, result.stderr

    payload = yaml.safe_load(result.stdout)
    assert payload["scope"] == "project"
    assert {entry["name"] for entry in payload["packages"]} == {
        "my-app",
        "monitoring",
        "alerting",
    }


@pytest.mark.usefixtures("tree")
def test_list_empty_project(run_tt):
    """A project nothing was installed into is an empty listing, not an error."""
    result = run_tt("package", "list", "-o", "table")
    assert result.returncode == 0, result.stderr
    assert "no packages installed" in result.stdout

    result = run_tt("package", "list", "-o", "json")
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout)["packages"] == []


@pytest.mark.usefixtures("shared_tree")
def test_list_rejects_unknown_format(run_tt):
    """A mistyped -o is exit 1, not a silent fallback."""
    result = run_tt("package", "list", "-o", "xml")
    assert result.returncode == 1
    assert "unknown output format" in result.stderr


@pytest.mark.usefixtures("shared_tree")
def test_list_rejects_unknown_scope(run_tt):
    result = run_tt("package", "list", "--scope", "global")
    assert result.returncode == 1
    assert "unknown scope" in result.stderr


def test_list_tolerates_broken_metadata(run_tt, shared_tree):
    """One corrupt metadata directory must not take the whole inventory down."""
    broken = shared_tree.manifests / "broken"
    broken.mkdir(parents=True, exist_ok=True)
    (broken / "manifest.toml").write_text("this is not = valid toml [")

    result = run_tt("package", "list", "-o", "json")
    assert result.returncode == 0, result.stderr

    names = {entry["name"] for entry in json.loads(result.stdout)["packages"]}
    assert names == {"my-app", "monitoring", "alerting"}
