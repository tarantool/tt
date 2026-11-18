"""tt package build and pack --target: a build for another platform than the
machine tt runs on.

A cargo module is built for the other architecture of Linux, which needs
cargo, the Rust target of that architecture and zig, which links it: the
test is skipped without them. The refusals need no toolchain at all.
"""

import os
import shutil
import struct
import subprocess
from pathlib import Path

import pytest

MANIFEST = """manifest_version = '0.1'

[package]
name = 'crossapp'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[components.app]
path = '.'
include = ['*.lua']
"""

CARGO_COMPONENT = """
[components.fastrs]
path = 'fastrs'

[components.fastrs.build]
backend = 'cargo'
module = 'fastrs'
profile = 'dev'
"""

C_COMPONENT = """
[components.cmod]
path = 'cmod'

[components.cmod.build]
backend = 'c'
module = 'cmod'
sources = ['cmod.c']
"""

CARGO_TOML = """[package]
name = "fastrs"
version = "0.1.0"
edition = "2021"

[dependencies]
"""

LIB_RS = """use std::os::raw::{c_int, c_void};

extern "C" {
    fn lua_pushinteger(l: *mut c_void, n: isize);
}

#[no_mangle]
pub unsafe extern "C" fn luaopen_crossapp_fastrs(l: *mut c_void) -> c_int {
    lua_pushinteger(l, 42);
    1
}
"""

# The other architecture of the machine: its GOARCH, the architecture of
# its Rust target, of its rock and the ELF machine of its files.
OTHER = {
    "x86_64": ("arm64", "aarch64", 183),
    "aarch64": ("amd64", "x86_64", 62),
}.get(os.uname().machine)


def run(tt_cmd, cwd: Path, *args: str) -> subprocess.CompletedProcess:
    p = subprocess.run([str(tt_cmd), *args], cwd=cwd, capture_output=True, text=True)
    print(f"$ tt {' '.join(args)}\n{p.stdout}{p.stderr}")
    return p


def make_project(root: Path, *components: str) -> Path:
    root.mkdir(parents=True, exist_ok=True)
    (root / "app.manifest.toml").write_text(MANIFEST + "".join(components))
    (root / "VERSION").write_text("1.0.0\n")
    (root / "init.lua").write_text("return {}\n")
    (root / "registry").mkdir()
    return root


def can_cross_build() -> bool:
    """Whether cargo, rustc with the gnu target of the other architecture of
    this Linux, and zig are in PATH."""
    if os.uname().sysname != "Linux" or OTHER is None:
        return False
    if any(shutil.which(tool) is None for tool in ("cargo", "rustc", "zig")):
        return False
    libdir = subprocess.run(
        ["rustc", "--print", "target-libdir", "--target", f"{OTHER[1]}-unknown-linux-gnu"],
        capture_output=True,
        text=True,
    )
    return libdir.returncode == 0 and Path(libdir.stdout.strip()).is_dir()


def elf_machine(path: Path) -> int:
    """The e_machine of the ELF file at path."""
    head = path.read_bytes()[:20]
    assert head[:4] == b"\x7fELF", path
    return struct.unpack("<H", head[18:20])[0]


@pytest.mark.skipif(not can_cross_build(), reason="no cargo, Rust target or zig to cross build")
def test_cargo_module_for_another_linux(tt_cmd, tmp_path):
    """The module is built for the other architecture, into the tree of the
    target and not into .rocks/; pack makes the rock of the platform and the
    archive --without-deps labeled with it."""
    goarch, arch, machine = OTHER
    platform = f"linux-{goarch}"
    project = make_project(tmp_path / "project", CARGO_COMPONENT)
    (project / "fastrs" / "src").mkdir(parents=True)
    (project / "fastrs" / "Cargo.toml").write_text(CARGO_TOML)
    (project / "fastrs" / "src" / "lib.rs").write_text(LIB_RS)
    subprocess.run(["cargo", "generate-lockfile", "--offline"], cwd=project / "fastrs", check=True)
    registry = str(project / "registry")

    result = run(
        tt_cmd,
        project,
        "package",
        "build",
        "--target",
        f"linux/{goarch}",
        "--registry",
        registry,
    )
    assert result.returncode == 0, result.stderr

    tree = project / "_build" / "target" / platform / "tree"
    module = tree / "lib" / "tarantool" / "crossapp" / "fastrs.so"
    assert elf_machine(module) == machine
    assert not (project / ".rocks" / "lib" / "tarantool" / "crossapp").exists()

    result = run(
        tt_cmd,
        project,
        "package",
        "pack",
        "--target",
        platform,
        "--format",
        "tt,rock",
        "--without-deps",
        "--registry",
        registry,
    )
    assert result.returncode == 0, result.stderr
    names = [Path(line).name for line in result.stdout.splitlines()]
    assert f"crossapp-1.0.0-{platform}.tt" in names
    assert f"crossapp-1.0.0-1.linux-{arch}.rock" in names


def other_platform() -> str:
    """A platform of Linux other than the machine."""
    return "linux/amd64" if os.uname().machine in ("aarch64", "arm64") else "linux/arm64"


def test_c_component_for_another_platform_fails_first(tt_cmd, tmp_path):
    """A c component builds for the machine alone: the build for another
    platform fails before anything is built, naming the component, the
    backend and the platform, with exit code 1."""
    project = make_project(tmp_path / "project", C_COMPONENT)
    (project / "cmod").mkdir()
    (project / "cmod" / "cmod.c").write_text("int luaopen_crossapp_cmod(void *L) { return 0; }\n")
    target = other_platform()

    result = run(
        tt_cmd,
        project,
        "package",
        "build",
        "--target",
        target,
        "--registry",
        str(project / "registry"),
    )
    assert result.returncode == 1, result.stderr
    assert 'building component "cmod": the build cannot be made for the target platform: ' in (
        result.stderr
    )
    assert "the c backend builds for the machine tt runs on alone" in result.stderr
    assert f"the build is for {target.replace('/', '-')}" in result.stderr
    assert not (project / "_build" / "target").exists()


def test_archive_with_dependencies_for_another_platform_is_refused(tt_cmd, tmp_path):
    """The .tt archive with its dependencies carries the runtime of the
    machine: a pack of it for another platform is refused before the build."""
    project = make_project(tmp_path / "project")

    result = run(
        tt_cmd,
        project,
        "package",
        "pack",
        "--target",
        other_platform(),
        "--registry",
        str(project / "registry"),
    )
    assert result.returncode == 1, result.stderr
    assert "a .tt archive with its dependencies is packed for the machine" in result.stderr
    assert "--without-deps" in result.stderr


def test_unknown_target_is_a_usage_error(tt_cmd, tmp_path):
    """A platform tt does not build for fails the parse of the flags."""
    project = make_project(tmp_path / "project")

    result = run(tt_cmd, project, "package", "build", "--target", "linux/riscv64")
    assert result.returncode == 1
    assert 'unknown platform "linux/riscv64": want linux/amd64, linux/arm64, ' in result.stderr
