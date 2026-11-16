"""tt package install of an archive packed --without-deps into a scope that
has no tarantool for it: the install succeeds and warns that the application
cannot start yet, naming [platform].tarantool and the tt binary install
command, rather than leave it to the first tt instance start."""

import pytest
from integration.scopes.scopes_helpers import (
    TARANTOOL_CONSTRAINT,
    Scope,
    install_decoy_tarantool,
    make_project,
    pack,
)


@pytest.mark.parametrize("scope_name", ["user", "system"])
def test_install_warns_without_tarantool(tt_cmd, tmp_path, scope_name):
    archive = pack(tt_cmd, make_project(tmp_path / "project", "1.0.0"))
    cwd = tmp_path / "cwd"
    cwd.mkdir()
    scope = Scope(tt_cmd, scope_name, tmp_path / scope_name, cwd)

    # 4.0.0 does not satisfy the constraint of myapp.
    install_decoy_tarantool(scope.root, "4.0.0")

    rc, out, err = scope.tt("package", "install", "--scope", scope_name, str(archive))

    assert rc == 0, err
    assert f"installed myapp 1.0.0 into {scope_name} scope" in out
    assert "myapp 1.0.0 cannot start yet: tarantool is not installed in the" in err
    assert f'"{TARANTOOL_CONSTRAINT}" of [platform].tarantool' in err
    assert f"tt binary install tarantool <version> --scope {scope_name}" in err


def test_install_does_not_warn_with_tarantool(tt_cmd, tmp_path):
    archive = pack(tt_cmd, make_project(tmp_path / "project", "1.0.0"))
    cwd = tmp_path / "cwd"
    cwd.mkdir()
    scope = Scope(tt_cmd, "user", tmp_path / "user", cwd)

    # Never run: the install only looks for it.
    install_decoy_tarantool(scope.root, "3.8.1")

    rc, out, err = scope.tt("package", "install", "--scope", "user", str(archive))

    assert rc == 0, err
    assert "installed myapp 1.0.0 into user scope" in out
    assert "cannot start yet" not in err
