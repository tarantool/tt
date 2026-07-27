import json

import pytest


def test_uninstall_drops_sole_dependency_keeps_shared(run_tt, shared_tree):
    """The task's headline case, end to end through the CLI.

    Removing `monitoring` takes its files, its metadata and `metrics` (which only
    it held); `checks` survives because `alerting` still holds it.
    """
    result = run_tt("package", "uninstall", "monitoring", "--yes")
    assert result.returncode == 0, result.stderr

    assert not shared_tree.rock_exists("monitoring")
    assert not shared_tree.metadata_exists("monitoring")
    assert not shared_tree.rock_exists("metrics")

    assert shared_tree.rock_exists("checks")
    assert shared_tree.rock_exists("alerting")
    assert shared_tree.metadata_exists("alerting")

    assert "removed unused dependency metrics" in result.stdout
    assert "kept checks (required by alerting)" in result.stdout


@pytest.mark.usefixtures("shared_tree")
def test_uninstall_then_list_is_consistent(run_tt):
    """After a removal, the inventory no longer reports the package."""
    assert run_tt("package", "uninstall", "monitoring", "--yes").returncode == 0

    result = run_tt("package", "list", "-o", "json")
    assert result.returncode == 0, result.stderr

    names = {entry["name"] for entry in json.loads(result.stdout)["packages"]}
    assert names == {"my-app", "alerting"}


def test_uninstall_refuses_primary_package(run_tt, shared_tree):
    """The project's own package is not a guest; its place is the project's call."""
    result = run_tt("package", "uninstall", "my-app", "--yes")
    assert result.returncode == 1
    assert "primary package" in result.stderr

    assert (shared_tree.root / "app.manifest.toml").exists()
    assert shared_tree.rock_exists("my-app")


def test_uninstall_unknown_package(run_tt, shared_tree):
    result = run_tt("package", "uninstall", "nosuch", "--yes")
    assert result.returncode == 1
    assert "not installed" in result.stderr

    assert shared_tree.metadata_exists("monitoring")


@pytest.mark.parametrize(
    "name",
    ["../../etc", "..", "has/slash", "Upper", "manifests", "bin"],
)
def test_uninstall_rejects_bad_package_name(run_tt, shared_tree, name):
    """The name becomes a path, so its shape is checked before any removal."""
    result = run_tt("package", "uninstall", name, "--yes")
    assert result.returncode == 1
    assert "invalid package name" in result.stderr

    assert shared_tree.metadata_exists("monitoring")
    assert shared_tree.rock_exists("metrics")


def test_uninstall_declined_removes_nothing(run_tt, shared_tree):
    """Declining the prompt is a full abort, not a partial removal."""
    result = run_tt("package", "uninstall", "monitoring", stdin="n\n")
    assert result.returncode == 1

    assert shared_tree.metadata_exists("monitoring")
    assert shared_tree.rock_exists("monitoring")
    assert shared_tree.rock_exists("metrics")


def test_uninstall_accepted_at_prompt(run_tt, shared_tree):
    """Accepting removes the package, and the prompt named what goes with it."""
    result = run_tt("package", "uninstall", "monitoring", stdin="y\n")
    assert result.returncode == 0, result.stderr

    prompt = result.stdout + result.stderr
    assert "metrics" in prompt
    assert not shared_tree.metadata_exists("monitoring")


def test_uninstall_is_repeatable(run_tt, shared_tree):
    """A second run reports a clean miss rather than crashing on a half-state."""
    assert run_tt("package", "uninstall", "monitoring", "--yes").returncode == 0

    result = run_tt("package", "uninstall", "monitoring", "--yes")
    assert result.returncode == 1
    assert "not installed" in result.stderr

    assert shared_tree.rock_exists("checks")


def test_uninstall_keeps_dependency_held_by_primary(run_tt, tree):
    """The project's own package counts as an owner of what its lock pins."""
    tree.install_primary("my-app", "1.0.0", {"metrics": "1.0.0"})
    tree.install_guest("monitoring", "2.0.0", {"metrics": "1.0.0"})

    result = run_tt("package", "uninstall", "monitoring", "--yes")
    assert result.returncode == 0, result.stderr

    assert tree.rock_exists("metrics")
    assert "kept metrics (required by my-app)" in result.stdout


def test_uninstall_reports_why_a_dependency_was_kept(run_tt, tree_fixture):
    """The two reasons a rock survives are reported apart, not conflated.

    `checks` stays because other packages still pin it; `shared-lib` stays
    because it is a package in its own right, which is a different thing and
    would read wrong as "required by shared-lib".
    """
    result = run_tt("package", "uninstall", "monitoring", "--yes")
    assert result.returncode == 0, result.stderr

    assert "kept checks (required by alerting, my-app)" in result.stdout
    assert "kept shared-lib (installed as a package in its own right)" in result.stdout
    assert "required by shared-lib" not in result.stdout

    assert tree_fixture.rock_exists("checks")
    assert tree_fixture.rock_exists("shared-lib")


def test_uninstall_removes_transitive_dependency(run_tt, tree_fixture):
    """A rock no manifest ever named is still removed when its last owner goes.

    A lock records the whole transitive closure, so ownership covers every rock a
    package caused to be installed, at any depth.
    """
    result = run_tt("package", "uninstall", "monitoring", "--yes")
    assert result.returncode == 0, result.stderr

    assert "removed unused dependency luasocket" in result.stdout
    assert not tree_fixture.rock_exists("luasocket")


def test_uninstall_rejects_unknown_scope(run_tt, shared_tree):
    result = run_tt("package", "uninstall", "monitoring", "--scope", "global", "--yes")
    assert result.returncode == 1
    assert "unknown scope" in result.stderr

    assert shared_tree.metadata_exists("monitoring")
