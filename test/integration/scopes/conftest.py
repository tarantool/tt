import platform
import shutil
import tempfile
from pathlib import Path

import pytest
from integration.scopes.scopes_helpers import Scope

# The prefix of the directory of the scope roots of a test; tempfile.mkdtemp
# adds eight random characters to it.
ROOTS_PREFIX = "tt-scopes-"

# The longest console socket of an instance the tests install, in the
# directory of the scope roots: <root>/var/run/<app>/<inst>/tarantool.control.
LONGEST_SOCKET = Path("system", "var", "run", "myapp", "host-storage", "tarantool.control")


def roots_base():
    """Return the directory to make the scope roots of a test in: the
    temporary directory, or /tmp when the longest socket under the former
    does not fit a unix socket address, or None when it fits under neither.

    An installed instance binds its console socket by the full path, since it
    is outside the working directory of tarantool, and tt refuses a path too
    long to bind. A TMPDIR of some depth leaves too little of the address for
    it, and so does the default temporary directory of macOS, under
    /var/folders."""
    # sun_path, the terminating NUL included.
    limit = 104 if platform.system() == "Darwin" else 108
    for base in (tempfile.gettempdir(), "/tmp"):
        socket = Path(base) / (ROOTS_PREFIX + "x" * 8) / LONGEST_SOCKET
        if len(str(socket)) < limit:
            return base
    return None


@pytest.fixture
def roots():
    """A directory for the scope roots of a test, short enough for the
    sockets of the instances (roots_base)."""
    base = roots_base()
    if base is None:
        pytest.skip("no temporary directory is short enough for the sockets of the scope roots")
    path = Path(tempfile.mkdtemp(prefix=ROOTS_PREFIX, dir=base))
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
