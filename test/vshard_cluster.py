import os
import platform
import subprocess
import time
import tomllib
from pathlib import Path

from utils import (
    inst_log_dir,
    instances_file,
    log_file,
    run_command_and_get_output,
    wait_string_in_file,
)

instances = ["router-001-a", "storage-001-a", "storage-001-b", "storage-002-a", "storage-002-b"]


def project_name(app_dir: Path) -> str:
    """Return the name of the project in app_dir: [package].name of its
    manifest, or the name of the directory without one."""
    manifest = app_dir / "app.manifest.toml"
    if manifest.exists():
        with open(manifest, "rb") as f:
            return tomllib.load(f)["package"]["name"]
    return app_dir.name


# VshardCluster wraps a project with tnt 3 vshard cluster application.
class VshardCluster:
    def __init__(self, tt_cmd, env_dir: Path, dir_name: str) -> None:
        """Wrap the project in env_dir or in env_dir/dir_name, or create one
        in env_dir/dir_name with tt create. The application is named by the
        manifest tt create writes, app_name."""
        env_dir = Path(env_dir)
        self.tt_cmd = tt_cmd
        self.instances = instances

        if (env_dir / instances_file).exists():
            self.app_dir = env_dir
        elif (env_dir / dir_name / instances_file).exists():
            self.app_dir = env_dir / dir_name
        else:
            rc, _ = run_command_and_get_output(
                [tt_cmd, "create", "vshard_cluster", "--name", dir_name, "-s", "-f"],
                cwd=env_dir,
            )
            assert rc == 0

            # tt create lays the project out: the pair is in
            # .templates/default/.
            self.app_dir = env_dir / dir_name

        self.env_dir = self.app_dir
        self.app_name = project_name(self.app_dir)
        print(f"Wrapping application {self.app_name} in {self.app_dir}.")

    def build(self, attempts=3):
        # The build downloads vshard from rocks.tarantool.org, which drops a
        # download now and then; rerunning it is safe.
        for attempt in range(1, attempts + 1):
            rc, out = run_command_and_get_output(
                [self.tt_cmd, "package", "build"],
                cwd=self.app_dir,
            )
            if rc == 0:
                return
            print(f"tt package build attempt {attempt}/{attempts} failed:\n{out}")
            if attempt < attempts:
                time.sleep(5)
        assert rc == 0, out

    def start(self):
        start_cmd = [self.tt_cmd, "instance", "start", f"{self.app_name}:*"]
        test_env = os.environ.copy()

        # Avoid too long path.
        if platform.system() == "Darwin":
            test_env["TT_LISTEN"] = ""
        rc, _ = run_command_and_get_output(start_cmd, cwd=self.app_dir, env=test_env)
        assert rc == 0

        def log(inst):
            return Path(inst_log_dir(self.app_dir, self.app_name, inst)) / log_file

        wait_string_in_file(log("router-001-a"), "All replicas are ok")

        for inst in ["storage-001-a", "storage-002-a"]:
            wait_string_in_file(log(inst), "leaving orphan mode")

        for inst in ["storage-001-b", "storage-002-b"]:
            wait_string_in_file(log(inst), "subscribed replica")

    def stop(self, inst=None):
        stop_arg = f"{self.app_name}:*"
        if inst is not None:
            stop_arg = f"{self.app_name}:{inst}"

        cmd = [self.tt_cmd, "instance", "stop", "-y", stop_arg]
        rc, _ = run_command_and_get_output(cmd, cwd=self.app_dir)
        assert rc == 0

    def eval(self, instance, lua):
        process = subprocess.Popen(
            [self.tt_cmd, "connect", f"{self.app_name}:{instance}", "-f-"],
            cwd=self.app_dir,
            stdin=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        out, _ = process.communicate(lua, timeout=10)
        assert process.returncode == 0
        return out
