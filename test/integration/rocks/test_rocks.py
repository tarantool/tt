import http.server
import os
import platform
import re
import shutil
import tempfile
import threading

import pytest

from utils import create_lua_config, run_command_and_get_output

# A manifest whose [platform].registries names the rock repository in the
# repo directory next to it.
MANIFEST_WITH_REPO = """manifest_version = '0.1'

[package]
name = 'rocks-test'

[platform]
registries = ['repo']
"""


class RemoteTrap(http.server.BaseHTTPRequestHandler):
    """The proxy of every request a command makes over the network: it records
    the request and answers 502, so no remote server is reached."""

    def answer(self):
        self.server.requests.append(f"{self.command} {self.path}")
        self.send_error(502, "no network in this test")

    do_GET = do_HEAD = do_POST = do_PUT = do_CONNECT = answer

    def log_message(self, format, *args):
        pass


@pytest.fixture
def offline(tmp_path):
    """The environment of a command cut off from the network, and the
    requests it made, which the proxy the environment names answered with
    502. tt and the downloaders LuaRocks runs, wget and curl, send every
    request to it; the LuaRocks cache is that of the test, so that no manifest
    cached a moment ago stands in for one the command asks a remote for."""
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), RemoteTrap)
    server.requests = []
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    proxy = f"http://127.0.0.1:{server.server_address[1]}"
    env = dict(os.environ, XDG_CACHE_HOME=str(tmp_path / "cache"))
    for var in ("http_proxy", "https_proxy", "all_proxy"):
        env[var] = env[var.upper()] = proxy
    env.pop("no_proxy", None)
    env.pop("NO_PROXY", None)

    yield env, server.requests

    server.shutdown()
    server.server_close()
    thread.join()


# ##### #
# Tests #
# ##### #


def test_rocks_module(tt_cmd, tmp_path):
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "help"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert re.search(rf"^Usage: {re.escape(tt_cmd.name)} rocks", output)

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "search", "queue"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert "Rockspecs and source rocks:\n" in output

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "queue"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert os.path.isfile(f"{tmp_path}/.rocks/share/tarantool/queue/init.lua")

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "doc", "queue", "--list"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert "Documentation files for queue" in output

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "pack", "queue"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    if shutil.which("zip") is None:
        # LuaRocks packs a rock with zip and, without it, says only "Failed
        # packing": tt says what is missing.
        assert rc == 1
        assert "Failed packing" in output
        assert "LuaRocks packs a rock with the zip program, which is not found in PATH" in output
    else:
        assert rc == 0
        assert re.search("Packed: .*queue-.*[.]rock", output)
        rock_file = output.split("Packed: ")[1].strip()
        assert os.path.isfile(rock_file)

        rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "unpack", rock_file],
            cwd=tmp_path,
            env=dict(os.environ, PWD=tmp_path),
        )
        assert rc == 0

        rock_dir = ""
        rock_dir_items = rock_file.split(".")
        for i in range((len(rock_dir_items) - 2)):
            rock_dir += rock_dir_items[i] + "."
        rock_dir = rock_dir[:-1]

        assert os.path.isdir(rock_dir)

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "remove", "queue"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert "Removal successful.\n" in output

    test_app_path = os.path.join(os.path.dirname(__file__), "files", "testapp-scm-1.rockspec")
    shutil.copy(test_app_path, tmp_path)
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "make", "testapp-scm-1.rockspec"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert "testapp scm-1 is now installed" in output

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "--verbose", "list"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert "fs.current_dir()\n" in output


NATIVE_ROCKSPEC = """package = "native"
version = "scm-1"
source = {url = "."}
build = {type = "builtin", modules = {native = "native.c"}}
"""

# The C part of the native rock: it includes the Lua headers and module.h,
# all of which Tarantool installs into include/tarantool.
NATIVE_C = """#include <lua.h>
#include <lauxlib.h>
#include <module.h>

static int hello(lua_State *L)
{
    lua_pushstring(L, "native");
    return 1;
}

int luaopen_native(lua_State *L)
{
    lua_newtable(L);
    lua_pushcfunction(L, hello);
    lua_setfield(L, -2, "hello");
    return 1;
}
"""


def test_rocks_make_native_rock_with_project_headers(tt_cmd, tmp_path):
    # The tarantool of a project is laid out the way tt binary install lays
    # it out: an installation under .tarantool/versions/, selected by the
    # .tarantool/bin/tarantool and .tarantool/include/tarantool links. tt
    # rocks builds a rock with a C part against those headers.
    tarantool = shutil.which("tarantool")
    if tarantool is None or shutil.which("cc") is None:
        pytest.skip("tarantool or a C compiler is not found")
    headers = os.path.join(os.path.dirname(os.path.dirname(tarantool)), "include", "tarantool")
    if not os.path.isfile(os.path.join(headers, "module.h")):
        pytest.skip("the headers of tarantool are not found next to it")

    with open(tmp_path / "app.manifest.toml", "w") as f:
        f.write("manifest_version = '0.1'\n\n[package]\nname = 'native'\n")

    root = tmp_path / ".tarantool"
    installation = root / "versions" / "tarantool" / "tarantool_3.8.1"
    os.makedirs(installation / "include")
    os.symlink(tarantool, installation / "tarantool")
    os.symlink(headers, installation / "include" / "tarantool")
    os.makedirs(root / "bin")
    os.symlink(installation / "tarantool", root / "bin" / "tarantool")
    os.makedirs(root / "include")
    os.symlink(installation / "include" / "tarantool", root / "include" / "tarantool")

    with open(tmp_path / "native-scm-1.rockspec", "w") as f:
        f.write(NATIVE_ROCKSPEC)
    with open(tmp_path / "native.c", "w") as f:
        f.write(NATIVE_C)

    env = {k: v for k, v in os.environ.items() if k != "TT_CLI_TARANTOOL_PREFIX"}
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "make"],
        cwd=tmp_path,
        env=dict(env, PWD=str(tmp_path)),
    )
    assert rc == 0
    assert f"-I{root / 'include' / 'tarantool'}" in output
    assert os.path.isfile(tmp_path / ".rocks" / "lib" / "tarantool" / "native.so")


def test_rocks_admin_module(tt_cmd, tmp_path):
    repo_path = os.path.join(tmp_path, "rocks_repo")
    os.mkdir(repo_path)

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "admin", "make_manifest", repo_path],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert os.path.isfile(f"{tmp_path}/rocks_repo/index.html")
    assert os.path.isfile(f"{tmp_path}/rocks_repo/manifest")

    test_app_path = os.path.join(os.path.dirname(__file__), "files", "testapp-scm-1.rockspec")
    shutil.copy(test_app_path, tmp_path)
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "admin", "add", "testapp-scm-1.rockspec", "--server", repo_path],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert os.path.isfile(f"{tmp_path}/rocks_repo/testapp-scm-1.rockspec")

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "admin", "remove", "testapp-scm-1.rockspec", "--server", repo_path],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert not os.path.exists(f"{tmp_path}/rocks_repo/testapp-scm-1.rockspec")


def test_rocks_install_remote(tt_cmd, tmp_path):
    # The CMakeLists.txt of stat asks for a CMake older than 3.5, with which
    # CMake 4 has dropped compatibility unless told the policy version.
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "stat"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path, CMAKE_POLICY_VERSION_MINIMUM="3.5"),
    )
    assert rc == 0
    assert "Installing http://rocks.tarantool.org/stat" in output


def test_rocks_install_local(tt_cmd, tmp_path, offline):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    # The rock servers of tt registry are the servers of tt rocks: here the
    # ones [platform].registries of the manifest names.
    with open(tmp_path / "app.manifest.toml", "w") as f:
        f.write(MANIFEST_WITH_REPO)

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"), os.path.join(tmp_path, "repo"))

    env, requests = offline
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "stat"],
        cwd=tmp_path,
        env=dict(env, PWD=tmp_path),
    )
    assert rc == 0, output
    assert f"Installing {tmp_path}/repo/stat-0.3.2-1.all.rock" in output
    assert requests == []


def test_rocks_install_local_if_network_is_up(tt_cmd, tmp_path):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"), os.path.join(tmp_path, "repo"))

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "--only-server=repo", "install", "stat"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0
    assert "Installing repo/stat-0.3.2-1.all.rock" in output
    assert "stat 0.3.2-1 is now installed in " + os.path.join(tmp_path, ".rocks") in output


def test_rocks_install_local_specific_version(tt_cmd, tmp_path):
    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"), os.path.join(tmp_path, "repo"))

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "stat", "0.3.1-1"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path, TT_REGISTRIES="repo"),
    )
    assert rc == 0
    assert f"Installing {tmp_path}/repo/stat-0.3.1-1.all.rock" in output


def write_index_only_server(path):
    """A directory rock server with nothing but a tt-rocks-index.json, which
    tt package reads and LuaRocks does not."""
    os.mkdir(path)
    with open(os.path.join(path, "tt-rocks-index.json"), "w") as f:
        f.write('{"format": 1, "repository": {}}\n')


def test_rocks_leaves_out_an_index_only_server(tt_cmd, tmp_path):
    """A configured server that serves only a rock index is left out with a
    warning, as is one TT_CLI_REPO_ROCKS names, and the rest are queried."""
    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"), os.path.join(tmp_path, "repo"))
    write_index_only_server(tmp_path / "index")

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "stat", "0.3.1-1"],
        cwd=tmp_path,
        env=dict(
            os.environ,
            PWD=tmp_path,
            TT_REGISTRIES="index,repo",
            TT_CLI_REPO_ROCKS=f"{tmp_path}/index",
        ),
    )
    assert rc == 0, output
    assert output.count("serves only a rock index (tt-rocks-index.json)") == 2
    assert f"tt rocks leaves out {tmp_path}/index:" in output
    assert "install from it with tt package" in output
    assert f"Installing {tmp_path}/repo/stat-0.3.1-1.all.rock" in output


@pytest.mark.parametrize("option", ["--server", "--only-server"])
def test_rocks_refuses_an_index_only_server_named_on_the_command_line(tt_cmd, tmp_path, option):
    """A server the user names for LuaRocks is not left out but refused: what
    it would install would come from elsewhere."""
    write_index_only_server(tmp_path / "index")

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "stat", f"{option}={tmp_path}/index"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 1, output
    assert "the rock server serves only a rock index" in output
    assert f"{tmp_path}/index has nothing but tt-rocks-index.json" in output
    assert not (tmp_path / ".rocks").exists()


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_rock_install_without_system_tarantool(tt_cmd, tmpdir_with_tarantool):
    rocks_cmd = [tt_cmd, "rocks", "install", "pg", "2.0.1"]
    rc, output = run_command_and_get_output(
        rocks_cmd,
        cwd=tmpdir_with_tarantool,
        env=dict(os.environ, PWD=tmpdir_with_tarantool.as_posix()),
    )
    assert rc == 0, output

    assert os.path.exists(os.path.join(tmpdir_with_tarantool, ".rocks", "lib", "tarantool", "pg"))


def test_rocks_install_from_dir_with_no_repo(tt_cmd, tmp_path, offline):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"), os.path.join(tmp_path, "repo"))

    os.mkdir(os.path.join(tmp_path, "subdir"))

    # A relative registry is taken from the working directory.
    env, requests = offline
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "stat"],
        cwd=os.path.join(tmp_path, "subdir"),
        env=dict(env, PWD=os.path.join(tmp_path, "subdir"), TT_REGISTRIES="../repo"),
    )
    assert rc == 0, output
    assert f"Installing {tmp_path}/repo/stat-0.3.2-1.all.rock" in output
    assert f"stat 0.3.2-1 is now installed in {tmp_path / 'subdir' / '.rocks'}" in output
    assert os.path.exists(os.path.join(tmp_path, "subdir", ".rocks"))
    assert requests == []


def test_rocks_install_from_env_var_repo(tt_cmd, tmp_path, offline):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"), os.path.join(tmp_path, "repo"))

    os.mkdir(os.path.join(tmp_path, "subdir"))

    # Without the env var, LuaRocks asks its remote servers, which it cannot
    # reach, and finds nothing.
    env, requests = offline
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "stat"],
        cwd=os.path.join(tmp_path, "subdir"),
        env=dict(env, PWD=os.path.join(tmp_path, "subdir")),
    )

    assert rc == 1
    assert "Error: No results matching query" in output
    assert requests != []

    # With the env var for the rock repo directory, the rock comes from it
    # while the remote servers stay out of reach.
    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "stat"],
        cwd=tmp_path / "subdir",
        env=dict(
            env,
            PWD=os.path.join(tmp_path, "subdir"),
            TT_CLI_REPO_ROCKS=f"{tmp_path}/repo",
        ),
    )
    assert rc == 0, output
    assert f"Installing {tmp_path}/repo/stat-0.3.2-1.all.rock" in output
    assert f"stat 0.3.2-1 is now installed in {tmp_path / 'subdir' / '.rocks'}" in output
    assert os.path.exists(os.path.join(tmp_path, "subdir", ".rocks"))


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_rock_install_with_non_system_tarantool_in_path(tt_cmd, tarantool_prefix):
    with tempfile.TemporaryDirectory() as tmp_path:
        # Rocks install must fail due to not found tarantool headers.
        rocks_cmd = [tt_cmd, "rocks", "install", "crud", "1.1.1-1"]
        rc, output = run_command_and_get_output(
            rocks_cmd,
            cwd=tmp_path,
            env=dict(
                os.environ,
                PWD=tmp_path,
                PATH=os.path.join(tarantool_prefix, "bin") + ":" + os.environ["PATH"],
            ),
        )

        assert rc == 1  # Tarantool headers are not found.
        assert re.search("Error: Failed finding Lua header files", output)

        # Set env var to find tarantool headers.
        rocks_cmd = [tt_cmd, "rocks", "install", "crud", "1.1.1-1"]
        rc, output = run_command_and_get_output(
            rocks_cmd,
            cwd=tmp_path,
            env=dict(
                os.environ,
                PWD=tmp_path,
                PATH=os.path.join(tarantool_prefix, "bin") + ":" + os.environ["PATH"],
                TT_CLI_TARANTOOL_PREFIX=str(tarantool_prefix),
            ),
        )

        assert rc == 0
        assert "crud 1.1.1-1 is now installed" in output

        assert os.path.exists(os.path.join(tmp_path, ".rocks", "share", "tarantool", "crud"))


@pytest.mark.docker
def test_rocks_with_hardcoded(tt_cmd, tmp_path):
    if shutil.which("docker") is None:
        pytest.skip("docker is not installed on this system")

    rc, _ = run_command_and_get_output(["docker", "ps"])
    assert rc == 0

    binaries_path = tmp_path / "binaries"
    os.mkdir(binaries_path)
    tarantool_message = (
        "Tarantool 3.3.0-entrypoint-31-g9eb397499\n"
        + "Target: Linux-x86_64-RelWithDebInfo\n"
        + "Build options: cmake . -DCMAKE_INSTALL_PREFIX=/usr/local -DENABLE_BACKTRACE=TRUE\n"
        + "Compiler: GNU-13.2.0\n"
    )
    tarantool_script = binaries_path / "tarantool"
    with open(tarantool_script, "w") as f:
        f.write(f'#!/bin/sh\necho "{tarantool_message}"')
    os.chmod(tarantool_script, 0o777)

    etc_luarocks_path = tmp_path / "luarocks"
    os.mkdir(etc_luarocks_path)
    create_lua_config(etc_luarocks_path)

    shutil.copy(tt_cmd, tmp_path / "tt")
    os.chmod(tmp_path / "tt", 0o755)

    base_dir = tmp_path

    rc, output = run_command_and_get_output(
        [
            "docker",
            "run",
            "--rm",
            "-v",
            "{0}:/usr/src/".format(base_dir),
            "-v",
            "{0}:/etc/luarocks".format(etc_luarocks_path),
            "-w",
            "/usr/src",
            "ubuntu",
            "bash",
            "-c",
            ("export PATH=/usr/src/binaries:$PATH && ./tt rocks config"),
        ],
    )
    assert rc == 0
    assert re.search('lua_interpreter = "tarantool"', output)
    assert re.search("IS_LUA_CONFIG_USED = true", output)


# The modules LuaRocks looks for on its own, under the names a repository
# could plant them: optional platform and library modules, probed by every
# command, and an external command, which the parser of every command used
# to load.
PLANTED_MODULES = [
    "luarocks/fs/linux.lua",
    "lfs.lua",
    "socket/http.lua",
    "luarocks/cmd/external/planted.lua",
]


def plant_modules(directory, marker):
    for module in PLANTED_MODULES:
        path = directory / module
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            f'local f = io.open("{marker}", "a") f:write("{module}\\n") f:close()\n'
            + "return {}\n",
        )


@pytest.mark.parametrize("place", ["cwd", "lua_path", "rocks_tree"])
def test_rocks_runs_no_lua_of_the_directory(tt_cmd, tmp_path, place):
    project = tmp_path / "project"
    project.mkdir()
    marker = tmp_path / "loaded.txt"
    env = dict(os.environ, PWD=str(project))
    env.pop("LUA_PATH", None)

    if place == "cwd":
        plant_modules(project, marker)
    elif place == "lua_path":
        plant_modules(tmp_path / "lua_path", marker)
        env["LUA_PATH"] = f"{tmp_path / 'lua_path'}/?.lua"
    else:
        plant_modules(project / ".rocks" / "share" / "tarantool", marker)

    for args in (["list"], ["path"], ["help"]):
        rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", *args],
            cwd=project,
            env=env,
        )
        assert rc == 0, output

    assert not marker.exists(), marker.read_text()


# The manifest of a tt package of one Lua module, which tt packs as rocks.
LUA_PACKAGE_MANIFEST = """manifest_version = '0.1'

[package]
name = 'lualib'
license_files = ['LICENSE']
repository = 'https://github.com/example/lualib'

[platform]
tarantool = '>=2.0.0'
tt = '>=3.0.0-alpha'

[components.lualib]
path = 'lualib'
"""


def test_rocks_refuses_the_source_rock_of_a_tt_package(tt_cmd, tmp_path):
    """LuaRocks builds nothing of the source rock of a tt package, whose
    rockspec has build type none: tt rocks removes what it installed and
    fails, rather than say a rock with no module is installed. The binary rock
    of the same version installs."""
    package = tmp_path / "lualib"
    (package / "lualib").mkdir(parents=True)
    (package / "app.manifest.toml").write_text(LUA_PACKAGE_MANIFEST)
    (package / "lualib" / "init.lua").write_text("return {answer = 42}\n")
    (package / "LICENSE").write_text("license\n")
    (package / "VERSION").write_text("1.0.0\n")

    env = dict(os.environ, PWD=str(package))
    for fmt, out in (("rock-src", "src"), ("rock", "bin")):
        rc, output = run_command_and_get_output(
            [tt_cmd, "package", "pack", "--format", fmt, "--out", str(tmp_path / out)],
            cwd=package,
            env=env,
        )
        assert rc == 0, output

    consumer = tmp_path / "consumer"
    consumer.mkdir()
    env = dict(os.environ, PWD=str(consumer))
    installed = consumer / ".rocks" / "share" / "tarantool" / "rocks" / "lualib"

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "lualib", "--only-server", str(tmp_path / "src")],
        cwd=consumer,
        env=env,
    )
    assert rc == 1, output
    # LuaRocks says the rock is installed before tt looks; tt's error, the
    # last word, says it is not.
    said = output.index("lualib 1.0.0-1 is now installed")
    refused = output.index("LuaRocks built nothing of the source rock of a tt package")
    assert said < refused, output
    assert (
        "lualib 1.0.0-1 is not installed, whatever LuaRocks printed above; "
        "tt removed it from the tree again" in output
    )
    assert "tt package build" in output
    assert not installed.exists()
    # The tree the install made goes with the rock, its manifest and lock too.
    assert not (consumer / ".rocks").exists()

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "install", "lualib", "--only-server", str(tmp_path / "bin")],
        cwd=consumer,
        env=env,
    )
    assert rc == 0, output
    assert (consumer / ".rocks" / "share" / "tarantool" / "lualib" / "init.lua").is_file()
