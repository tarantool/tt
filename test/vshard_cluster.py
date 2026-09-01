import os
import platform
import shutil
import subprocess
from pathlib import Path

from utils import (
    create_tt_config,
    log_file,
    log_path,
    run_command_and_get_output,
    wait_string_in_file,
)

instances = ["router-001-a", "storage-001-a", "storage-001-b", "storage-002-a", "storage-002-b"]


# VshardCluster wraps tt environment with tnt 3 vshard cluster application.
class VshardCluster:
    def __init__(self, tt_cmd, env_dir: Path, app_name: str) -> None:
        env_dir = Path(env_dir)
        self.tt_cmd = tt_cmd
        self.instances = instances
        self.app_name = app_name

        if (env_dir / "tt.yaml").exists() or (env_dir / "tt.yml").exists():
            if env_dir.name == app_name:
                self.app_dir = env_dir
            else:
                # The session fixture copies a standalone application into a
                # pytest directory with a generated name. Nest it once more so
                # the config directory has the declarative application name.
                self.app_dir = env_dir / app_name
                self.app_dir.mkdir()
                for path in list(env_dir.iterdir()):
                    if path != self.app_dir:
                        shutil.move(path, self.app_dir / path.name)
            self.env_dir = self.app_dir
            print(f"Wrapping existing application in {self.app_dir}.")
            return

        create_tt_config(env_dir, "")

        rc, out = run_command_and_get_output(
            [tt_cmd, "create", "vshard_cluster", "--name", self.app_name, "-s", "-f"],
            cwd=env_dir,
        )
        assert rc == 0

        self.app_dir = env_dir / self.app_name
        shutil.move(env_dir / "tt.yaml", self.app_dir / "tt.yaml")
        self.env_dir = self.app_dir

    def build(self):
        rc, out = run_command_and_get_output(
            [self.tt_cmd, "package", "build"],
            cwd=self.app_dir,
        )
        assert rc == 0

    def start(self):
        start_cmd = [self.tt_cmd, "start", self.app_name]
        test_env = os.environ.copy()

        # Avoid too long path.
        if platform.system() == "Darwin":
            test_env["TT_LISTEN"] = ""
        rc, _ = run_command_and_get_output(start_cmd, cwd=self.app_dir, env=test_env)
        assert rc == 0

        wait_string_in_file(
            self.app_dir / log_path / "router-001-a" / log_file,
            "All replicas are ok",
        )

        for inst in ["storage-001-a", "storage-002-a"]:
            wait_string_in_file(
                self.app_dir / log_path / inst / log_file,
                "leaving orphan mode",
            )

        for inst in ["storage-001-b", "storage-002-b"]:
            wait_string_in_file(
                self.app_dir / log_path / inst / log_file,
                "subscribed replica",
            )

    def stop(self, inst=None):
        stop_arg = self.app_name
        if inst is not None:
            stop_arg = stop_arg + ":" + inst

        cmd = [self.tt_cmd, "stop", "-y", stop_arg]
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
