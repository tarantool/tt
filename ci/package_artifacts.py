"""Validate GoReleaser output and stage release assets and test binaries."""

import argparse
import hashlib
import json
import shutil
import subprocess
from pathlib import Path


def collect(dist, variant, arch):
    targets = {("linux", arch)}
    if variant == "default":
        targets = {("linux", "amd64"), ("linux", "arm64"), ("darwin", "arm64")}
    binaries = {}
    for artifact in json.loads((dist / "artifacts.json").read_text()):
        if artifact["type"] != "Binary":
            continue
        target = (artifact["goos"], artifact["goarch"])
        if target in binaries:
            raise ValueError(f"Duplicate binary for {target}")
        binaries[target] = Path(artifact["path"])
    if binaries.keys() != targets:
        raise ValueError(f"Expected binaries {targets}, got {binaries.keys()}")

    for (goos, goarch), path in binaries.items():
        metadata = subprocess.check_output(["go", "version", "-m", path], text=True)
        cgo = "0" if variant == "default" else "1"
        if f"CGO_ENABLED={cgo}" not in metadata:
            raise ValueError(f"Unexpected CGO setting in {path}:\n{metadata}")
        if variant == "openssl_static" and "openssl,openssl_static" not in metadata:
            raise ValueError(f"Missing OpenSSL build tags in {path}:\n{metadata}")
        if goos == "linux":
            headers = subprocess.check_output(["readelf", "-l", "-d", path], text=True)
            if "INTERP" in headers or "NEEDED" in headers:
                raise ValueError(f"{path} is dynamically linked")
        output = Path("dist/binaries") / f"{goos}-{goarch}" / "tt"
        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, output)
        print(f"{goos}/{goarch}: {hashlib.sha256(path.read_bytes()).hexdigest()} {path}")

    linux_count = sum(goos == "linux" for goos, _ in targets)
    assets = []
    for pattern, count in [
        ("*.deb", linux_count),
        ("*.rpm", linux_count),
        ("*.tar.gz", len(targets)),
        ("*checksums.txt", 1),
    ]:
        files = list(dist.glob(pattern))
        if len(files) != count:
            raise ValueError(f"Expected {count} {pattern} files, got {files}")
        assets.extend(files)
    subprocess.run(["sha256sum", "--check", assets[-1].name], cwd=dist, check=True)
    destination = Path("dist/assets")
    destination.mkdir(parents=True, exist_ok=True)
    for asset in assets:
        target = destination / asset.name
        if target.exists():
            raise ValueError(f"Duplicate release asset: {target}")
        shutil.copy2(asset, target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dist", type=Path)
    parser.add_argument("--variant", choices=["default", "openssl_static"], required=True)
    parser.add_argument("--arch", choices=["amd64", "arm64"], required=True)
    args = parser.parse_args()
    collect(args.dist, args.variant, args.arch)


if __name__ == "__main__":
    main()
