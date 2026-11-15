"""`tt package clean` and the lock of a project.

clean removes what the builds made - _build/ but its lock, and the rock of the
package in .rocks/ - and keeps the dependencies, the manifest, the lock and
the sources. The commands that change a project hold its lock: two builds of
one project run one after the other, and a tt a hook runs on the project
works under the lock of the tt that runs it rather than wait for it.

Everything resolves against the fixture rock repository as a local directory
registry, so the suite stays offline.
"""

import os
import subprocess
from pathlib import Path

ROCKS_REPO = Path(__file__).parent.parent / "rocks" / "repo"

MANIFEST = """manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
stat = '>=0.3.0'

[products.default]
components = ['lua', 'gen']
default = true

[components.lua]
path = 'src'

[components.gen]
path = 'gen'

[components.gen.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'echo run >> ../runs.txt && echo "return 1" > gen.lua']
output = [{ path = 'gen.lua', to = 'share' }]
incremental = true

[hooks.clean]
command = 'sh'
args = ['-c', 'rm -f gen/gen.lua']
"""


def run(tt_cmd, cwd: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run([str(tt_cmd), *args], cwd=cwd, capture_output=True, text=True)


def make_project(root: Path, manifest: str = MANIFEST) -> Path:
    (root / "src").mkdir(parents=True)
    (root / "src" / "init.lua").write_text("return {}\n")
    (root / "gen").mkdir()
    (root / "gen" / "main.txt").write_text("the sources of gen\n")
    # What the build generates is no input of its incremental build.
    (root / "gen" / ".gitignore").write_text("gen.lua\n")
    (root / "app.manifest.toml").write_text(manifest)
    return root


def build(tt_cmd, project: Path, *args: str) -> subprocess.CompletedProcess:
    return run(tt_cmd, project, "package", "build", "--registry", str(ROCKS_REPO), *args)


def runs(project: Path) -> int:
    return len((project / "runs.txt").read_text().splitlines())


def test_clean_removes_what_the_build_made(tt_cmd, tmp_path):
    project = make_project(tmp_path / "project")
    result = build(tt_cmd, project)
    assert result.returncode == 0, result.stderr

    share = project / ".rocks" / "share" / "tarantool"
    assert (share / "my-app" / "gen.lua").exists()
    assert (share / "stat").is_dir()

    # A dry run says what would go, and removes nothing.
    result = run(tt_cmd, project, "package", "clean", "--dry-run")
    assert result.returncode == 0, result.stderr
    lines = result.stdout.splitlines()
    assert "would run the clean hook" in lines
    assert "would remove _build/results.json" in lines
    assert any(line.startswith("would remove my-app ") for line in lines), lines
    assert "  .rocks/share/tarantool/my-app/gen.lua" in lines
    assert (share / "my-app" / "gen.lua").exists()
    assert (project / "_build" / "results.json").exists()

    result = run(tt_cmd, project, "package", "clean")
    assert result.returncode == 0, result.stderr
    assert "ran the clean hook" in result.stdout
    assert "removed _build/results.json" in result.stdout

    assert sorted(p.name for p in (project / "_build").iterdir()) == ["tt.lock"]
    assert not (share / "my-app").exists()
    assert not (project / "gen" / "gen.lua").exists(), "the clean hook ran"
    assert (share / "stat").is_dir(), "the dependencies stay"
    assert (project / "app.manifest.lock").exists()
    assert (project / "src" / "init.lua").exists()

    result = run(tt_cmd, project, "package", "clean")
    assert result.returncode == 0, result.stderr
    assert result.stdout == "ran the clean hook\n"

    result = build(tt_cmd, project)
    assert result.returncode == 0, result.stderr
    assert (share / "my-app" / "gen.lua").exists()


def test_clean_of_a_component_builds_it_again(tt_cmd, tmp_path):
    project = make_project(tmp_path / "project")
    assert build(tt_cmd, project).returncode == 0
    assert build(tt_cmd, project).returncode == 0
    assert runs(project) == 1, "the incremental build is skipped while nothing changed"

    result = run(tt_cmd, project, "package", "clean", "gen")
    assert result.returncode == 0, result.stderr
    assert "removed _build/component/gen" in result.stdout
    assert "removed _build/cache/gen.json" in result.stdout
    assert "clean hook" not in result.stdout
    assert (project / "gen" / "gen.lua").exists(), "the hook runs on a clean of the project"
    assert not (project / ".rocks" / "share" / "tarantool" / "my-app").exists()

    result = build(tt_cmd, project)
    assert result.returncode == 0, result.stderr
    assert runs(project) == 2, "a component cleaned is built again"

    result = run(tt_cmd, project, "package", "clean", "nope")
    assert result.returncode == 1
    assert 'the manifest declares no component "nope"' in result.stderr


BUSY_MANIFEST = MANIFEST.replace(
    "args = ['-c', 'echo run >> ../runs.txt && echo \"return 1\" > gen.lua']\n"
    "output = [{ path = 'gen.lua', to = 'share' }]\n"
    "incremental = true\n",
    "args = ['-c', 'mkdir ../busy && sleep 2 && rmdir ../busy']\n",
)


def test_builds_of_one_project_wait_for_each_other(tt_cmd, tmp_path):
    project = make_project(tmp_path / "project", BUSY_MANIFEST)
    # The lock and .rocks first, so that both builds only build.
    assert build(tt_cmd, project).returncode == 0

    cmd = [str(tt_cmd), "package", "build", "--registry", str(ROCKS_REPO)]
    builds = [
        subprocess.Popen(
            cmd,
            cwd=project,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        for _ in range(2)
    ]
    outputs = [process.communicate(timeout=120) for process in builds]

    for process, (_, stderr) in zip(builds, outputs):
        assert process.returncode == 0, stderr

    waited = [stderr for _, stderr in outputs if "waiting for another tt working on" in stderr]
    assert len(waited) == 1, outputs
    assert f"(pid {builds[0].pid})" in waited[0] or f"(pid {builds[1].pid})" in waited[0]


def test_tt_of_a_hook_works_under_the_lock(tt_cmd, tmp_path):
    # tt package fetch takes the lock as a build does, and needs no Tarantool.
    hook = f"""
[hooks.post_build]
command = '{tt_cmd}'
args = ['package', 'fetch', '--registry', '{ROCKS_REPO}']
"""
    project = make_project(tmp_path / "project", MANIFEST + hook)

    result = subprocess.run(
        [str(tt_cmd), "package", "build", "--registry", str(ROCKS_REPO)],
        cwd=project,
        capture_output=True,
        text=True,
        timeout=120,
        env={k: v for k, v in os.environ.items() if k != "TT_PROJECT_LOCK"},
    )
    assert result.returncode == 0, result.stderr
    assert "waiting" not in result.stderr
