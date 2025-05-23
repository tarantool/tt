import os
import platform
import subprocess
from pathlib import Path

from utils import log_file, log_path, run_command_and_get_output, wait_string_in_file

instances = ["router-001-a", "storage-001-a", "storage-001-b", "storage-002-a", "storage-002-b"]


# VshardCluster wraps tt environment with tnt 3 vshard cluster application.
class VshardCluster:
    def __init__(self, tt_cmd, env_dir: Path, app_name: str) -> None:
        self.env_dir = env_dir
        self.tt_cmd = tt_cmd
        self.instances = instances
        self.app_name = app_name
        self.app_dir = self.env_dir / self.app_name

        if (self.env_dir / "tt.yaml").exists() or (self.env_dir / "tt.yml").exists():
            print(f"Wrapping existing application in {self.env_dir / self.app_name}.")
            return

        rc, out = run_command_and_get_output([tt_cmd, "init"], cwd=self.env_dir)
        assert rc == 0

        rc, out = run_command_and_get_output(
            [tt_cmd, "create", "vshard_cluster", "--name", self.app_name, "-s", "-f"],
            cwd=self.env_dir,
        )
        assert rc == 0

    def build(self):
        rc, out = run_command_and_get_output(
            [self.tt_cmd, "build", self.app_name],
            cwd=self.env_dir,
        )
        assert rc == 0

    def start(self):
        start_cmd = [self.tt_cmd, "start", self.app_name]
        test_env = os.environ.copy()

        # Avoid too long path.
        if platform.system() == "Darwin":
            test_env["TT_LISTEN"] = ""
        rc, _ = run_command_and_get_output(start_cmd, cwd=self.env_dir, env=test_env)
        assert rc == 0

        wait_string_in_file(
            self.env_dir / self.app_name / log_path / "router-001-a" / log_file,
            "All replicas are ok",
        )

        for inst in ["storage-001-a", "storage-002-a"]:
            wait_string_in_file(
                self.env_dir / self.app_name / log_path / inst / log_file,
                "leaving orphan mode",
            )

        for inst in ["storage-001-b", "storage-002-b"]:
            wait_string_in_file(
                self.env_dir / self.app_name / log_path / inst / log_file,
                "subscribed replica",
            )

    def stop(self, inst=None):
        stop_arg = self.app_name
        if inst is not None:
            stop_arg = stop_arg + ":" + inst

        cmd = [self.tt_cmd, "stop", "-y", stop_arg]
        rc, _ = run_command_and_get_output(cmd, cwd=self.env_dir)
        assert rc == 0

    def eval(self, instance, lua):
        process = subprocess.Popen(
            [self.tt_cmd, "connect", f"{self.app_name}:{instance}", "-f-"],
            cwd=self.env_dir,
            stdin=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        out, _ = process.communicate(lua, timeout=10)
        assert process.returncode == 0
        return out
