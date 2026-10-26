import os
import shutil
from pathlib import Path

# The application the tests install into a system root: a version with the
# script of an external process.
SYSTEMD_APP = Path(__file__).parent / "systemd_app"

# The pair of the host the tests run myapp from: two external processes with
# different restart policies and stop timeouts.
HOST_INSTANCES = """\
worker:
  binary: ./worker.sh
  restart: on-failure
  stop_timeout: 10s
api:
  binary: ./worker.sh
  args: [--api]
  restart: always
"""


def install_app(root: Path, tt_path, instances=HOST_INSTANCES):
    """Install myapp into the system root root the way tt package install
    lays an application out, with instances as the pair of the host, and
    link tt_path in as the tt of the scope, if given."""
    version = root / "versions" / "myapp" / "myapp_1.0.0"
    shutil.copytree(SYSTEMD_APP, version, symlinks=True)
    (root / "apps").mkdir(parents=True, exist_ok=True)
    os.symlink(os.path.join("..", "versions", "myapp", "myapp_1.0.0"), root / "apps" / "myapp")

    host = root / "etc" / "myapp"
    host.mkdir(parents=True)
    (host / "instances.yaml").write_text(instances)

    if tt_path is not None:
        (root / "bin").mkdir()
        os.symlink(tt_path, root / "bin" / "tt")
