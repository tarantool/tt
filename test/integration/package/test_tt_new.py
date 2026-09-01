"""End-to-end coverage for `tt new`.

The command writes one file, so the tests are about what that file is worth: it
has to be a manifest the rest of the pipeline accepts, and it must never replace
one that is already there.
"""

import json

try:
    import tomllib
except ModuleNotFoundError:  # Python < 3.11, which is what CI runs.
    import tomli as tomllib


def read_manifest(root):
    with (root / "app.manifest.toml").open("rb") as handle:
        return tomllib.load(handle)


def test_new_writes_a_manifest_the_pipeline_accepts(tree, run_tt):
    """The skeleton parses, validates and is readable by another command.

    `tt package deps` is the cheapest proof of that: it parses and validates the
    manifest and would fail on a skeleton that does not hold up.
    """
    root = tree.root

    result = run_tt("new", "-n", "my-app")
    assert result.returncode == 0, result.stderr
    assert "app.manifest.toml" in result.stdout

    parsed = read_manifest(root)
    assert parsed["manifest_version"] == "0.1"
    assert parsed["package"]["name"] == "my-app"
    assert parsed["platform"]["tarantool"].startswith(">=3.")
    assert parsed["dependencies"] == {}

    deps = run_tt("package", "deps", "-o", "json")
    assert deps.returncode == 0, deps.stderr

    report = json.loads(deps.stdout)
    assert report["package"] == parsed["package"]["name"]
    assert report["lock"] == "missing"


def test_new_refuses_a_directory_name_that_is_not_a_package_name(tree, run_tt):
    """The pytest tmpdir is named with underscores, which is the case itself.

    The refusal has to name the flag that fixes it, or the user is left
    renaming a directory to satisfy a tool.
    """
    root = tree.root

    result = run_tt("new")
    assert result.returncode == 1
    assert "is not a package name" in result.stderr
    assert "tt new -n " in result.stderr
    assert not (root / "app.manifest.toml").exists()


def test_new_refuses_to_overwrite(tree, run_tt):
    """A second run is an error, and the existing file is left as it is."""
    root = tree.root

    assert run_tt("new", "-n", "my-app").returncode == 0

    (root / "app.manifest.toml").write_text(
        (root / "app.manifest.toml").read_text() + "\n# hand-written\n",
    )
    before = (root / "app.manifest.toml").read_text()

    result = run_tt("new", "-n", "my-app")
    assert result.returncode == 1
    assert "already exists" in result.stderr
    assert (root / "app.manifest.toml").read_text() == before


def test_new_then_add_declares_a_dependency(tree, run_tt, rock_server):
    """The skeleton is what `tt package add` writes into.

    Going through the real add is what proves the empty `[dependencies]` table
    is a table the editor can splice into, rather than merely valid TOML.
    """
    root = tree.root

    assert run_tt("new", "-n", "my-app").returncode == 0

    # Point the default registry list at the loopback server for this one run,
    # so the resolution behind `add` stays offline.
    manifest = (root / "app.manifest.toml").read_text()
    manifest = manifest.replace(
        "[dependencies]",
        f'[dependencies.stat]\nsource = "registry"\nregistry = "{rock_server}"\nversion = "*"\n',
    )
    (root / "app.manifest.toml").write_text(manifest)

    result = run_tt("package", "add", "stat", ">=0.3.1")
    assert result.returncode == 0, result.stderr

    parsed = read_manifest(root)
    assert parsed["dependencies"]["stat"]["version"] == ">=0.3.1"
