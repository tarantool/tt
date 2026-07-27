import json

import pytest

PREFIXES = ("\u2502   ", "    ")
BRANCHES = ("\u251c\u2500\u2500 ", "\u2514\u2500\u2500 ")


def tree_lines(output: str) -> list[str]:
    """The rendered tree without the scope header."""
    return output.split("\n\n", 1)[1].rstrip("\n").split("\n")


def depth(line: str) -> int:
    """How deep a rendered line sits: 0 for the root, 1 for its children."""
    n = 0
    while n + 4 <= len(line):
        chunk = line[n : n + 4]
        if chunk in PREFIXES:
            n += 4
        elif chunk in BRANCHES:
            return n // 4 + 1
        else:
            break
    return 0


def label(line: str) -> str:
    """A line's own text, with the tree prefix stripped."""
    n = 0
    while n + 4 <= len(line):
        chunk = line[n : n + 4]
        if chunk in PREFIXES:
            n += 4
        elif chunk in BRANCHES:
            return line[n + 4 :]
        else:
            break
    return line


def subtree_of(output: str, prefix: str) -> str:
    """The node whose label starts with `prefix`, plus everything nested under it."""
    lines = tree_lines(output)
    for i, line in enumerate(lines):
        if not label(line).startswith(prefix):
            continue
        own = depth(line)
        block = [line]
        for nxt in lines[i + 1 :]:
            if depth(nxt) <= own:
                break
            block.append(nxt)
        return "\n".join(block)
    raise AssertionError(f"no {prefix!r} node in:\n{output}")


@pytest.mark.usefixtures("tree_fixture")
def test_tree_annotates_every_dependency(run_tt):
    """Each branch says whether the dependency would leave with its package."""
    result = run_tt("package", "list", "--tree", "-o", "table")
    assert result.returncode == 0, result.stderr

    monitoring = subtree_of(result.stdout, "monitoring 2.0.0")
    assert "checks 3.1.0 (shared with alerting, my-app)" in monitoring
    assert "metrics 1.0.0 (only here)" in monitoring
    assert "shared-lib 1.0.0 (installed package)" in monitoring
    assert "shared with monitoring" not in monitoring

    # One tree, rooted at the project's own package, with the guests beneath it.
    roots = [label(line) for line in tree_lines(result.stdout) if depth(line) == 0]
    assert roots == ["my-app 1.2.3 (primary)"]

    assert "(no dependencies)" in subtree_of(result.stdout, "shared-lib 1.0.0 (guest)")


@pytest.mark.usefixtures("tree_fixture")
def test_tree_nests_under_the_requiring_rock(run_tt):
    """A rock hangs off whatever pulled it in, read from the tree manifest.

    The lock cannot say this — it stores the closure flattened, parent discarded —
    but LuaRocks records each installed rock's own requirements in the tree.
    """
    result = run_tt("package", "list", "--tree", "-o", "table")
    assert result.returncode == 0, result.stderr

    metrics = subtree_of(result.stdout, "metrics 1.0.0").split("\n")
    assert len(metrics) == 2
    assert "checks 3.1.0" in label(metrics[1])

    shared_lib = subtree_of(result.stdout, "shared-lib 1.0.0 (installed package)").split("\n")
    assert len(shared_lib) == 2
    assert "luasocket 3.0.4" in label(shared_lib[1])

    # Nothing is left unrooted, so the unknown-parent group does not appear.
    assert "parent not recorded" not in result.stdout


@pytest.mark.usefixtures("shared_tree")
def test_tree_without_manifest_falls_back(run_tt):
    """A tree with no LuaRocks manifest still lists indirect rocks, ungrouped."""
    result = run_tt("package", "list", "--tree", "-o", "table")
    assert result.returncode == 0, result.stderr

    # shared_tree writes no tree manifest, and its packages declare nothing, so
    # every rock is unrooted — and still shown.
    assert "parent not recorded" in result.stdout
    assert "metrics 1.0.0" in result.stdout
    assert "checks 3.1.0" in result.stdout


@pytest.mark.usefixtures("tree_fixture")
def test_tree_in_json(run_tt):
    """`--tree` is not a table-only view: the index travels in machine output."""
    result = run_tt("package", "list", "--tree", "-o", "json")
    assert result.returncode == 0, result.stderr

    deps = json.loads(result.stdout)["rocks"]
    by_name = {dep["name"]: dep for dep in deps}

    assert [dep["name"] for dep in deps] == ["checks", "luasocket", "metrics", "shared-lib"]
    assert by_name["checks"]["owners"] == ["alerting", "monitoring", "my-app"]
    assert by_name["checks"]["shared"] is True
    assert by_name["metrics"]["owners"] == ["monitoring"]
    assert by_name["metrics"]["shared"] is False
    assert by_name["shared-lib"]["installed"] is True

    # A rock nobody declared is owned exactly like a declared one.
    assert by_name["luasocket"]["owners"] == ["monitoring"]
    assert by_name["luasocket"]["shared"] is False

    # The edges travel in the machine output too.
    assert by_name["metrics"]["requires"] == ["checks"]
    assert by_name["shared-lib"]["requires"] == ["luasocket"]
    assert "requires" not in by_name["checks"]


@pytest.mark.usefixtures("shared_tree")
def test_list_without_tree_has_no_index(run_tt):
    """The index is computed only when asked for.

    A package entry still carries its own "dependencies" object; the top-level
    index is a separate key precisely so the two shapes do not share a name.
    """
    result = run_tt("package", "list", "-o", "json")
    assert result.returncode == 0, result.stderr

    payload = json.loads(result.stdout)
    assert "rocks" not in payload
    assert "dependencies" in payload["packages"][1]


def test_tree_empty_scope_falls_back(run_tt, tmp_path):
    """An empty scope says so rather than printing a header with nothing under it."""
    empty = tmp_path / "empty"
    empty.mkdir()

    result = run_tt("package", "list", "--tree", "-o", "table", cwd=empty)
    assert result.returncode == 0, result.stderr

    assert "no packages installed" in result.stdout
    assert "├" not in result.stdout
    assert "└" not in result.stdout


def test_tree_predicts_uninstall_outcome(run_tt, tree_fixture):
    """What the tree says would happen is what uninstall actually does."""
    listed = run_tt("package", "list", "--tree", "-o", "table")
    assert listed.returncode == 0, listed.stderr

    monitoring = subtree_of(listed.stdout, "monitoring 2.0.0")
    assert "metrics 1.0.0 (only here)" in monitoring
    assert "checks 3.1.0 (shared with alerting, my-app)" in monitoring

    removed = run_tt("package", "uninstall", "monitoring", "--yes")
    assert removed.returncode == 0, removed.stderr

    assert "removed unused dependency metrics" in removed.stdout
    assert not tree_fixture.rock_exists("metrics")
    assert tree_fixture.rock_exists("checks")
    assert tree_fixture.rock_exists("shared-lib")
