import shutil
import tempfile
from pathlib import Path

import pytest
from integration.scopes.scopes_helpers import Scope


@pytest.fixture
def roots():
    """A directory for the scope roots of a test, short enough for the
    sockets of the instances: an installed instance binds its console socket
    by the full path, <root>/var/run/<app>/<inst>/tarantool.control, and a
    root under tmp_path, deep in the base directory of pytest, leaves too
    little of a unix socket address for it."""
    path = Path(tempfile.mkdtemp(prefix="tt-scopes-"))
    (path / "cwd").mkdir()
    yield path
    shutil.rmtree(path, ignore_errors=True)


def make_scope(tt_cmd, name, roots, request):
    scope = Scope(tt_cmd, name, roots / name, roots / "cwd")
    request.addfinalizer(scope.cleanup)
    return scope


@pytest.fixture
def user_scope(tt_cmd, roots, request):
    """The user scope, with a root of the test."""
    return make_scope(tt_cmd, "user", roots, request)


@pytest.fixture
def system_scope(tt_cmd, roots, request):
    """The system scope, with a root of the test."""
    return make_scope(tt_cmd, "system", roots, request)
