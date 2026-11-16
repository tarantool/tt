"""A project of one instance, iproto:inst, that the tests reach over iproto by
the address its config gives (iproto.advertise.client, else iproto.listen),
and that records the local address of every iproto session it accepts: the
address tells which listener a client came through."""

import contextlib
import json
import os
import shutil
import socket
import tempfile
from pathlib import Path

import yaml
from integration.replicaset.replicaset_helpers import (
    start_template_project,
    stop_template_project,
)

from utils import run_command_and_get_output, short_tmp_dir

APP_NAME = "iproto"
INSTANCE = "inst"
TARGET = f"{APP_NAME}:{INSTANCE}"

# The user with the rights, and the flags that log in as them.
USER = "client"
PASSWORD = "secret"
USER_FLAGS = ["-u", USER, "-p", PASSWORD]

# A user of the environment that does not exist: a login as it fails, so an
# instance logged in to as it would refuse the connection.
ENV_USER = "nobody"
ENV_CREDENTIALS = {"TT_CLI_USERNAME": ENV_USER, "TT_CLI_PASSWORD": "wrong"}

# The app records the local address of each iproto session: the port of a TCP
# listener, or the path of a unix socket. socket.from_fd() wraps the socket of
# the session for its name, and detach() leaves the socket open.
APP_LUA = """\
local socket = require('socket')

rawset(_G, 'iproto_listeners', {})

box.session.on_connect(function()
    if box.session.type() ~= 'binary' then
        return
    end
    local sock = socket.from_fd(box.session.fd())
    local name = sock:name()
    sock:detach()
    table.insert(iproto_listeners, tostring(name.port))
end)
"""


def write_iproto_project(workdir, iproto):
    """Lay the project APP_NAME out in workdir with iproto, the iproto section
    of its config, and return its directory."""
    app_dir = Path(workdir) / APP_NAME
    templates = app_dir / ".templates" / "default"
    templates.mkdir(parents=True)
    (app_dir / "app.lua").write_text(APP_LUA)
    (templates / "instances.yaml").write_text(f"{INSTANCE}:\n")
    config = {
        "credentials": {"users": {USER: {"password": PASSWORD, "roles": ["super"]}}},
        "iproto": iproto,
        "app": {"file": "app.lua"},
        "groups": {"g": {"replicasets": {"r": {"instances": {INSTANCE: {}}}}}},
    }
    (templates / "config.yaml").write_text(yaml.safe_dump(config))
    return app_dir


@contextlib.contextmanager
def running_iproto_project(tt_cmd, iproto):
    """Run the project with iproto, the iproto section of its config, and yield
    its directory. The sockets of the instance are under the project, so it
    is laid out in a short directory."""
    workdir = tempfile.mkdtemp(dir=short_tmp_dir())
    app_dir = write_iproto_project(workdir, iproto)
    try:
        start_template_project(tt_cmd, app_dir, [INSTANCE])
        yield app_dir
    finally:
        stop_template_project(tt_cmd, app_dir)
        shutil.rmtree(workdir)


def iproto_listeners(tt_cmd, app_dir):
    """The listeners the iproto sessions of the instance came through, read
    over its console: ports as strings, or socket paths."""
    rc, out = run_command_and_get_output(
        [tt_cmd, "connect", TARGET, "-f-"],
        cwd=app_dir,
        input="require('json').encode(iproto_listeners)",
    )
    assert rc == 0, out
    return json.loads(yaml.safe_load(out)[0])


def env_with(**variables):
    """The environment of the test with variables set."""
    return dict(os.environ, **variables)


def has_ipv6_loopback():
    """Whether ::1 can be bound on this host."""
    if not socket.has_ipv6:
        return False
    try:
        with socket.socket(socket.AF_INET6, socket.SOCK_STREAM) as sock:
            sock.bind(("::1", 0))
    except OSError:
        return False
    return True
